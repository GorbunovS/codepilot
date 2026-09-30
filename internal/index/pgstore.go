package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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

// pgSchema — DDL общей части хранилища. Чанки и вектора лежат в отдельной
// таблице на каждый проект (chunksTable), здесь только meta.
var pgSchema = `
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS meta (
	project TEXT NOT NULL,
	key     TEXT NOT NULL,
	value   TEXT,
	PRIMARY KEY (project, key)
);
`

// chunksTable — имя таблицы проекта: chunks_<hex(sha256 пути)[:16]>.
// Таблица на проект, а не строки с ключом project в общей таблице:
// смешивание векторов разных проектов структурно невозможно — каждый
// запрос обращается только к таблице своего проекта, а HNSW-индекс
// строится по чанкам одного проекта. Имя — hex-константа, безопасно
// подставляется в SQL без параметризации.
func chunksTable(project string) string {
	sum := sha256.Sum256([]byte(project))
	return "chunks_" + hex.EncodeToString(sum[:])[:16]
}

// vecIndexName — имя HNSW-индекса таблицы проекта (имена индексов в
// Postgres глобальны на схему, поэтому суффикс повторяет таблицу).
func vecIndexName(tbl string) string { return "idx_vec_" + strings.TrimPrefix(tbl, "chunks_") }

// createTableSQL — DDL таблицы проекта.
func createTableSQL(tbl string) string {
	return `CREATE TABLE IF NOT EXISTS ` + tbl + ` (
	id          TEXT PRIMARY KEY,
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
	embedding   vector(` + fmt.Sprint(embed.Dim) + `)
)`
}

// vecIndexSQL — HNSW по косинусной близости для VecSearch.
func vecIndexSQL(tbl string) string {
	return `CREATE INDEX IF NOT EXISTS ` + vecIndexName(tbl) +
		` ON ` + tbl + ` USING hnsw (embedding vector_cosine_ops)`
}

// bulkThreshold — сколько новых векторов считается «массовой заливкой»: на таком
// объёме HNSW-индекс дешевле перестроить после вставок, чем поддерживать на
// каждой вставке. На малых инкрементах индекс остаётся на месте.
const bulkThreshold = 4096

// ensureTable создаёт таблицу проекта, если её ещё нет.
func (s *PGStore) ensureTable(ctx context.Context, tbl string) error {
	_, err := s.pool.Exec(ctx, createTableSQL(tbl))
	return err
}

// migrateLegacy переносит строки легаси-таблицы chunks (с колонкой project)
// в таблицы проектов и удаляет её. No-op, если легаси-таблицы нет.
func migrateLegacy(ctx context.Context, conn *pgx.Conn) error {
	var reg *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.chunks')::text`).Scan(&reg); err != nil {
		return err
	}
	if reg == nil {
		return nil
	}
	prows, err := conn.Query(ctx, `SELECT DISTINCT project FROM chunks`)
	if err != nil {
		return err
	}
	var projects []string
	for prows.Next() {
		var p string
		if err := prows.Scan(&p); err != nil {
			prows.Close()
			return err
		}
		projects = append(projects, p)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return err
	}
	for _, p := range projects {
		tbl := chunksTable(p)
		Logf("pg: миграция легаси-таблицы chunks → %s (проект %s)", tbl, p)
		if _, err := conn.Exec(ctx, createTableSQL(tbl)); err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, `INSERT INTO `+tbl+`
			(id, file_path, language, start_line, end_line,
			 symbol_name, kind, signature, doc, content, hash, embedding)
			SELECT id, file_path, language, start_line, end_line,
			 symbol_name, kind, signature, doc, content, hash, embedding
			FROM chunks WHERE project = $1
			ON CONFLICT (id) DO NOTHING`, p); err != nil {
			return err
		}
	}
	// легаси-индекс и таблица уходят вместе; HNSW пересоздастся при Save
	if _, err := conn.Exec(ctx, `DROP TABLE chunks`); err != nil {
		return err
	}
	return nil
}

