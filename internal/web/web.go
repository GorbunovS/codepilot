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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"codepilot/internal/chunk"
	"codepilot/internal/config"
	"codepilot/internal/embed"
	"codepilot/internal/index"
	"codepilot/internal/laya"
	"codepilot/internal/mcp"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/skill/SKILL.md
var skillMD []byte

// Options — настройки веб-панели (флаги хранилища совпадают с CLI).
type Options struct {
	Store       string // sqlite|pg
	PGDSN       string // DSN Postgres (режим pg)
	Embed       string // onnx|""
	EmbedDir    string // каталог ONNX-модели эмбеддингов
	EmbedServer string // URL MLX-сайдкара (заменяет локальный ONNX)
	Laya        string // heuristic|onnx (для поиска и MCP-сниппета)
	LayaDir     string // каталог модели Laya
	Device      string // cpu|coreml|cuda|directml
	MaxThreads  int    // лимит потоков ONNX-инференса (0 — все ядра)
	LogPath     string // путь к mcp-calls.jsonl
	BinPath     string // путь к бинарю codepilot (для MCP-сниппета)
}

// project — элемент списка проектов. Name — короткий идентификатор для
// агента в MCP-инструментах (аргумент project); по умолчанию basename пути.
type project struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	AddedAt string `json:"added_at"`
}

// indexJob — состояние фоновой индексации одного проекта.
type indexJob struct {
	mu      sync.Mutex
	log     []string // ring-buffer последних строк
	Last    string   // итог последнего прогона (ok/error/aborted + сообщение)
	Running bool
	Cancel  bool // запрошена отмена текущего прогона
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
	addr      string // адрес HTTP-сервера (для URL в MCP-сниппете)
	projectsF string
	jobsF     string
	mu        sync.Mutex // сериализует индексацию и доступ к projects
	projects  []project
	jobs      map[string]*indexJob
	indexing  bool // идёт индексация (глобально, максимум один проект)
	mcp       *mcp.Server // MCP-демон: HTTP-транспорт на /mcp

	idxMu     sync.Mutex
	idxCache  map[string]*cachedIndex // открытые индексы проектов (для stats/search)
	searchMu  sync.Mutex             // сериализует поиск (ONNX-сессии не потокобезопасны)
	devOnce   sync.Once
	devices   []deviceInfo
	scorerMu  sync.Mutex
	scorer    laya.Scorer
	scorerSet bool
	embMu     sync.Mutex
	emb       embed.TextEmbedder // общий e5-эмбеддер панели (ленивый синглтон)
	embSet    bool
}

// cachedIndex — открытый индекс проекта с меткой файла для инвалидации.
type cachedIndex struct {
	ix      *index.Index
	cleanup func() // pg: закрытие соединения/эмбеддера
	mtime   time.Time
	size    int64
}

// deviceInfo — пункт комбобокса устройств инференса.
type deviceInfo struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// persistedJob — дисковый слепок состояния индексации проекта.
type persistedJob struct {
	Path string   `json:"path"`
	Last string   `json:"last"`
	Log  []string `json:"log"`
}

func (j *indexJob) persist() persistedJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return persistedJob{Last: j.Last, Log: append([]string(nil), j.log...)}
}

func (s *server) loadJobs() {
	data, err := os.ReadFile(s.jobsF)
	if err != nil {
		return
	}
	var list []persistedJob
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for _, p := range list {
		if p.Path == "" {
			continue
		}
		s.jobs[p.Path] = &indexJob{Last: p.Last, log: append([]string(nil), p.Log...)}
	}
}

