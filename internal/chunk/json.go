package chunk

import (
	"encoding/json"
	"regexp"
	"sort"
)

var jsonKeyRe = regexp.MustCompile(`^\s*"([^"]+)"\s*:\s*(?:\{|\[)`)

// chunkJSON разбивает JSON по ключам верхнего уровня.
// Если файл не парсится (например, JSONL/minified) — fallback окна.
func chunkJSON(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)

	var cands []struct {
		line int
		name string
	}
	for i, l := range lines {
		if m := jsonKeyRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, struct{ line int; name string }{i, m[1]})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].line < cands[b].line })

	var out []Chunk
	lastEnd := -1
	for _, c := range cands {
		if c.line <= lastEnd {
			continue
		}
		end := jsonExtent(lines, c.line)
		lastEnd = end
		ch := newChunk(relPath, "json", c.line+1, end+1, c.name, "key", "", "", lines, hash)
		ch.Content = joinLines(lines, c.line+1, end+1)
		out = append(out, ch)
	}
	if len(out) == 0 {
		// возможно minified JSON — попробуем распарсить и разбить по путям
		if parsed, ok := parseJSONPaths(src); ok && len(parsed) > 0 {
			for _, p := range parsed {
				out = append(out, newChunk(relPath, "json", p.line, p.line, p.key, "key", p.path, "", lines, hash))
			}
			return out
		}
		return Fallback(relPath, "json", src)
	}
	return out
}

// jsonExtent балансирует [] и {}.
func jsonExtent(lines []string, start int) int {
	depth := 0
	seen := false
	for j := start; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{', '[':
				depth++
				seen = true
			case '}', ']':
				depth--
			}
		}
		if seen && depth <= 0 {
			return j
		}
	}
	return len(lines) - 1
}

type jsonPath struct {
	key  string
	path string
	line int
}

// parseJSONPaths пробует распарсить JSON и вернуть пути к объектам/строкам верхнего уровня.
func parseJSONPaths(src []byte) ([]jsonPath, bool) {
	var root map[string]any
	if err := json.Unmarshal(src, &root); err != nil {
		return nil, false
	}
	var out []jsonPath
	for k := range root {
		out = append(out, jsonPath{key: k, path: "$" + k, line: 1})
	}
	return out, true
}
