package embed

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"codepilot/internal/ortlib"
)

func TestMeanPoolNorm(t *testing.T) {
	// 2 токена, Dim=384: первый — единицы, второй замаскирован.
	hidden := make([]float32, 2*Dim)
	for i := 0; i < Dim; i++ {
		hidden[i] = 1
	}
	mask := []int64{1, 0}
	vec := meanPoolNorm(hidden, mask)
	if len(vec) != Dim {
		t.Fatalf("dim = %d, ожидалось %d", len(vec), Dim)
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-5 {
		t.Fatalf("норма = %v, ожидалось ~1", math.Sqrt(norm))
	}
	// каждый компонент = 1/sqrt(Dim)
	want := 1 / math.Sqrt(Dim)
	if math.Abs(float64(vec[0])-want) > 1e-5 {
		t.Fatalf("vec[0] = %v, ожидалось ~%v", vec[0], want)
	}
}

func testEmbedder(t *testing.T) *Embedder {
	t.Helper()
	dir := filepath.Join("..", "..", "models", "e5-small")
	if _, err := os.Stat(filepath.Join(dir, "model.onnx")); err != nil {
		t.Skip("модель models/e5-small не скачана (scripts/download_models.sh)")
	}
	// тест бежит из internal/embed — подсказываем путь к либе из корня репо
	if lib, err := filepath.Abs(filepath.Join("..", "..", "bin", "libonnxruntime.dylib")); err == nil {
		if _, err := os.Stat(lib); err == nil {
			t.Setenv(ortlib.EnvVar, lib)
		}
	}
	if ortlib.Find(dir) == "" {
		t.Skip("нативная onnxruntime не найдена (bin/ или " + ortlib.EnvVar + ")")
	}
	e, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(e.Close)
	return e
}

func cos(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func TestEmbedSemantic(t *testing.T) {
	e := testEmbedder(t)
	q, err := e.EmbedQueries([]string{"где проверяется токен доступа"})
	if err != nil {
		t.Fatalf("EmbedQueries: %v", err)
	}
	if len(q[0]) != Dim {
		t.Fatalf("dim = %d, ожидалось %d", len(q[0]), Dim)
	}
	p, err := e.EmbedPassages([]string{
		"func ValidateToken(token string) (string, error) — проверяет токен и возвращает userID",
		"рецепт борща: свёкла, капуста, сметана",
	})
	if err != nil {
		t.Fatalf("EmbedPassages: %v", err)
	}
	near, far := cos(q[0], p[0]), cos(q[0], p[1])
	t.Logf("cos(релевантный)=%.3f cos(нерелевантный)=%.3f", near, far)
	if near <= far {
		t.Fatalf("семантика сломана: релевантный %.3f <= нерелевантный %.3f", near, far)
	}
}