func (s *server) saveJobs() {
	s.mu.Lock()
	jobs := make([]persistedJob, 0, len(s.jobs))
	for path, j := range s.jobs {
		pj := j.persist()
		if pj.Last == "" && len(pj.Log) == 0 {
			continue
		}
		pj.Path = path
		jobs = append(jobs, pj)
	}
	s.mu.Unlock()
	if len(jobs) == 0 {
		_ = os.Remove(s.jobsF)
		return
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Path < jobs[j].Path })
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return
	}
	tmp := s.jobsF + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.jobsF)
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
		addr:      addr,
		projectsF: filepath.Join(confDir, "projects.json"),
		jobsF:     filepath.Join(confDir, "jobs.json"),
		jobs:      map[string]*indexJob{},
		idxCache:  map[string]*cachedIndex{},
	}
	if data, err := os.ReadFile(s.projectsF); err == nil {
		_ = json.Unmarshal(data, &s.projects)
	}
	// Имя — идентификатор проекта для агента (аргумент project в MCP):
	// старым записям без имени проставляем basename пути.
	for i := range s.projects {
		if s.projects[i].Name == "" {
			s.projects[i].Name = config.DefaultName(s.projects[i].Path)
		}
	}
	s.loadJobs()

	// MCP-демон: тот же код, что у stdio-serve, но по HTTP и на кэше
	// индексов панели. Агенты подключаются по URL, процесс один.
	s.mcp = mcp.NewServer(s.resolveMCP, s.mcpProjects, "", lazyScorer{s})
	if err := s.mcp.SetLog(opts.LogPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: лог MCP-вызовов %s не открыт: %v\n", opts.LogPath, err)
	}
	// Триггер переиндексации от агента: тот же фоновый прогон, что по кнопке
	// в панели (глобальный гейт «одна индексация за раз» сохраняется).
	s.mcp.SetReindex(func(nameOrPath string) error {
		abs, err := s.findProjectPath(nameOrPath)
		if err != nil {
			return err
		}
		if !s.startIndexing(abs, "", 0) {
			return fmt.Errorf("индексация уже идёт, повторите позже")
		}
		return nil
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store") // SPA встраивается в бинарь, кэш браузера не нужен
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/projects", s.handleProjects)
	mux.HandleFunc("POST /api/projects", s.handleAddProject)
	mux.HandleFunc("DELETE /api/projects", s.handleDelProject)
	mux.HandleFunc("GET /api/ls", s.handleLs)
	mux.HandleFunc("POST /api/index", s.handleIndex)
	mux.HandleFunc("POST /api/index/cancel", s.handleCancelIndex)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/mcp-snippet", s.handleSnippet)
	mux.HandleFunc("POST /api/mcp/install", s.handleMCPInstall)
	mux.HandleFunc("GET /api/devices", s.handleDevices)
	mux.HandleFunc("POST /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/skill", s.handleSkill)
	mux.HandleFunc("GET /api/skill/status", s.handleSkillStatus)
	mux.HandleFunc("POST /api/skill/install", s.handleSkillInstall)

	mux.HandleFunc("POST /mcp", s.handleMCP)
	mux.HandleFunc("GET /mcp", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusMethodNotAllowed, errors.New("MCP: используйте POST (streamable http)"))
	})
	fmt.Fprintf(os.Stderr, "codepilot web: панель на http://%s (MCP: http://%s/mcp)\n", addr, addr)
	return http.ListenAndServe(addr, mux)
}

