package index

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codepilot/internal/chunk"
	"codepilot/internal/embed"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// PassageEmbedder — минимальный интерфейс эмбеддера для сохранения векторов.
type PassageEmbedder interface {
	EmbedPassages(texts []string) ([][]float32, error)
}

// PGStore — хранилище индекса в Postgres + pgvector: чанки, манифест и
// векторы e5. BM25/TF-IDF по-прежнему строятся в памяти при Load.
type PGStore struct {
	pool *pgxpool.Pool
}

// pgSchema — DDL хранилища. project изолирует несколько репозиториев в одной БД.
var pgSchema = `
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS meta (
	project TEXT NOT NULL,
	key     TEXT NOT NULL,
	value   TEXT,
	PRIMARY KEY (project, key)
);
CREATE TABLE IF NOT EXISTS chunks (
	project     TEXT NOT NULL,
	id          TEXT NOT NULL,
	file_path   TEXT NOT NULL,
	language    TEXT,
	start_line  INTEGER,
	end_line    INTEGER,
	symbol_name TEXT,
	kind        TEXT,
	signature   TEXT,
	doc         TEXT,
	content     TEXT,
	hash        TEXT,
	embedding   vector(` + fmt.Sprint(embed.Dim) + `),
	PRIMARY KEY (project, id)
);
CREATE INDEX IF NOT EXISTS idx_chunks_embedding
	ON chunks USING hnsw (embedding vector_cosine_ops);
`

// OpenPG подключается к Postgres по DSN и гарантирует схему.
// Схема создаётся отдельным соединением до пула: регистрация типов pgvector
// в AfterConnect требует, чтобы расширение vector уже существовало.
func OpenPG(dsn string) (*PGStore, error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: connect: %w", err)
	}
	if _, err := conn.Exec(ctx, pgSchema); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("pg: schema (расширение vector установлено?): %w", err)
	}
	conn.Close(ctx)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: DSN: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		return pgxvec.RegisterTypes(ctx, c)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg: pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: ping: %w", err)
	}
	return &PGStore{pool: pool}, nil
}

// Close закрывает пул соединений.
func (s *PGStore) Close() { s.pool.Close() }

// embedBatch — размер батча при прогоне чанков через эмбеддер.
const embedBatch = 32

// saveCommitBatch — сколько чанков пишется в одной транзакции. Порционные
// коммиты делают индексацию перезапускаемой: при обрыве (таймаут, сон
// машины) уже записанные вектора не теряются, следующий запуск пересчитает
// только чанки без вектора.
const saveCommitBatch = 2048

