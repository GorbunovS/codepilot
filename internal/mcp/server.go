// Package mcp реализует минимальный MCP-сервер по stdio:
// newline-delimited JSON-RPC 2.0 без заголовков.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codepilot/internal/index"
	"codepilot/internal/laya"
)

// Project — проект, обслуживаемый сервером (мультипроектный режим).
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ResolveFunc открывает индекс проекта по имени/пути из аргумента project
// (пустая строка — проект по умолчанию). Ошибка должна перечислять
// доступные имена, чтобы агент мог исправиться без перезапуска сервера.
type ResolveFunc func(nameOrPath string) (*index.Index, error)

// ReindexFunc запускает инкрементальную переиндексацию проекта. Вызов
// должен быть быстрым: тяжёлая работа идёт в фоне, агенту возвращается
// подтверждение запуска. nil у сервера — триггер недоступен.
type ReindexFunc func(nameOrPath string) error

// Server — MCP-сервер поверх индексов одного или нескольких проектов.
type Server struct {
	resolve  ResolveFunc
	reindex  ReindexFunc
	projects func() []Project // для list_projects; nil — один проект
	def      string           // имя/путь проекта по умолчанию
	scorer   laya.Scorer
	log      *os.File

	// logMu сериализует запись в jsonl-лог: по HTTP запросы приходят
	// параллельно, в отличие от stdio-цикла.
	logMu sync.Mutex

	// rerankBusy — защита от наложения тяжёлых Laya-инференсов: вызов,
	// пришедший во время счёта предыдущего, сразу деградирует в hybrid.
	// Флаг общий на все проекты: узкое место — scorer, он один.
	rerankBusy atomic.Bool
	// searchTimeout — бюджет search_code с реранком (env
	// CODEPILOT_MCP_SEARCH_TIMEOUT, секунды).
	searchTimeout time.Duration
}

// NewServer — обработчик JSON-RPC поверх резолвера индексов; общая основа
// stdio (Serve/ServeMulti) и HTTP-транспорта (HandleRaw).
func NewServer(resolve ResolveFunc, projects func() []Project, def string, scorer laya.Scorer) *Server {
	if scorer == nil {
		scorer = laya.Heuristic{}
	}
	if projects == nil {
		projects = func() []Project { return nil }
	}
	return &Server{resolve: resolve, projects: projects, def: def, scorer: scorer, searchTimeout: searchTimeoutDefault()}
}

// SetLog включает jsonl-лог вызовов инструментов (пустой путь — без лога).
func (s *Server) SetLog(logPath string) error {
	if logPath == "" {
		return nil
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	s.log = f
	return nil
}

// SetReindex подключает триггер переиндексации (инструмент reindex).
func (s *Server) SetReindex(f ReindexFunc) { s.reindex = f }

// HandleRaw обрабатывает один JSON-RPC запрос и возвращает сериализованный
// ответ; nil — ответ не требуется (notification или пустой ввод).
// Потокобезопасно.
func (s *Server) HandleRaw(raw []byte) []byte {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	var req rpcRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		resp, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return resp
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil // уведомления не требуют ответа
	}
	resp := s.handle(&req)
	if resp == nil {
		return nil
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return data
}

// Serve читает JSON-RPC запросы из in (по строкам) и пишет ответы в out.
// Однопроектный режим: все инструменты работают с ix, аргумент project
// игнорируется. scorer — слой решений Laya (эвристика или ONNX-модель);
// nil = эвристика. Каждый вызов инструмента логируется в logPath (jsonl).
func Serve(ix *index.Index, logPath string, in io.Reader, out io.Writer, scorer laya.Scorer) error {
	return RunServer(NewServer(func(string) (*index.Index, error) { return ix, nil }, nil, "", scorer), logPath, in, out)
}

// ServeMulti — мультипроектный режим: resolve открывает индекс по имени
// проекта из аргументов инструмента, projects перечисляет реестр для
// инструмента list_projects, def — проект по умолчанию (пустой project).
func ServeMulti(resolve ResolveFunc, projects func() []Project, def, logPath string, in io.Reader, out io.Writer, scorer laya.Scorer) error {
	return RunServer(NewServer(resolve, projects, def, scorer), logPath, in, out)
}

// RunServer — stdio-цикл для готового сервера (нужен, когда сервер
// собран через NewServer с дополнительными настройками вроде SetReindex).
func RunServer(s *Server, logPath string, in io.Reader, out io.Writer) error {
	if err := s.SetLog(logPath); err == nil && s.log != nil {
		defer s.log.Close()
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		resp := s.HandleRaw(sc.Bytes())
		if resp == nil {
			continue
		}
		if _, err := out.Write(append(resp, '\n')); err != nil {
			return err
		}
	}
	return sc.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func ok(id json.RawMessage, result interface{}) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *Server) handle(req *rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "codepilot", "version": "0.2.0"},
			"instructions": "Сервер обслуживает несколько проектов. Все инструменты принимают необязательный " +
				"аргумент project — имя из list_projects (или путь); пустой project — проект по умолчанию. " +
				"Начинайте с search_code, при need_more дочитывайте через read_span.",
		})
	case "ping":
		return ok(req.ID, map[string]interface{}{})
	case "tools/list":
		return ok(req.ID, map[string]interface{}{"tools": toolDefs()})
	case "tools/call":
		return s.toolsCall(req)
	default:
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

