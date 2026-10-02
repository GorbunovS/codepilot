package chunk

import (
	"regexp"
	"sort"
	"strings"
)

var (
	qmlFuncRe      = regexp.MustCompile(`^\s*(?:function)\s+([A-Za-z_][\w]*)\s*\(`)
	qmlSignalRe    = regexp.MustCompile(`^\s*signal\s+([A-Za-z_][\w]*)\b`)
	qmlPropertyRe  = regexp.MustCompile(`^\s*(?:readonly\s+)?property\s+(?:[a-zA-Z_][\w]*)\s+([A-Za-z_][\w]*)\s*:?`)
	qmlComponentRe = regexp.MustCompile(`^\s*([A-Z][a-zA-Z0-9_]*)\s*\{`)
)

// chunkQML разбивает QML-файл на чанки: компоненты, функции, сигналы, свойства.
func chunkQML(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)

	type cand struct {
		line int
		name string
		kind string
	}
	var cands []cand
	for i, l := range lines {
		if m := qmlFuncRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, cand{i, m[1], "function"})
			continue
		}
		if m := qmlSignalRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, cand{i, m[1], "signal"})
			continue
		}
		if m := qmlPropertyRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, cand{i, m[1], "property"})
			continue
		}
		if m := qmlComponentRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, cand{i, m[1], "component"})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].line < cands[b].line })

	var out []Chunk
	lastEnd := -1
	for _, c := range cands {
		if c.line <= lastEnd {
			continue // вложенный кандидат
		}
		end := qmlExtent(lines, c.line)
		lastEnd = end
		doc := jsDoc(lines, c.line)
		sig := strings.TrimSpace(lines[c.line])
		ch := newChunk(relPath, "qml", c.line+1, end+1, c.name, c.kind, sig, doc, nil, hash)
		ch.Content = joinLines(lines, c.line+1, end+1)
		out = append(out, ch)
	}
	if len(out) == 0 {
		return Fallback(relPath, "qml", src)
	}
	return out
}

// qmlExtent возвращает 0-based индекс последней строки конструкции,
// начинающейся на строке start: по балансу фигурных скобок либо до ';'.
func qmlExtent(lines []string, start int) int {
	depth := 0
	seenBrace := false
	for j := start; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{':
				depth++
				seenBrace = true
			case '}':
				depth--
			case ';':
				if !seenBrace {
					return j
				}
			}
		}
		if seenBrace && depth <= 0 {
			return j
		}
		if !seenBrace && j > start {
			return start
		}
	}
	return len(lines) - 1
}
