// Package web — локальная веб-панель codepilot (SPA + JSON API).
//
// Панель управляет списком проектов, запускает индексацию с живым логом,
// показывает статистику индекса и нагрузку по вызовам MCP-инструментов,
// а также генерирует сниппет MCP-конфига для подключения агента.
// Предполагается использование из десктоп-обёртки (Pake) поверх `codepilot web`.
package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codepilot/internal/embed"
	"codepilot/internal/index"
)

//go:embed static/index.html
var indexHTML []byte

// Options — настройки веб-панели (флаги хранилища совпадают с CLI).
type Options struct {
	Store    string // sqlite|pg
	PGDSN    string // DSN Postgres (режим pg)
	Embed    string // onnx|""
	EmbedDir string // каталог ONNX-модели эмбеддингов
	Laya     string // heuristic|onnx (для MCP-сниппета)
	LayaDir  string // каталог модели Laya (для MCP-сниппета)
	Device   string // cpu|coreml|cuda
	LogPath  string // путь к mcp-calls.jsonl
	BinPath  string // путь к бинарю codepilot (для MCP-сниппета)
}

// project — элемент списка проектов.
type project struct {
	Path    string `json:"path"`
	AddedAt string `json:"added_at"`
}

// indexJob — состояние фоновой индексации одного проекта.
type indexJob struct {
	mu      sync.Mutex
	log     []string // ring-buffer последних строк
	Last    string   // итог последнего прогона (ok/error + сообщение)
	Running bool
}

const maxLogLines = 500

func (j *indexJob) append(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.log) >= maxLogLines {
		j.log = j.log[len(j.log)-maxLogLines+1:]
	}
	j.log = append(j.log, line)
}

func (j *indexJob) snapshot() (bool, []string, string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Running, append([]string(nil), j.log...), j.Last
}

// server — состояние веб-панели.
type server struct {
	opts      Options
	projectsF string
	mu        sync.Mutex // сериализует индексацию и доступ к projects
	projects  []project
	jobs      map[string]*indexJob
	indexing  bool // идёт индексация (глобально, максимум один проект)
}

// Serve поднимает HTTP-сервер панели на addr и блокируется до ошибки.
func Serve(addr string, opts Options) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	confDir := filepath.Join(home, ".codepilot")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return err
	}
	s := &server{
		opts:      opts,
		projectsF: filepath.Join(confDir, "projects.json"),
		jobs:      map[string]*indexJob{},
	}
	if data, err := os.ReadFile(s.projectsF); err == nil {
		_ = json.Unmarshal(data, &s.projects)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("POST /api/projects", s.handleAddProject)
	mux.HandleFunc("DELETE /api/projects", s.handleDelProject)
	mux.HandleFunc("GET /api/ls", s.handleLs)
	mux.HandleFunc("POST /api/index", s.handleIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/mcp-snippet", s.handleSnippet)

	fmt.Fprintf(os.Stderr, "codepilot web: панель на http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *server) handleProjects(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.projects})
}

func (s *server) saveProjectsLocked() {
	data, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.projectsF, data, 0o644)
}

func (s *server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("нужен JSON {\"path\": \"...\"}"))
		return
	}
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("путь недоступен: %w", err))
		return
	}
	if !st.IsDir() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s — не каталог", abs))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.Path == abs {
			writeJSON(w, http.StatusOK, map[string]any{"projects": s.projects})
			return
		}
	}
	s.projects = append(s.projects, project{Path: abs, AddedAt: time.Now().Format(time.RFC3339)})
	s.saveProjectsLocked()
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.projects})
}

func (s *server) handleDelProject(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.projects[:0]
	for _, p := range s.projects {
		if p.Path != path {
			out = append(out, p)
		}
	}
	s.projects = out
	s.saveProjectsLocked()
	writeJSON(w, http.StatusOK, map[string]any{"projects": s.projects})
}