// createVecIndex (пере)создаёт HNSW-индекс после массовой заливки.
// maintenance_work_mem поднимаем сессионно: построение HNSW упирается в память.
// Параллельную сборку отключаем: она требует dynamic shared memory, а в
// Docker-контейнере /dev/shm по умолчанию 64 МБ — сборка падает с
// «could not resize shared memory segment».
func (s *PGStore) createVecIndex(ctx context.Context, tbl string) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET maintenance_work_mem = '512MB'`); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `SET max_parallel_maintenance_workers = 0`); err != nil {
		return err
	}
	Logf("pg: перестроение HNSW-индекса %s…", vecIndexName(tbl))
	_, err = conn.Exec(ctx, vecIndexSQL(tbl))
	return err
}

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
	if err := migrateLegacy(ctx, conn); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("pg: миграция легаси-таблицы: %w", err)
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

// embedBatch — размер батча при прогоне чанков через эмбеддер. Не поднимать
// выше 32: формы [128, *] отправляют компилятор CoreML в бесконечную сборку,
// на [32, 128|256|512] CoreML работает (~2x CPU на M1 Max).
const embedBatch = 32

// batchFor возвращает размер батча под эмбеддер: удалённый (MLX-сайдкар)
// берёт крупнее — у него накладные расходы на HTTP-раундтрип, а не на форму.
func batchFor(emb PassageEmbedder) int {
	type preferrer interface{ PreferredBatch() int }
	if p, ok := emb.(preferrer); ok && p.PreferredBatch() > 0 {
		return p.PreferredBatch()
	}
	return embedBatch
}

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
	tbl := chunksTable(project)
	if err := s.ensureTable(ctx, tbl); err != nil {
		return 0, fmt.Errorf("pg: таблица проекта: %w", err)
	}
	type row struct {
		hash   string
		hasVec bool
	}
	existing := map[string]row{} // id -> состояние строки
	rows, err := s.pool.Query(ctx,
		`SELECT id, hash, embedding IS NOT NULL FROM `+tbl)
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
		// Сортируем по длине текста: батчи получаются однородными, все три
		// ведра паддинга встречаются компактно — стабильные формы для CoreML
		// и меньше лишнего паддинга на CPU.
		sort.Slice(need, func(a, b int) bool {
			return len(passageText(ix.Chunks[need[a]])) < len(passageText(ix.Chunks[need[b]]))
		})
		Logf("pg: векторов к пересчёту %d (батчи по %d, коммит каждые %d)", len(need), batchFor(emb), saveCommitBatch)
	}
	// Массовая заливка: HNSW-индекс дешевле перестроить в конце, чем платить
	// за обновление графа на каждой вставке (оно и даёт прогрессивный тормоз).
	bulk := len(need) > bulkThreshold
	if bulk {
		if _, err := s.pool.Exec(ctx, `DROP INDEX IF EXISTS `+vecIndexName(tbl)); err != nil {
			return 0, fmt.Errorf("pg: drop vec index: %w", err)
		}
		defer func() {
			// Пересоздаём даже при обрыве: без индекса VecSearch деградирует
			// до seq scan (медленно, но корректно) — не оставляем так навсегда.
			if ierr := s.createVecIndex(context.Background(), tbl); ierr != nil {
				Logf("pg: не удалось пересоздать HNSW-индекс: %v", ierr)
			}
		}()
	}

	upsert := `INSERT INTO ` + tbl + `
		(id, file_path, language, start_line, end_line,
		 symbol_name, kind, signature, doc, content, hash, embedding)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET
		file_path=$2, language=$3, start_line=$4, end_line=$5,
		symbol_name=$6, kind=$7, signature=$8, doc=$9, content=$10,
		hash=$11, embedding=$12`

	batch := embedBatch
	if emb != nil {
		batch = batchFor(emb)
	}
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
			for boff := 0; boff < len(part); boff += batch {
				bend := boff + batch
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
				if b := boff / batch; b%2 == 0 {
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
				c.ID, c.FilePath, c.Language, c.StartLine, c.EndLine,
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
		`DELETE FROM `+tbl+` WHERE id <> ALL($1)`, ids); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return embedded, err
	}
	// Гарантируем наличие HNSW-индекса и на малых инкрементах (свежая БД,
	// маленький проект): CREATE IF NOT EXISTS — no-op, если уже есть.
	if !bulk {
		if err := s.createVecIndex(ctx, tbl); err != nil {
			Logf("pg: не удалось создать HNSW-индекс: %v", err)
		}
	}
	return embedded, nil
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
		`SELECT id, 1 - (embedding <=> $1) AS sim
		 FROM `+chunksTable(project)+`
		 WHERE embedding IS NOT NULL
		 ORDER BY embedding <=> $1
		 LIMIT $2`, pgvector.NewVector(vec), k)
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
	tbl := chunksTable(project)
	// ensureTable, чтобы свежий проект падал с понятной «не найден»,
	// а не с «relation does not exist»
	if err := s.ensureTable(ctx, tbl); err != nil {
		return nil, fmt.Errorf("pg: таблица проекта: %w", err)
	}
	ix, err := s.readProject(ctx, project, tbl)
	if err != nil {
		return nil, err
	}
	if len(ix.Chunks) == 0 {
		return nil, fmt.Errorf("проект %s не найден в pg-индексе; сначала: codepilot index %s --store pg", project, project)
	}
	ix.buildModel()
	return ix, nil
}

// Prev возвращает сохранённое состояние проекта (чанки + манифест) без
// построения поисковых моделей — предыдущее состояние для инкрементального
// BuildPrev в pg-режиме. nil, если проекта в базе нет.
func (s *PGStore) Prev(project string) *Index {
	ix, err := s.readProject(context.Background(), project, chunksTable(project))
	if err != nil || len(ix.Chunks) == 0 {
		return nil
	}
	return ix
}

// readProject читает meta и чанки проекта из его таблицы.
func (s *PGStore) readProject(ctx context.Context, project, tbl string) (*Index, error) {
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
		FROM `+tbl)
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
	return ix, nil
}
