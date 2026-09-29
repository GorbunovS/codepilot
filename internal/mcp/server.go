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
	"strings"
	"time"

	"codepilot/internal/index"
	"codepilot/internal/laya"
)

// Server — MCP-сервер поверх индекса.
type Server struct {
	ix     *index.Index
	scorer laya.Scorer
	log    *os.File
}

// Serve читает JSON-RPC запросы из in (по строкам) и пишет ответы в out.
// scorer — слой решений Laya (эвристика или ONNX-модель); nil = эвристика.
// Каждый вызов инструмента логируется в logPath (jsonl).
func Serve(ix *index.Index, logPath string, in io.Reader, out io.Writer, scorer laya.Scorer) error {
	if scorer == nil {
		scorer = laya.Heuristic{}
	}
	s := &Server{ix: ix, scorer: scorer}
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			s.log = f
			defer f.Close()
		}
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if strings.HasPrefix(req.Method, "notifications/") {
			continue // уведомления не требуют ответа
		}
		resp := s.handle(&req)
		if resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
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
			"serverInfo":      map[string]interface{}{"name": "codepilot", "version": "0.1.0"},
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
	return []map[string]interface{}{
		{
			"name": "search_code",
			"description": "Гибридный поиск по коду (BM25 + TF-IDF + RRF + Laya rerank). " +
				"Возвращает топ чанков с содержимым, метаданными и флагом need_more.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": str("запрос на естественном языке или имя символа"),
					"top_k": map[string]interface{}{"type": "integer", "default": 5},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "get_symbol",
			"description": "Определение символа из индекса: файл, span, сигнатура, doc.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"name": str("имя символа")},
				"required":   []string{"name"},
			},
		},
		{
			"name":        "find_references",
			"description": "Текстовый поиск упоминаний имени (word-boundary, без строк и комментариев). Список file:line.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"name": str("имя символа")},
				"required":   []string{"name"},
			},
		},
		{
			"name":        "read_span",
			"description": "Прочитать точные строки файла (1-based, включительно).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"file":  str("путь относительно корня проекта"),
					"start": map[string]interface{}{"type": "integer"},
					"end":   map[string]interface{}{"type": "integer"},
				},
				"required": []string{"file", "start", "end"},
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
		_, _ = s.log.Write(append(data, '\n'))
	}
}

func (s *Server) dispatch(name string, args json.RawMessage) (interface{}, []float64, error) {
	switch name {
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
	default:
		return nil, nil, fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) searchCode(args json.RawMessage) (interface{}, []float64, error) {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, err
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}
	hits, err := s.ix.Search(a.Query, "hybrid+rerank", a.TopK, s.scorer)
	if err != nil {
		return nil, nil, err
	}
	noul := s.scorer.Noul(a.Query, hits)
	var scores []float64
	for _, h := range hits {
		scores = append(scores, h.Score)
	}
	return map[string]interface{}{
		"query":     a.Query,
		"results":   hits,
		"noul":      noul,
		"need_more": noul < 0.5,
	}, scores, nil
}

func (s *Server) getSymbol(args json.RawMessage) (interface{}, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	found := s.ix.FindSymbol(a.Name)
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
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Name == "" {
		return nil, fmt.Errorf("name is required")
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
	for _, c := range s.ix.Chunks {
		seen[c.FilePath] = true
	}
	for rel := range seen {
		data, err := os.ReadFile(filepath.Join(s.ix.ProjectRoot, filepath.FromSlash(rel)))
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
		File  string `json:"file"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	content, err := index.ReadSpan(s.ix.ProjectRoot, a.File, a.Start, a.End)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"file": a.File, "start": a.Start, "end": a.End, "content": content,
	}, nil
}