// handleLs — мини-проводник: подкаталоги заданного пути (без dot-каталогов).
func (s *server) handleLs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		path = home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   abs,
		"parent": filepath.Dir(abs),
		"dirs":   dirs,
	})
}

func (s *server) job(path string) *indexJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[path]
	if !ok {
		j = &indexJob{}
		s.jobs[path] = j
	}
	return j
}

// handleIndex запускает индексацию проекта в горутине.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("нужен JSON {\"path\": \"...\"}"))
		return
	}
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Глобальный гейт: index.Logf — хук на весь пакет, поэтому одновременно
	// индексируется максимум один проект.
	s.mu.Lock()
	if s.indexing {
		s.mu.Unlock()
		writeErr(w, http.StatusConflict, errors.New("индексация уже идёт"))
		return
	}
	s.indexing = true
	s.mu.Unlock()
	j := s.job(abs)
	j.mu.Lock()
	j.Running = true
	j.log = nil
	j.Last = ""
	j.mu.Unlock()
	go s.runIndex(abs, j)
	writeJSON(w, http.StatusAccepted, map[string]any{"indexing": true})
}

// runIndex — фоновая индексация; прогресс пишется в ring-buffer задачи
// через глобальный хук index.Logf (восстанавливается по завершении).
func (s *server) runIndex(root string, j *indexJob) {
	defer func() {
		j.mu.Lock()
		j.Running = false
		j.mu.Unlock()
		s.mu.Lock()
		s.indexing = false
		s.mu.Unlock()
	}()
	finish := func(err error) {
		j.mu.Lock()
		if err != nil {
			j.Last = "error: " + err.Error()
		}
		j.mu.Unlock()
	}

	prev := index.Logf
	index.Logf = func(format string, args ...any) { j.append(fmt.Sprintf(format, args...)) }
	defer func() { index.Logf = prev }()

	j.append("индексация " + root)
	ix, st, err := index.Build(root)
	if err != nil {
		finish(err)
		return
	}
	switch s.opts.Store {
	case "sqlite", "":
		if err := ix.Save(); err != nil {
			finish(fmt.Errorf("сохранение индекса: %w", err))
			return
		}
		msg := fmt.Sprintf("ok: файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d)",
			st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed)
		j.append(msg)
		j.mu.Lock()
		j.Last = msg
		j.mu.Unlock()
	case "pg":
		if s.opts.Embed != "onnx" {
			finish(errors.New("--store pg требует --embed onnx"))
			return
		}
		providers, err := embed.ProvidersForDevice(s.opts.Device)
		if err != nil {
			finish(err)
			return
		}
		emb, err := embed.Load(s.opts.EmbedDir, providers...)
		if err != nil {
			finish(err)
			return
		}
		defer emb.Close()
		pg, err := index.OpenPG(s.opts.PGDSN)
		if err != nil {
			finish(err)
			return
		}
		defer pg.Close()
		embedded, err := pg.Save(ix, emb)
		if err != nil {
			finish(fmt.Errorf("сохранение индекса (pg): %w", err))
			return
		}
		msg := fmt.Sprintf("ok (pg): файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d), векторов пересчитано %d",
			st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed, embedded)
		j.append(msg)
		j.mu.Lock()
		j.Last = msg
		j.mu.Unlock()
	default:
		finish(fmt.Errorf("неизвестное хранилище %q (sqlite|pg)", s.opts.Store))
	}
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	j := s.job(path)
	running, log, last := j.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"indexing":    running,
		"log":         log,
		"last_result": last,
	})
}

// mcpCall — запись из mcp-calls.jsonl.
type mcpCall struct {
	TS        string `json:"ts"`
	Tool      string `json:"tool"`
	LatencyMs int64  `json:"latency_ms"`
	RespBytes int    `json:"response_bytes"`
}

