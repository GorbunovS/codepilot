package index

import (
	"context"
	"os"
	"testing"

	"codepilot/internal/chunk"

	"github.com/jackc/pgx/v5"
)

// stubEmbedder — детерминированные векторы для интеграционного теста:
// "a" → e1, "b" → e2, остальное — e3.
type stubEmbedder struct{}

func (stubEmbedder) EmbedPassages(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 384)
		switch {
		case len(t) > 0 && t[0] == 'a':
			v[0] = 1
		case len(t) > 0 && t[0] == 'b':
			v[1] = 1
		default:
			v[2] = 1
		}
		out[i] = v
	}
	return out, nil
}

func testPGStore(t *testing.T) *PGStore {
	t.Helper()
	dsn := os.Getenv("CODEPILOT_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CODEPILOT_PG_TEST_DSN не задан (нужен Postgres с pgvector)")
	}
	s, err := OpenPG(dsn)
	if err != nil {
		t.Fatalf("OpenPG: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPGStoreRoundTrip(t *testing.T) {
	s := testPGStore(t)
	project := "test://pgstore-roundtrip"
	ctx := context.Background()
	defer func() {
		_, _ = s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+chunksTable(project))
		_, _ = s.pool.Exec(ctx, `DELETE FROM meta WHERE project = $1`, project)
	}()

	mk := func() *Index {
		return &Index{
			ProjectRoot:    project,
			ChunkerVersion: 1,
			Manifest:       map[string]string{"a.go": "h1", "b.go": "h2"},
			Chunks: []chunk.Chunk{
				{ID: "a.go:1:A", FilePath: "a.go", StartLine: 1, EndLine: 5, SymbolName: "A", Kind: "func", Content: "aaa", Hash: "h1"},
				{ID: "b.go:1:B", FilePath: "b.go", StartLine: 1, EndLine: 5, SymbolName: "B", Kind: "func", Content: "bbb", Hash: "h2"},
			},
		}
	}
	ix := mk()
	embedded, err := s.Save(ix, stubEmbedder{})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if embedded != 2 {
		t.Fatalf("embedded = %d, ожидалось 2", embedded)
	}

	loaded, err := s.Load(project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Chunks) != 2 {
		t.Fatalf("чанков %d, ожидалось 2", len(loaded.Chunks))
	}
	if loaded.Manifest["a.go"] != "h1" {
		t.Fatalf("манифест потерян: %v", loaded.Manifest)
	}

	// повторный Save без изменений — векторы не пересчитываются
	embedded, err = s.Save(mk(), stubEmbedder{})
	if err != nil {
		t.Fatalf("Save (повтор): %v", err)
	}
	if embedded != 0 {
		t.Fatalf("embedded на no-op = %d, ожидалось 0", embedded)
	}

	// изменение хеша одного файла → переэмбеддинг только его чанка
	ix2 := mk()
	ix2.Chunks[1].Hash = "h2'"
	ix2.Manifest["b.go"] = "h2'"
	embedded, err = s.Save(ix2, stubEmbedder{})
	if err != nil {
		t.Fatalf("Save (изменение): %v", err)
	}
	if embedded != 1 {
		t.Fatalf("embedded при изменении = %d, ожидалось 1", embedded)
	}

	// векторный поиск: запрос "a" ближе к чанку A
	hits, err := s.VecSearch(project, stubVec(0), 2)
	if err != nil {
		t.Fatalf("VecSearch: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != "a.go:1:A" {
		t.Fatalf("VecSearch топ-1 = %+v, ожидался a.go:1:A", hits)
	}

	// удаление файла b.go из проекта → его чанк исчезает
	ix3 := mk()
	ix3.Chunks = ix3.Chunks[:1]
	ix3.Manifest = map[string]string{"a.go": "h1"}
	if _, err := s.Save(ix3, stubEmbedder{}); err != nil {
		t.Fatalf("Save (удаление): %v", err)
	}
	loaded, err = s.Load(project)
	if err != nil {
		t.Fatalf("Load (после удаления): %v", err)
	}
	if len(loaded.Chunks) != 1 || loaded.Chunks[0].ID != "a.go:1:A" {
		t.Fatalf("после удаления: %+v", loaded.Chunks)
	}
}

// stubVec — единичный вектор по оси i (совпадает с разметкой stubEmbedder).
func stubVec(i int) []float32 {
	v := make([]float32, 384)
	v[i] = 1
	return v
}

// TestPGStoreProjectIsolation — у каждого проекта своя таблица: поиск и
// загрузка одного проекта физически не видят чанки и векторы другого.
func TestPGStoreProjectIsolation(t *testing.T) {
	s := testPGStore(t)
	ctx := context.Background()
	pa, pb := "test://pgstore-iso-a", "test://pgstore-iso-b"
	defer func() {
		for _, p := range []string{pa, pb} {
			_, _ = s.pool.Exec(ctx, `DROP TABLE IF EXISTS `+chunksTable(p))
			_, _ = s.pool.Exec(ctx, `DELETE FROM meta WHERE project = $1`, p)
		}
	}()

	save := func(project, id, content string) {
		ix := &Index{
			ProjectRoot:    project,
			ChunkerVersion: 1,
			Manifest:       map[string]string{"f.go": "h"},
			Chunks: []chunk.Chunk{
				{ID: id, FilePath: "f.go", StartLine: 1, EndLine: 3, SymbolName: "F", Kind: "func", Content: content, Hash: "h"},
			},
		}
		if _, err := s.Save(ix, stubEmbedder{}); err != nil {
			t.Fatalf("Save %s: %v", project, err)
		}
	}
	save(pa, "f.go:1:A", "aaa") // вектор e1
	save(pb, "f.go:1:B", "bbb") // вектор e2

	// векторный поиск по B с запросом-вектором проекта A: видит только B
	hits, err := s.VecSearch(pb, stubVec(0), 10)
	if err != nil {
		t.Fatalf("VecSearch: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "f.go:1:B" {
		t.Fatalf("VecSearch по B вернул %+v, ожидался только f.go:1:B", hits)
	}

	// Load(A) не видит чанки B
	loaded, err := s.Load(pa)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Chunks) != 1 || loaded.Chunks[0].ID != "f.go:1:A" {
		t.Fatalf("Load(A) = %+v, ожидался только f.go:1:A", loaded.Chunks)
	}
}

// TestPGStoreLegacyMigration — строки легаси-таблицы chunks (с колонкой
// project) переносятся в таблицы проектов при OpenPG, легаси удаляется.
func TestPGStoreLegacyMigration(t *testing.T) {
	dsn := os.Getenv("CODEPILOT_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CODEPILOT_PG_TEST_DSN не задан (нужен Postgres с pgvector)")
	}
	ctx := context.Background()
	project := "test://pgstore-legacy"
	defer func() {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return
		}
		defer conn.Close(ctx)
		_, _ = conn.Exec(ctx, `DROP TABLE IF EXISTS chunks`)
		_, _ = conn.Exec(ctx, `DROP TABLE IF EXISTS `+chunksTable(project))
		_, _ = conn.Exec(ctx, `DELETE FROM meta WHERE project = $1`, project)
	}()

	// поднимаем легаси-схему с одной строкой до подключения PGStore
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("extension: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS meta (
		project TEXT NOT NULL, key TEXT NOT NULL, value TEXT,
		PRIMARY KEY (project, key))`); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TABLE chunks (
		project TEXT NOT NULL, id TEXT NOT NULL, file_path TEXT NOT NULL,
		language TEXT, start_line INTEGER, end_line INTEGER,
		symbol_name TEXT, kind TEXT, signature TEXT, doc TEXT,
		content TEXT, hash TEXT, embedding vector(384),
		PRIMARY KEY (project, id))`); err != nil {
		t.Fatalf("legacy chunks: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO chunks
		(project, id, file_path, language, start_line, end_line, symbol_name, kind, signature, doc, content, hash)
		VALUES ($1, 'f.go:1:L', 'f.go', '', 1, 3, 'L', 'func', '', '', 'legacy', 'h')`, project); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	conn.Close(ctx)

	s, err := OpenPG(dsn)
	if err != nil {
		t.Fatalf("OpenPG: %v", err)
	}
	defer s.Close()

	loaded, err := s.Load(project)
	if err != nil {
		t.Fatalf("Load после миграции: %v", err)
	}
	if len(loaded.Chunks) != 1 || loaded.Chunks[0].ID != "f.go:1:L" {
		t.Fatalf("после миграции: %+v, ожидался f.go:1:L", loaded.Chunks)
	}
	var reg *string
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('public.chunks')::text`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg != nil {
		t.Fatal("легаси-таблица chunks не удалена")
	}
}