func toolDefs() []map[string]interface{} {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	proj := func() map[string]interface{} {
		return str("имя проекта из list_projects (или путь); пусто — проект по умолчанию")
	}
	return []map[string]interface{}{
		{
			"name":        "list_projects",
			"description": "Список проектов, обслуживаемых сервером: имя, путь, проект по умолчанию.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			"name": "search_code",
			"description": "Гибридный поиск по коду (BM25 + TF-IDF + RRF + Laya rerank). " +
				"Возвращает топ чанков с содержимым, метаданными и флагом need_more.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":   str("запрос на естественном языке или имя символа"),
					"top_k":   map[string]interface{}{"type": "integer", "default": 5},
					"project": proj(),
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_symbol",
			"description": "Определение символа из индекса: файл, span, сигнатура, doc.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":    str("имя символа"),
					"project": proj(),
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "find_references",
			"description": "Текстовый поиск упоминаний имени (word-boundary, без строк и комментариев). Список file:line.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":    str("имя символа"),
					"project": proj(),
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "read_span",
			"description": "Прочитать точные строки файла (1-based, включительно).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file":    str("путь относительно корня проекта"),
					"start":   map[string]interface{}{"type": "integer"},
					"end":     map[string]interface{}{"type": "integer"},
					"project": proj(),
				},
				"required": []string{"file", "start", "end"},
			},
		},
		{
			"name": "reindex",
			"description": "Запустить инкрементальную переиндексацию проекта в фоне. " +
				"Вызывай, когда задача завершена и код изменился (в т.ч. перед пушем), " +
				"чтобы индекс не устаревал. Быстрый no-op, если файлы не менялись.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"project": proj(),
				},
			},
		},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type logEntry struct {
	TS         string    `json:"ts"`
	Tool       string    `json:"tool"`
	Args       string    `json:"args"`
	LatencyMs  int64     `json:"latency_ms"`
	RespBytes  int       `json:"response_bytes"`
	Scores     []float64 `json:"scores,omitempty"`
}

func (s *Server) toolsCall(req *rpcRequest) *rpcResponse {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}}
	}
	start := time.Now()
	payload, scores, err := s.dispatch(p.Name, p.Arguments)
	entry := logEntry{
		TS: time.Now().Format(time.RFC3339), Tool: p.Name, Args: string(p.Arguments),
		LatencyMs: time.Since(start).Milliseconds(), Scores: scores,
	}
	var text string
	isErr := err != nil
	if isErr {
		text = "error: " + err.Error()
	} else {
		data, mErr := json.MarshalIndent(payload, "", "  ")
		if mErr != nil {
			text = "error: " + mErr.Error()
			isErr = true
		} else {
			text = string(data)
		}
	}
	entry.RespBytes = len(text)
	s.writeLog(entry)
	return ok(req.ID, map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isErr,
	})
}