// handleStats — статистика индекса проекта + нагрузка по mcp-calls.jsonl.
func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resp := map[string]any{"path": path, "store": s.opts.Store}

	switch s.opts.Store {
	case "pg":
		pg, err := index.OpenPG(s.opts.PGDSN)
		if err != nil {
			resp["index_error"] = err.Error()
			break
		}
		defer pg.Close()
		ix, err := pg.Load(path)
		if err != nil {
			resp["index_error"] = err.Error()
			break
		}
		resp["index"] = map[string]any{"files": len(ix.Manifest), "chunks": len(ix.Chunks)}
	default:
		dbPath := index.IndexPath(path)
		st, err := os.Stat(dbPath)
		if err != nil {
			resp["index_error"] = "индекс не найден: " + dbPath
			break
		}
		ix, err := index.Load(dbPath)
		if err != nil {
			resp["index_error"] = err.Error()
			break
		}
		resp["index"] = map[string]any{
			"files":    len(ix.Manifest),
			"chunks":   len(ix.Chunks),
			"db_bytes": st.Size(),
			"db_mtime": st.ModTime().Format(time.RFC3339),
		}
	}

	resp["mcp"] = mcpStats(s.opts.LogPath)
	writeJSON(w, http.StatusOK, resp)
}

// mcpStats разбирает jsonl-лог вызовов MCP-инструментов.
func mcpStats(logPath string) map[string]any {
	out := map[string]any{
		"total": 0, "per_tool": map[string]int{},
		"avg_latency_ms": 0.0, "avg_bytes": 0.0,
		"recent": []mcpCall{}, "last": []mcpCall{},
	}
	if logPath == "" {
		return out
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		out["log_error"] = err.Error()
		return out
	}
	var calls []mcpCall
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c mcpCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		calls = append(calls, c)
	}
	perTool := map[string]int{}
	var sumLat int64
	var sumBytes int
	for _, c := range calls {
		perTool[c.Tool]++
		sumLat += c.LatencyMs
		sumBytes += c.RespBytes
	}
	out["total"] = len(calls)
	out["per_tool"] = perTool
	if len(calls) > 0 {
		out["avg_latency_ms"] = float64(sumLat) / float64(len(calls))
		out["avg_bytes"] = float64(sumBytes) / float64(len(calls))
	}
	// серия для графиков — последние 200 вызовов
	recent := calls
	if len(recent) > 200 {
		recent = recent[len(recent)-200:]
	}
	if recent == nil {
		recent = []mcpCall{}
	}
	out["recent"] = recent
	// последние 10 вызовов (свежие первыми)
	last := calls
	if len(last) > 10 {
		last = last[len(last)-10:]
	}
	rev := make([]mcpCall, 0, len(last))
	for i := len(last) - 1; i >= 0; i-- {
		rev = append(rev, last[i])
	}
	out["last"] = rev
	return out
}

// handleSnippet — готовый блок mcpServers для конфига агента.
func (s *server) handleSnippet(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	args := []string{"serve", "--project", path}
	if s.opts.Store != "" && s.opts.Store != "sqlite" {
		args = append(args, "--store", s.opts.Store)
		if s.opts.Store == "pg" {
			args = append(args, "--pg-dsn", s.opts.PGDSN)
		}
	}
	if s.opts.Embed != "" {
		args = append(args, "--embed", s.opts.Embed, "--embed-dir", s.opts.EmbedDir)
	}
	if s.opts.Laya != "" && s.opts.Laya != "heuristic" {
		args = append(args, "--laya", s.opts.Laya)
		if s.opts.LayaDir != "" {
			args = append(args, "--laya-dir", s.opts.LayaDir)
		}
	}
	if s.opts.Device != "" && s.opts.Device != "cpu" {
		args = append(args, "--device", s.opts.Device)
	}
	snippet := map[string]any{
		"mcpServers": map[string]any{
			"codepilot": map[string]any{
				"command": s.opts.BinPath,
				"args":    args,
			},
		},
	}
	data, _ := json.MarshalIndent(snippet, "", "  ")
	writeJSON(w, http.StatusOK, map[string]string{"json": string(data)})
}