// Save записывает индекс проекта в Postgres. Эмбеддинги считаются только для
// новых и изменённых чанков (по hash файла), остальные строки не трогаются.
func (s *PGStore) Save(ix *Index, emb PassageEmbedder) (embedded int, err error) {
	ctx := context.Background()
	project := ix.ProjectRoot
	type row struct {
		hash   string
		hasVec bool
	}
	existing := map[string]row{} // id -> состояние строки
	rows, err := s.pool.Query(ctx,
		`SELECT id, hash, embedding IS NOT NULL FROM chunks WHERE project = $1`, project)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id, h string
		var hv bool
		if err := rows.Scan(&id, &h, &hv); err != nil {
			rows.Close()
			return 0, err
		}
		existing[id] = row{h, hv}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var need []int // индексы чанков без актуального вектора
	for i, c := range ix.Chunks {
		ex, ok := existing[c.ID]
		if !ok || ex.hash != c.Hash || (emb != nil && !ex.hasVec) {
			need = append(need, i)
		}
	}
	if emb != nil && len(need) > 0 {
		Logf("pg: векторов к пересчёту %d (батчи по %d, коммит каждые %d)", len(need), embedBatch, saveCommitBatch)
	}

	upsert := `INSERT INTO chunks
		(project, id, file_path, language, start_line, end_line,
		 symbol_name, kind, signature, doc, content, hash, embedding)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (project, id) DO UPDATE SET
		file_path=$3, language=$4, start_line=$5, end_line=$6,
		symbol_name=$7, kind=$8, signature=$9, doc=$10, content=$11,
		hash=$12, embedding=$13`

	for off := 0; off < len(need); off += saveCommitBatch {
		// Отмена между порционными коммитами: записанное не теряется.
		if CheckAbort != nil && CheckAbort() {
			return embedded, ErrAborted
		}
		end := off + saveCommitBatch
		if end > len(need) {
			end = len(need)
		}
		part := need[off:end]
		vectors := map[int][]float32{}
		if emb != nil {
			for boff := 0; boff < len(part); boff += embedBatch {
				bend := boff + embedBatch
				if bend > len(part) {
					bend = len(part)
				}
				texts := make([]string, 0, bend-boff)
				for _, i := range part[boff:bend] {
					texts = append(texts, passageText(ix.Chunks[i]))
				}
				vecs, err := emb.EmbedPassages(texts)
				if err != nil {
					return embedded, fmt.Errorf("pg: эмбеддинги: %w", err)
				}
				for j, i := range part[boff:bend] {
					vectors[i] = vecs[j]
				}
				if b := boff / embedBatch; b%16 == 0 {
					Logf("pg: эмбеддинги %d/%d", off+bend, len(need))
				}
			}
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return embedded, err
		}
		for _, i := range part {
			c := ix.Chunks[i]
			var ev interface{}
			if v, ok := vectors[i]; ok {
				ev = pgvector.NewVector(v)
			}
			if _, err := tx.Exec(ctx, upsert,
				project, c.ID, c.FilePath, c.Language, c.StartLine, c.EndLine,
				c.SymbolName, c.Kind, c.Signature, c.Doc, c.Content, c.Hash, ev); err != nil {
				tx.Rollback(ctx)
				return embedded, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return embedded, err
		}
		if emb != nil {
			embedded += len(part)
		}
		Logf("pg: сохранено %d/%d чанков", end, len(need))
	}

	// финальная транзакция: вычистить чанки удалённых файлов и записать мету
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return embedded, err
	}
	defer tx.Rollback(ctx)
	// убрать чанки удалённых/переименованных файлов
	ids := make([]string, len(ix.Chunks))
	for i, c := range ix.Chunks {
		ids[i] = c.ID
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM chunks WHERE project = $1 AND id <> ALL($2)`, project, ids); err != nil {
		return embedded, err
	}
	manifest, err := json.Marshal(ix.Manifest)
	if err != nil {
		return embedded, err
	}
	for k, v := range map[string]string{
		"project_root":    ix.ProjectRoot,
		"chunker_version": fmt.Sprint(ix.ChunkerVersion),
		"manifest":        string(manifest),
	} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO meta (project, key, value) VALUES ($1,$2,$3)
			 ON CONFLICT (project, key) DO UPDATE SET value = $3`, project, k, v); err != nil {
			return embedded, err
		}
	}
	return embedded, tx.Commit(ctx)
}

// passageText — текст чанка для эмбеддинга: символ, сигнатура, doc, тело.
func passageText(c chunk.Chunk) string {
	var b strings.Builder
	for _, s := range []string{c.SymbolName, c.Signature, c.Doc, c.Content} {
		if s != "" {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// VecHit — результат векторного поиска: id чанка и косинусная близость.
type VecHit struct {
	ID    string
	Score float64
}

// VecSearch возвращает k ближайших чанков проекта по косинусной близости.
func (s *PGStore) VecSearch(project string, vec []float32, k int) ([]VecHit, error) {
	rows, err := s.pool.Query(context.Background(),
		`SELECT id, 1 - (embedding <=> $2) AS sim
		 FROM chunks
		 WHERE project = $1 AND embedding IS NOT NULL
		 ORDER BY embedding <=> $2
		 LIMIT $3`, project, pgvector.NewVector(vec), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []VecHit
	for rows.Next() {
		var h VecHit
		if err := rows.Scan(&h.ID, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Load читает индекс проекта из Postgres и перестраивает модели поиска.
func (s *PGStore) Load(project string) (*Index, error) {
	ctx := context.Background()
	ix := &Index{Manifest: map[string]string{}}
	mrows, err := s.pool.Query(ctx,
		`SELECT key, value FROM meta WHERE project = $1`, project)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var k, v string
		if err := mrows.Scan(&k, &v); err != nil {
			mrows.Close()
			return nil, err
		}
		switch k {
		case "project_root":
			ix.ProjectRoot = v
		case "chunker_version":
			fmt.Sscan(v, &ix.ChunkerVersion)
		case "manifest":
			_ = json.Unmarshal([]byte(v), &ix.Manifest)
		}
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	crows, err := s.pool.Query(ctx, `SELECT id, file_path, language, start_line, end_line,
		symbol_name, kind, signature, doc, content, hash
		FROM chunks WHERE project = $1`, project)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c chunk.Chunk
		if err := crows.Scan(&c.ID, &c.FilePath, &c.Language, &c.StartLine, &c.EndLine,
			&c.SymbolName, &c.Kind, &c.Signature, &c.Doc, &c.Content, &c.Hash); err != nil {
			return nil, err
		}
		ix.Chunks = append(ix.Chunks, c)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	if len(ix.Chunks) == 0 {
		return nil, fmt.Errorf("проект %s не найден в pg-индексе; сначала: codepilot index %s --store pg", project, project)
	}
	ix.buildModel()
	return ix, nil
}