// handleMCP — MCP по HTTP (упрощённый streamable-транспорт): один JSON-RPC
// запрос на POST, ответ — JSON; уведомления (notifications/*) → 202.
func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resp := s.mcp.HandleRaw(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// findProjectPath — имя/путь проекта из аргумента project → абсолютный путь
// (пустой — default_project из конфига, иначе первый проект реестра).
func (s *server) findProjectPath(nameOrPath string) (string, error) {
	list := s.projectsList()
	cp := make([]config.Project, 0, len(list))
	for _, p := range list {
		cp = append(cp, config.Project{Name: p.Name, Path: p.Path})
	}
	def := ""
	if cfg, err := config.Load(); err == nil {
		def = cfg.Default
	}
	p, ok := config.Find(cp, nameOrPath, def)
	if !ok {
		names := make([]string, 0, len(list))
		for _, pr := range list {
			names = append(names, pr.Name)
		}
		return "", fmt.Errorf("проект %q не найден; доступные: %s", nameOrPath, strings.Join(names, ", "))
	}
	return filepath.Abs(p.Path)
}

// resolveMCP — индекс проекта по имени/пути из аргумента project MCP-вызова.
// Индексы — общий кэш панели (getIndex): статистика, ручной поиск и MCP
// смотрят в одни и те же открытые индексы.
func (s *server) resolveMCP(nameOrPath string) (*index.Index, error) {
	abs, err := s.findProjectPath(nameOrPath)
	if err != nil {
		return nil, err
	}
	return s.getIndex(abs)
}

// mcpProjects — реестр проектов для инструмента list_projects.
func (s *server) mcpProjects() []mcp.Project {
	list := s.projectsList()
	out := make([]mcp.Project, 0, len(list))
	for _, p := range list {
		out = append(out, mcp.Project{Name: p.Name, Path: p.Path})
	}
	return out
}

// lazyScorer — отложенная инициализация Laya-синглтона панели: модель
// грузится при первом поиске, а не при старте web.
type lazyScorer struct{ s *server }

func (l lazyScorer) Score(query string, c chunk.Chunk) float64 {
	return l.s.getScorer().Score(query, c)
}

func (l lazyScorer) Noul(query string, top []index.SearchHit) float64 {
	return l.s.getScorer().Noul(query, top)
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
	added := true
	for _, p := range s.projects {
		if p.Path == abs {
			added = false
			break
		}
	}
	if added {
		s.projects = append(s.projects, project{Name: config.DefaultName(abs), Path: abs, AddedAt: time.Now().Format(time.RFC3339)})
		s.saveProjectsLocked()
	}
	s.mu.Unlock()

	// Автоиндексация: новый проект без index.db индексируем сразу, чтобы
	// пользователю не приходилось запускать команду руками. Глобальный гейт
	// (одна индексация за раз) сохраняется — при занятости запускаем вручную.
	indexing := false
	if added && !s.hasIndexFile(abs) {
		indexing = s.startIndexing(abs, "", 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"projects": s.projectsList(),
		"added":    added,
		"indexing": indexing,
		"skill":    s.skillStatus(abs),
	})
}

// projectsList — снапшот списка проектов под мьютексом.
func (s *server) projectsList() []project {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]project(nil), s.projects...)
}

// hasIndexFile — есть ли файл индекса (sqlite-режим; для pg всегда false —
// дешёвой проверки нет, автоиндексация там инкрементальна и безвредна).
func (s *server) hasIndexFile(abs string) bool {
	if s.opts.Store == "pg" {
		return false
	}
	_, err := os.Stat(index.IndexPath(abs))
	return err == nil
}

