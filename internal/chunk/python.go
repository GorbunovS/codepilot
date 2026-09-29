package chunk

import (
	"regexp"
	"strings"
)

var (
	pyDefRe    = regexp.MustCompile(`^(?:async\s+def|def|class)\s+([A-Za-z_]\w*)`)
	pyKindRe   = regexp.MustCompile(`^(async\s+def|def|class)`)
	pyAssignRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s*=[^=]`)
)

// chunkPython чанкит Python по отступам: def/class/присваивания верхнего
// уровня, docstring после def/class, модульный docstring отдельным чанком.
func chunkPython(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	n := len(lines)
	var out []Chunk

	isTopLevel := func(i int) bool {
		l := lines[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			return false
		}
		t := strings.TrimSpace(l)
		return t != "" && !strings.HasPrefix(t, "#")
	}

	if doc, end := pyDocstring(lines, 0); doc != "" {
		out = append(out, newChunk(relPath, "python", 1, end, "module", "module", relPath, doc, lines, hash))
	}

	for i := 0; i < n; i++ {
		if !isTopLevel(i) {
			continue
		}
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "@") {
			continue // декоратор подхватывается следующим def/class
		}
		if m := pyDefRe.FindStringSubmatch(t); m != nil {
			start := i + 1
			for j := i - 1; j >= 0; j-- {
				tj := strings.TrimSpace(lines[j])
				if tj == "" {
					continue
				}
				if strings.HasPrefix(tj, "@") && lines[j][0] != ' ' && lines[j][0] != '\t' {
					start = j + 1
					continue
				}
				break
			}
			end := n
			for j := i + 1; j < n; j++ {
				if isTopLevel(j) {
					end = j
					break
				}
			}
			kind := "func"
			if pyKindRe.FindString(t) == "class" {
				kind = "class"
			}
			sig := strings.TrimRight(t, ":")
			doc, _ := pyDocstring(lines, i+1)
			out = append(out, newChunk(relPath, "python", start, end, m[1], kind, sig, doc, lines, hash))
			continue
		}
		if m := pyAssignRe.FindStringSubmatch(t); m != nil {
			out = append(out, newChunk(relPath, "python", i+1, i+1, m[1], "var", t, "", lines, hash))
		}
	}
	if len(out) == 0 {
		return Fallback(relPath, "python", src)
	}
	return out
}

// pyDocstring извлекает docstring, начинающийся на первой непустой строке
// с позиции from (0-based). Возвращает текст и 1-based номер последней строки.
func pyDocstring(lines []string, from int) (string, int) {
	for i := from; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		for _, q := range []string{`"""`, `'''`} {
			if strings.HasPrefix(t, q) {
				rest := t[len(q):]
				if idx := strings.Index(rest, q); idx >= 0 {
					return strings.TrimSpace(rest[:idx]), i + 1
				}
				var b strings.Builder
				b.WriteString(rest)
				for j := i + 1; j < len(lines); j++ {
					if idx := strings.Index(lines[j], q); idx >= 0 {
						b.WriteString("\n" + lines[j][:idx])
						return strings.TrimSpace(b.String()), j + 1
					}
					b.WriteString("\n" + lines[j])
				}
				return strings.TrimSpace(b.String()), len(lines)
			}
		}
		return "", 0
	}
	return "", 0
}
