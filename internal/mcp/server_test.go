package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codepilot/internal/chunk"
	"codepilot/internal/index"
	"codepilot/internal/laya"
)

// slowScorer — Laya-заглушка, которая «думает» дольше любого бюджета:
// имитация ONNX-реранка на CPU (десятки секунд на пул).
type slowScorer struct{}

func (slowScorer) Score(query string, c chunk.Chunk) float64 {
	time.Sleep(500 * time.Millisecond)
	return 0
}

func (slowScorer) Noul(query string, top []index.SearchHit) float64 { return 0.9 }

func testIndex(t *testing.T) *index.Index {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\n\n// A первая.\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ix, _, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// testServer — сервер с одним статическим индексом (любой project → ix).
func testServer(ix *index.Index, scorer laya.Scorer, timeout time.Duration) *Server {
	return &Server{
		resolve:       func(string) (*index.Index, error) { return ix, nil },
		scorer:        scorer,
		searchTimeout: timeout,
	}
}

// TestSearchCodeRerankTimeout — реранк, не уложившийся в бюджет, не вешает
// MCP-вызов: отдаём hybrid-порядок с degraded=rerank_timeout.
func TestSearchCodeRerankTimeout(t *testing.T) {
	s := testServer(testIndex(t), slowScorer{}, 100*time.Millisecond)
	start := time.Now()
	payload, _, err := s.searchCode(json.RawMessage(`{"query":"A","top_k":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("деградированный ответ занял %v — бюджет не работает", elapsed)
	}
	m := payload.(map[string]interface{})
	if m["degraded"] != "rerank_timeout" {
		t.Fatalf("degraded = %v, ожидался rerank_timeout", m["degraded"])
	}
	if len(m["results"].([]index.SearchHit)) == 0 {
		t.Fatal("пустая выдача при деградации")
	}
}

// TestSearchCodeRerankBusy — второй поиск во время счёта первого сразу
// получает hybrid (degraded=rerank_busy), а не встаёт в очередь на ORT.
func TestSearchCodeRerankBusy(t *testing.T) {
	s := testServer(testIndex(t), slowScorer{}, 30*time.Second)
	s.rerankBusy.Store(true) // будто предыдущий реранк ещё считается
	payload, _, err := s.searchCode(json.RawMessage(`{"query":"A","top_k":3}`))
	if err != nil {
		t.Fatal(err)
	}
	m := payload.(map[string]interface{})
	if m["degraded"] != "rerank_busy" {
		t.Fatalf("degraded = %v, ожидался rerank_busy", m["degraded"])
	}
}

// TestSearchTimeoutEnv — бюджет переопределяется переменной окружения.
func TestSearchTimeoutEnv(t *testing.T) {
	t.Setenv("CODEPILOT_MCP_SEARCH_TIMEOUT", "7")
	if got := searchTimeoutDefault(); got != 7*time.Second {
		t.Fatalf("searchTimeout = %v, ожидалось 7s", got)
	}
}

// testIndexWith — индекс temp-проекта с одним файлом (символ символьно уникален).
func testIndexWith(t *testing.T, file, content string) *index.Index {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	ix, _, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// TestMultiProjectRouting — аргумент project маршрутизирует запрос в индекс
// нужного проекта: символ проекта b не виден из проекта a и наоборот.
func TestMultiProjectRouting(t *testing.T) {
	ixA := testIndexWith(t, "a.go", "package a\n\n// OnlyA здесь.\nfunc OnlyA() {}\n")
	ixB := testIndexWith(t, "b.go", "package b\n\n// OnlyB там.\nfunc OnlyB() {}\n")
	byName := map[string]*index.Index{"alpha": ixA, "beta": ixB}
	s := &Server{
		resolve: func(name string) (*index.Index, error) {
			if name == "" {
				name = "alpha"
			}
			ix, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("проект %q не найден, доступные: alpha, beta", name)
			}
			return ix, nil
		},
		scorer:        laya.Heuristic{},
		searchTimeout: time.Second,
	}

	payload, err := s.getSymbol(json.RawMessage(`{"name":"OnlyB","project":"beta"}`))
	if err != nil {
		t.Fatalf("get_symbol beta: %v", err)
	}
	data, _ := json.Marshal(payload)
	if !strings.Contains(string(data), "b.go") {
		t.Fatalf("ожидался b.go в выдаче: %s", data)
	}

	if _, err := s.getSymbol(json.RawMessage(`{"name":"OnlyB","project":"alpha"}`)); err == nil {
		t.Fatal("OnlyB не должен находиться в проекте alpha")
	}

	// Пустой project → дефолтный (alpha).
	payload, err = s.getSymbol(json.RawMessage(`{"name":"OnlyA"}`))
	if err != nil {
		t.Fatalf("get_symbol default: %v", err)
	}
	data, _ = json.Marshal(payload)
	if !strings.Contains(string(data), "a.go") {
		t.Fatalf("дефолтный проект не alpha: %s", data)
	}

	// Неизвестный проект — ошибка с именами доступных.
	if _, err := s.getSymbol(json.RawMessage(`{"name":"OnlyA","project":"gamma"}`)); err == nil ||
		!strings.Contains(err.Error(), "alpha") {
		t.Fatalf("ожидалась ошибка со списком проектов, получено: %v", err)
	}
}

// TestReindexTool — триггер переиндексации: колбэк получает имя проекта,
// без подключённого колбэка — понятная ошибка.
func TestReindexTool(t *testing.T) {
	s := &Server{scorer: laya.Heuristic{}, searchTimeout: time.Second}

	if _, err := s.reindexProject(json.RawMessage(`{"project":"frontend"}`)); err == nil {
		t.Fatal("без SetReindex должна быть ошибка")
	}

	var got string
	s.SetReindex(func(name string) error { got = name; return nil })
	payload, err := s.reindexProject(json.RawMessage(`{"project":"frontend"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "frontend" {
		t.Fatalf("колбэк получил %q, ожидалось frontend", got)
	}
	m := payload.(map[string]interface{})
	if m["started"] != true {
		t.Fatalf("started = %v", m["started"])
	}
}
func TestListProjects(t *testing.T) {
	s := &Server{
		resolve: func(string) (*index.Index, error) { return nil, fmt.Errorf("no") },
		projects: func() []Project {
			return []Project{{Name: "backend", Path: "/srv/back"}, {Name: "frontend", Path: "/srv/front"}}
		},
		def:           "backend",
		scorer:        laya.Heuristic{},
		searchTimeout: time.Second,
	}
	payload, err := s.listProjects()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(payload)
	text := string(data)
	for _, want := range []string{`"backend"`, `"frontend"`, `"is_default":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("в list_projects нет %s: %s", want, text)
		}
	}
}