func (s *server) handleDelProject(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	s.mu.Lock()
	out := s.projects[:0]
	for _, p := range s.projects {
		if p.Path != path {
			out = append(out, p)
		}
	}
	s.projects = out
	s.saveProjectsLocked()
	s.mu.Unlock()
	s.invalidateIndex(path)
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
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
// Необязательное поле device (cpu|coreml|cuda|directml) запоминается в настройках
// панели и используется для этого прогона (и попадёт в MCP-сниппет).
// low_cpu: true — щадящий режим: инференс ограничен половиной ядер,
// машина остаётся отзывчивой ценой более долгой индексации.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Device string `json:"device"`
		LowCPU bool   `json:"low_cpu"`
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
	if _, err := embed.ProvidersForDevice(req.Device); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	threads := 0
	if req.LowCPU {
		threads = max(1, runtime.NumCPU()/2)
	}
	if !s.startIndexing(abs, req.Device, threads) {
		writeErr(w, http.StatusConflict, errors.New("индексация уже идёт"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"indexing": true})
}

// startIndexing запускает фоновую индексацию проекта, если сейчас ничего не
// индексируется (index.Logf/CheckAbort — глобальные хуки пакета, поэтому
// одновременно возможен только один прогон). device != "" запоминается в
// настройках панели. threads == 0 — дефолт панели (opts.MaxThreads).
// Возвращает false, если индексация уже идёт.
func (s *server) startIndexing(abs, device string, threads int) bool {
	s.mu.Lock()
	if s.indexing {
		s.mu.Unlock()
		return false
	}
	s.indexing = true
	if device != "" {
		s.opts.Device = device
	}
	dev := s.opts.Device
	if threads == 0 {
		threads = s.opts.MaxThreads
	}
	s.mu.Unlock()
	j := s.job(abs)
	j.mu.Lock()
	j.Running = true
	j.Cancel = false
	j.log = nil
	j.Last = ""
	j.mu.Unlock()
	go s.runIndex(abs, dev, threads, j)
	return true
}

// handleCancelIndex запрашивает отмену текущей индексации проекта.
func (s *server) handleCancelIndex(w http.ResponseWriter, r *http.Request) {
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
	j := s.job(abs)
	j.mu.Lock()
	if !j.Running {
		j.mu.Unlock()
		writeErr(w, http.StatusConflict, errors.New("индексация этого проекта не идёт"))
		return
	}
	j.Cancel = true
	j.mu.Unlock()
	j.append("отмена запрошена пользователем…")
	writeJSON(w, http.StatusAccepted, map[string]any{"cancelling": true})
}

// runIndex — фоновая индексация; прогресс пишется в ring-buffer задачи
// через глобальный хук index.Logf, отмена — через index.CheckAbort
// (оба хука восстанавливаются по завершении). threads — лимит потоков
// ONNX-инференса (0 — все ядра).
func (s *server) runIndex(root, device string, threads int, j *indexJob) {
	defer func() {
		j.mu.Lock()
		j.Running = false
		j.mu.Unlock()
		s.mu.Lock()
		s.indexing = false
		s.mu.Unlock()
		s.invalidateIndex(root)
		s.saveJobs()
	}()
	finish := func(err error) {
		j.mu.Lock()
		if err != nil {
			if errors.Is(err, index.ErrAborted) {
				j.Last = "aborted: отменено пользователем"
			} else {
				j.Last = "error: " + err.Error()
			}
		}
		j.mu.Unlock()
	}

	prevLog := index.Logf
	index.Logf = func(format string, args ...any) { j.append(fmt.Sprintf(format, args...)) }
	prevAbort := index.CheckAbort
	index.CheckAbort = func() bool {
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.Cancel
	}
	defer func() { index.Logf = prevLog; index.CheckAbort = prevAbort }()

	j.append("индексация " + root)
	// Снапшот настроек хранилища: Device может меняться параллельно
	// через POST /api/index, остальные поля неизменны после старта.
	s.mu.Lock()
	store, embedKind, embedDir, embedServer, dsn := s.opts.Store, s.opts.Embed, s.opts.EmbedDir, s.opts.EmbedServer, s.opts.PGDSN
	s.mu.Unlock()
	// pg-режим: предыдущее состояние берём из Postgres, иначе каждый прогон
	// был бы полным речанкингом — локального index.db у pg-проекта нет.
	var pg *index.PGStore
	if store == "pg" {
		var err error
		pg, err = index.OpenPG(dsn)
		if err != nil {
			finish(err)
			return
		}
		defer pg.Close()
	}
	var prev *index.Index
	if pg != nil {
		abs, _ := filepath.Abs(root)
		prev = pg.Prev(abs)
	}
	ix, st, err := index.BuildPrev(root, prev)
	if err != nil {
		j.append("прервано: " + err.Error())
		finish(err)
		return
	}
	switch store {
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
		var emb index.PassageEmbedder
		if embedServer != "" {
			r := embed.NewRemoteEmbedder(embedServer)
			if err := r.CheckDim(); err != nil {
				finish(err)
				return
			}
			emb = r
			defer r.Close()
		} else {
			if embedKind != "onnx" {
				finish(errors.New("--store pg требует --embed onnx или --embed-server URL"))
				return
			}
			providers, err := embed.ProvidersForDevice(device)
			if err != nil {
				finish(err)
				return
			}
			e, err := embed.Load(embedDir, threads, providers...)
			if err != nil {
				finish(err)
				return
			}
			defer e.Close()
			emb = e
		}
		embedded, err := pg.Save(ix, emb)
		if err != nil {
			finish(fmt.Errorf("сохранение индекса (pg): %w", err))
			return
		}
		vecNote := fmt.Sprintf("векторов пересчитано %d", embedded)
		if embedded == 0 {
			// ноль пересчитанных при живом индексе — «всё уже посчитано»,
			// а не «эмбеддер не запустился»; иначе читается как сбой
			vecNote = "векторы актуальны (пересчёт не требуется)"
		}
		msg := fmt.Sprintf("ok (pg): файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d), %s",
			st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed, vecNote)
		j.append(msg)
		j.mu.Lock()
		j.Last = msg
		j.mu.Unlock()
	default:
		finish(fmt.Errorf("неизвестное хранилище %q (sqlite|pg)", store))
	}
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	j := s.job(path)
	running, log, last := j.snapshot()
	s.mu.Lock()
	device := s.opts.Device
	s.mu.Unlock()
	if device == "" {
		device = "cpu"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"indexing":    running,
		"log":         log,
		"last_result": last,
		"device":      device,
	})
}