func (s *Server) writeLog(e logEntry) {
	if s.log == nil {
		return
	}
	if data, err := json.Marshal(e); err == nil {
		s.logMu.Lock()
		defer s.logMu.Unlock()
		_, _ = s.log.Write(append(data, '\n'))
	}
}

func (s *Server) dispatch(name string, args json.RawMessage) (interface{}, []float64, error) {
	switch name {
	case "list_projects":
		payload, err := s.listProjects()
		return payload, nil, err
	case "search_code":
		return s.searchCode(args)
	case "get_symbol":
		payload, err := s.getSymbol(args)
		return payload, nil, err
	case "find_references":
		payload, err := s.findReferences(args)
		return payload, nil, err
	case "read_span":
		payload, err := s.readSpan(args)
		return payload, nil, err
	case "reindex":
		payload, err := s.reindexProject(args)
		return payload, nil, err
	default:
		return nil, nil, fmt.Errorf("unknown tool %q", name)
	}
}

// reindexProject — триггер инкрементальной переиндексации: быстрый ответ
// агенту, тяжёлая работа в фоне на стороне демона.
func (s *Server) reindexProject(args json.RawMessage) (interface{}, error) {
	var a struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if s.reindex == nil {
		return nil, fmt.Errorf("переиндексация через MCP недоступна в этом режиме сервера")
	}
	if err := s.reindex(a.Project); err != nil {
		return nil, err
	}
	return map[string]interface{}{"started": true, "project": a.Project}, nil
}

// listProjects — реестр проектов сервера с пометкой проекта по умолчанию.
func (s *Server) listProjects() (interface{}, error) {
	list := s.projects()
	type pj struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Default bool   `json:"is_default"`
	}
	out := make([]pj, 0, len(list))
	for i, p := range list {
		out = append(out, pj{p.Name, p.Path, p.Name == s.def || p.Path == s.def || (s.def == "" && i == 0)})
	}
	return map[string]interface{}{"projects": out}, nil
}

// indexFor — индекс проекта из аргумента project (пустой — по умолчанию).
func (s *Server) indexFor(project string) (*index.Index, error) {
	return s.resolve(project)
}

