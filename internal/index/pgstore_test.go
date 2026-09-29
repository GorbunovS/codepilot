package index

import (
	"context"
	"os"
	"testing"

	"codepilot/internal/chunk"
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
		_, _ = s.pool.Exec(ctx, `DELETE FROM chunks WHERE project = $1`, project)
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