// getIndex возвращает открытый индекс проекта из кэша; промах или изменение
// файла index.db (mtime/size) — перезагрузка. BM25/TF-IDF перестраиваются
// при каждом Load, поэтому без кэша polling статистики грузил бы CPU.
func (s *server) getIndex(path string) (*index.Index, error) {
	s.idxMu.Lock()
	defer s.idxMu.Unlock()
	if s.opts.Store == "pg" {
		if c, ok := s.idxCache[path]; ok {
			return c.ix, nil
		}
		ix, cleanup, err := s.loadPGIndex(path)
		if err != nil {
			return nil, err
		}
		s.idxCache[path] = &cachedIndex{ix: ix, cleanup: cleanup}
		return ix, nil
	}
	dbPath := index.IndexPath(path)
	st, err := os.Stat(dbPath)
	if err != nil {
		return nil, fmt.Errorf("индекс не найден: %s", dbPath)
	}
	if c, ok := s.idxCache[path]; ok && c.mtime.Equal(st.ModTime()) && c.size == st.Size() {
		return c.ix, nil
	}
	s.dropIndexLocked(path)
	ix, err := index.Load(dbPath)
	if err != nil {
		return nil, err
	}
	s.idxCache[path] = &cachedIndex{ix: ix, mtime: st.ModTime(), size: st.Size()}
	return ix, nil
}

// getEmbedder — общий e5-эмбеддер панели (ленивый синглтон): одна
// ONNX-сессия на все pg-проекты, иначе каждый проект держал бы свою копию
// модели (~300+ МБ). nil без ошибки — эмбеддинги не настроены.
func (s *server) getEmbedder() (embed.TextEmbedder, error) {
	s.embMu.Lock()
	defer s.embMu.Unlock()
	if s.embSet {
		return s.emb, nil
	}
	s.embSet = true
	switch {
	case s.opts.EmbedServer != "":
		s.emb = embed.NewRemoteEmbedder(s.opts.EmbedServer)
	case s.opts.Embed == "onnx":
		providers, err := embed.ProvidersForDevice(s.opts.Device)
		if err != nil {
			return nil, err
		}
		e, err := embed.Load(s.opts.EmbedDir, s.opts.MaxThreads, providers...)
		if err != nil {
			return nil, err
		}
		s.emb = e
	}
	return s.emb, nil
}