// searchTimeoutDefault — бюджет search_code с Laya-реранком. ONNX Laya на
// CPU считает пул из 20 чанков десятки секунд — дольше таймаута MCP-клиента
// (~60с). По дедлайну отдаём чистый hybrid-порядок с флагом degraded.
// Переопределяется env CODEPILOT_MCP_SEARCH_TIMEOUT (секунды).
func searchTimeoutDefault() time.Duration {
	if v := os.Getenv("CODEPILOT_MCP_SEARCH_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 25 * time.Second
}

// searchOutcome — результат тяжёлого пути (hybrid+rerank + Noul ONNX).
type searchOutcome struct {
	hits []index.SearchHit
	noul float64
	err  error
}

func (s *Server) searchCode(args json.RawMessage) (interface{}, []float64, error) {
	var a struct {
		Query   string `json:"query"`
		TopK    int    `json:"top_k"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, err
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	ix, err := s.indexFor(a.Project)
	if err != nil {
		return nil, nil, err
	}
	// Реранк уже идёт (прошлый вызов ещё досчитывается в фоне после своего
	// таймаута) — второй Run не ставим: сразу деградируем в hybrid.
	if !s.rerankBusy.CompareAndSwap(false, true) {
		return s.hybridFallback(ix, a.Query, a.TopK, "rerank_busy")
	}
	ch := make(chan searchOutcome, 1)
	go func() {
		defer s.rerankBusy.Store(false)
		hits, err := ix.Search(a.Query, "hybrid+rerank", a.TopK, s.scorer)
		if err != nil {
			ch <- searchOutcome{err: err}
			return
		}
		ch <- searchOutcome{hits: hits, noul: s.scorer.Noul(a.Query, hits)}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, nil, r.err
		}
		return searchPayload(a.Query, r.hits, r.noul, "")
	case <-time.After(s.searchTimeout):
		// goroutine досчитает в фоне (ORT Run не отменить) — отвечаем hybrid
		return s.hybridFallback(ix, a.Query, a.TopK, "rerank_timeout")
	}
}

// hybridFallback — деградированный search_code: чистый hybrid-порядок,
// Noul эвристикой (ONNX-инференс здесь недопустим — он и есть причина
// деградации), reason в поле degraded.
func (s *Server) hybridFallback(ix *index.Index, query string, topK int, reason string) (interface{}, []float64, error) {
	hits, err := ix.Search(query, "hybrid", topK, nil)
	if err != nil {
		return nil, nil, err
	}
	noul := laya.Heuristic{}.Noul(query, hits)
	return searchPayload(query, hits, noul, reason)
}
func searchPayload(query string, hits []index.SearchHit, noul float64, degraded string) (interface{}, []float64, error) {
	var scores []float64
	for _, h := range hits {
		scores = append(scores, h.Score)
	}
	out := map[string]interface{}{
		"query":     query,
		"results":   hits,
		"noul":      noul,
		"need_more": noul < 0.5,
	}
	if degraded != "" {
		out["degraded"] = degraded
	}
	return out, scores, nil
}

func (s *Server) getSymbol(args json.RawMessage) (interface{}, error) {
	var a struct {
		Name    string `json:"name"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	ix, err := s.indexFor(a.Project)
	if err != nil {
		return nil, err
	}
	found := ix.FindSymbol(a.Name)
	if len(found) == 0 {
		return nil, fmt.Errorf("symbol %q not found in index", a.Name)
	}
	type sym struct {
		File      string `json:"file"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		Kind      string `json:"kind"`
		Language  string `json:"language"`
		Signature string `json:"signature"`
		Doc       string `json:"doc"`
	}
	var out []sym
	for _, c := range found {
		out = append(out, sym{c.FilePath, c.StartLine, c.EndLine, c.Kind, c.Language, c.Signature, c.Doc})
	}
	return map[string]interface{}{"symbols": out}, nil
}

var stringLitRe = regexp.MustCompile("`[^`]*`|\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'")

// stripCode убирает строковые литералы и строчные комментарии (насколько
// разумно без полноценного парсера; блочные комментарии не обрабатываются).
func stripCode(line, lang string) string {
	line = stringLitRe.ReplaceAllString(line, "")
	switch lang {
	case "python":
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
	default:
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
	}
	return line
}

func (s *Server) findReferences(args json.RawMessage) (interface{}, error) {
	var a struct {
		Name    string `json:"name"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	ix, err := s.indexFor(a.Project)
	if err != nil {
		return nil, err
	}
	wordRe, err := regexp.Compile(`\b` + regexp.QuoteMeta(a.Name) + `\b`)
	if err != nil {
		return nil, err
	}
	type ref struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	var refs []ref
	seen := map[string]bool{}
	for _, c := range ix.Chunks {
		seen[c.FilePath] = true
	}
	for rel := range seen {
		data, err := os.ReadFile(filepath.Join(ix.ProjectRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		lang := ""
		if filepath.Ext(rel) == ".py" {
			lang = "python"
		}
		for i, line := range strings.Split(string(data), "\n") {
			if wordRe.MatchString(stripCode(line, lang)) {
				refs = append(refs, ref{rel, i + 1, strings.TrimSpace(line)})
				if len(refs) >= 50 {
					return map[string]interface{}{"name": a.Name, "references": refs, "truncated": true}, nil
				}
			}
		}
	}
	return map[string]interface{}{"name": a.Name, "references": refs, "truncated": false}, nil
}

func (s *Server) readSpan(args json.RawMessage) (interface{}, error) {
	var a struct {
		File    string `json:"file"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	ix, err := s.indexFor(a.Project)
	if err != nil {
		return nil, err
	}
	content, err := index.ReadSpan(ix.ProjectRoot, a.File, a.Start, a.End)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"file": a.File, "start": a.Start, "end": a.End, "content": content,
	}, nil
}