// loadPGIndex открывает pg-индекс проекта и подключает общий эмбеддер
// панели (MLX-сайдкар или локальный ONNX) — аналог loadIndexStore из CLI.
func (s *server) loadPGIndex(path string) (*index.Index, func(), error) {
	pg, err := index.OpenPG(s.opts.PGDSN)
	if err != nil {
		return nil, nil, err
	}
	noop := func() {}
	ix, err := pg.Load(path)
	if err != nil {
		pg.Close()
		return nil, nil, err
	}
	ix.PG = pg
	cleanup := func() { pg.Close() }
	emb, err := s.getEmbedder()
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	ix.Emb = emb // общий синглтон: не закрывается в cleanup
	return ix, cleanup, nil
}

// dropIndexLocked удаляет запись кэша, освобождая ресурсы. Без блокировки idxMu.
func (s *server) dropIndexLocked(path string) {
	if c, ok := s.idxCache[path]; ok {
		if c.cleanup != nil {
			c.cleanup()
		}
		delete(s.idxCache, path)
	}
}

// invalidateIndex сбрасывает кэш индекса проекта (после индексации/удаления).
func (s *server) invalidateIndex(path string) {
	s.idxMu.Lock()
	defer s.idxMu.Unlock()
	s.dropIndexLocked(path)
}

// getScorer — ленивый синглтон слоя решений (ONNX Laya грузится секунды,
// поэтому создаём один раз на жизнь сервера; fallback на эвристику внутри
// laya.Resolve). Устройство и потоки фиксируются снапшотом opts на момент
// первого обращения.
func (s *server) getScorer() laya.Scorer {
	s.scorerMu.Lock()
	defer s.scorerMu.Unlock()
	if s.scorerSet {
		return s.scorer
	}
	providers, err := embed.ProvidersForDevice(s.opts.Device)
	if err != nil {
		providers = nil
	}
	s.scorer = laya.Resolve(s.opts.Laya, s.opts.LayaDir, s.opts.MaxThreads, providers...)
	s.scorerSet = true
	return s.scorer
}

// layaONNX — запущена ли панель с ONNX-моделью Laya (модель реально на диске).
func (s *server) layaONNX() bool {
	if s.opts.Laya != "onnx" {
		return false
	}
	dir := s.opts.LayaDir
	if dir == "" {
		dir = "models/laya-multilingual"
	}
	_, err := os.Stat(filepath.Join(dir, "laya.onnx"))
	return err == nil
}

// mcpCall — запись из mcp-calls.jsonl.
type mcpCall struct {
	TS        string `json:"ts"`
	Tool      string `json:"tool"`
	LatencyMs int64  `json:"latency_ms"`
	RespBytes int    `json:"response_bytes"`
}

// handleStats — статистика индекса проекта (из кэша) + нагрузка по mcp-calls.jsonl.
func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	s.mu.Lock()
	store, logPath := s.opts.Store, s.opts.LogPath
	s.mu.Unlock()
	resp := map[string]any{"path": path, "store": store}

	ix, err := s.getIndex(path)
	if err != nil {
		resp["index_error"] = err.Error()
	} else {
		info := map[string]any{"files": len(ix.Manifest), "chunks": len(ix.Chunks)}
		if store != "pg" {
			if st, err := os.Stat(index.IndexPath(path)); err == nil {
				info["db_bytes"] = st.Size()
				info["db_mtime"] = st.ModTime().Format(time.RFC3339)
			}
		}
		info["languages"] = topCounts(ix, func(c chunkRef) string { return c.lang }, 8)
		info["kinds"] = topCounts(ix, func(c chunkRef) string { return c.kind }, 8)
		resp["index"] = info
	}

	resp["mcp"] = mcpStats(logPath)
	writeJSON(w, http.StatusOK, resp)
}

// chunkRef — лёгкая проекция чанка для подсчёта статистики.
type chunkRef struct {
	lang string
	kind string
}

// topCounts — топ-n значений поля по числу чанков (отсортировано по убыванию).
func topCounts(ix *index.Index, field func(chunkRef) string, n int) []map[string]any {
	counts := map[string]int{}
	for _, c := range ix.Chunks {
		v := field(chunkRef{lang: c.Language, kind: c.Kind})
		if v == "" {
			v = "other"
		}
		counts[v]++
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].v > pairs[b].v })
	out := make([]map[string]any, 0, n)
	rest := 0
	for i, p := range pairs {
		if i < n {
			out = append(out, map[string]any{"name": p.k, "count": p.v})
		} else {
			rest += p.v
		}
	}
	if rest > 0 {
		out = append(out, map[string]any{"name": "прочее", "count": rest})
	}
	return out
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

// mcpConfig — блок конфигурации MCP-сервера codepilot для проекта
// (формат mcpServers, общий для Kimi Code `.kimi-code/mcp.json` и клиентов
// со схожей схемой). Транспорт — HTTP: панель и есть MCP-демон, все агенты
// ходят в один процесс по URL, а не плодят по codepilot.exe на сессию.
// Настройки демона — в ~/.codepilot/config.json (их пишет панель), проект
// агент выбирает аргументом project инструментов (имя из list_projects).
func (s *server) mcpConfig(path string) map[string]any {
	s.mu.Lock()
	addr := s.addr
	s.mu.Unlock()
	_ = path // мультипроектный сервер: привязка к проекту ушла в аргументы инструментов
	return map[string]any{
		"mcpServers": map[string]any{
			"codepilot": map[string]any{
				"type": "http",
				"url":  "http://" + addr + "/mcp",
			},
		},
	}
}

// handleSnippet — готовый блок mcpServers для конфига агента.
func (s *server) handleSnippet(w http.ResponseWriter, r *http.Request) {
	data, _ := json.MarshalIndent(s.mcpConfig(r.URL.Query().Get("path")), "", "  ")
	writeJSON(w, http.StatusOK, map[string]string{"json": string(data)})
}

// handleMCPInstall записывает/мержит codepilot в <project>/.kimi-code/mcp.json —
// проектный конфиг MCP для Kimi Code. Чужие серверы в файле сохраняются.
func (s *server) handleMCPInstall(w http.ResponseWriter, r *http.Request) {
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
	cfgPath := filepath.Join(abs, ".kimi-code", "mcp.json")
	conf := map[string]any{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, &conf); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s не парсится как JSON: %w", cfgPath, err))
			return
		}
	}
	servers, _ := conf["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	fresh, _ := s.mcpConfig(abs)["mcpServers"].(map[string]any)
	servers["codepilot"] = fresh["codepilot"]
	conf["mcpServers"] = servers
	data, _ := json.MarshalIndent(conf, "", "  ")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(cfgPath, append(data, '\n'), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": cfgPath})
}

// handleDevices — список доступных устройств инференса. cpu есть всегда;
// coreml — только на macOS; cuda/directml — если нативная onnxruntime найдена
// и её сборка включает соответствующий провайдер. Детекция — один раз за
// жизнь сервера (поднимаем и сразу закрываем runtime).
func (s *server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	s.devOnce.Do(func() {
		devs := []deviceInfo{{Value: "cpu", Label: "cpu"}}
		has := map[string]bool{}
		for _, p := range embed.AvailableProviders() {
			has[p] = true
		}
		switch runtime.GOOS {
		case "darwin":
			if has["CoreMLExecutionProvider"] {
				devs = append(devs, deviceInfo{Value: "coreml", Label: "coreml (ANE/GPU)"})
			}
		case "windows":
			if has["DmlExecutionProvider"] {
				devs = append(devs, deviceInfo{Value: "directml", Label: "directml (GPU)"})
			}
			if has["CUDAExecutionProvider"] {
				devs = append(devs, deviceInfo{Value: "cuda", Label: "cuda (GPU)"})
			}
		default:
			if has["CUDAExecutionProvider"] {
				devs = append(devs, deviceInfo{Value: "cuda", Label: "cuda (GPU)"})
			}
		}
		s.devices = devs
	})
	device := s.opts.Device
	if device == "" {
		device = "cpu"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":  s.devices,
		"current":  device,
		"laya_onnx": s.layaONNX(),
	})
}

// searchModes — допустимые режимы ручного поиска.
var searchModes = map[string]bool{
	"fts": true, "vec": true, "hybrid": true,
	"hybrid+rerank": true, "hybrid+blend": true,
}

// handleSearch — ручной поиск по индексу проекта из панели:
// {path, query, mode, top} → хиты + noul/need_more (тот же порог 0.5,
// что в MCP-сервере и bench).
func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string `json:"path"`
		Query string `json:"query"`
		Mode  string `json:"mode"`
		Top   int    `json:"top"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" || strings.TrimSpace(req.Query) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("нужен JSON {\"path\": \"...\", \"query\": \"...\"}"))
		return
	}
	if req.Mode == "" {
		req.Mode = "hybrid"
	}
	if !searchModes[req.Mode] {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("неизвестный режим %q", req.Mode))
		return
	}
	if req.Top <= 0 || req.Top > 50 {
		req.Top = 5
	}
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ix, err := s.getIndex(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	scorer := s.getScorer()
	var searchScorer index.Scorer
	var noulScorer laya.Scorer = scorer
	if strings.HasPrefix(req.Mode, "hybrid+") {
		searchScorer = scorer
	}
	hits, err := ix.Search(req.Query, req.Mode, req.Top, searchScorer)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	noul := noulScorer.Noul(req.Query, hits)
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		c := h.Chunk
		out = append(out, map[string]any{
			"score":       h.Score,
			"file_path":   c.FilePath,
			"start_line":  c.StartLine,
			"end_line":    c.EndLine,
			"kind":        c.Kind,
			"symbol_name": c.SymbolName,
			"signature":   c.Signature,
			"doc":         c.Doc,
			"content":     c.Content,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":      out,
		"mode":      req.Mode,
		"noul":      noul,
		"need_more": noul < 0.5,
	})
}

// handleSkill — текст SKILL.md для агента (скачивание/копирование из панели).
func (s *server) handleSkill(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"content": string(skillMD)})
}

// skillDirs — каталоги скиллов агента в проекте (Kimi Code: проектный уровень).
var skillDirs = []string{
	filepath.Join(".kimi-code", "skills"),
	filepath.Join(".agents", "skills"),
}

// skillStatus — установлен ли скилл codepilot в проекте и есть ли вообще
// каталог скиллов (используется для предложения установки в UI).
func (s *server) skillStatus(abs string) map[string]any {
	st := map[string]any{"installed": false, "skills_dir": false, "path": ""}
	for _, d := range skillDirs {
		p := filepath.Join(abs, d)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			st["skills_dir"] = true
		}
		target := filepath.Join(p, "codepilot", "SKILL.md")
		if _, err := os.Stat(target); err == nil {
			st["installed"] = true
			st["path"] = target
		}
	}
	return st
}

func (s *server) handleSkillStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	abs, err := filepath.Abs(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.skillStatus(abs))
}

// handleSkillInstall кладёт SKILL.md в каталог скиллов проекта: если уже есть
// .agents/skills — туда, иначе .kimi-code/skills/codepilot/. Идемпотентно.
func (s *server) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
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
	dir := filepath.Join(abs, skillDirs[0])
	if fi, err := os.Stat(filepath.Join(abs, skillDirs[1])); err == nil && fi.IsDir() {
		dir = filepath.Join(abs, skillDirs[1])
	}
	target := filepath.Join(dir, "codepilot")
	if err := os.MkdirAll(target, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	p := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(p, skillMD, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "skill": s.skillStatus(abs)})
}
