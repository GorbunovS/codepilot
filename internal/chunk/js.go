package chunk

import (
	"regexp"
	"sort"
	"strings"
)

var (
	jsFuncRe    = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)
	jsConstRe   = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)
	jsMethRe    = regexp.MustCompile(`^\s*(?:async\s+)?([A-Za-z_$][\w$]*)\s*\([^()]*\)\s*\{`)
	jsDefaultRe = regexp.MustCompile(`^\s*export\s+default\b`)
)

var jsKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true,
	"catch": true, "return": true, "else": true, "do": true,
	"function": true, "new": true, "throw": true,
}

// componentName — имя компонента из имени файла (OrderCard.vue → OrderCard).
func componentName(relPath string) string {
	base := relPath
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base
}

// chunkJS чанкит JS: function/const/стрелочные функции/методы объектов,
// JSDoc и //-комментарии прикрепляются к чанку как doc.
func chunkJS(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	out := chunkJSLines(relPath, "js", lines, 0, HashBytes(src))
	if len(out) == 0 {
		return Fallback(relPath, "js", src)
	}
	return out
}

type jsCandidate struct {
	line int // 0-based
	name string
	kind string
}

// chunkJSLines чанкит JS-код в lines; offset — смещение строк (для Vue SFC,
// где script-блок чанкится с позициями в исходном файле).
func chunkJSLines(relPath, lang string, lines []string, offset int, hash string) []Chunk {
	var cands []jsCandidate
	for i, l := range lines {
		if jsDefaultRe.MatchString(l) {
			// export default — компонент/объект модуля; имя берём из файла,
			// чтобы примыкающий комментарий («// OrderCard — карточка заказа»)
			// не осиротел.
			cands = append(cands, jsCandidate{i, componentName(relPath), "component"})
			continue
		}
		if m := jsFuncRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, jsCandidate{i, m[1], "func"})
			continue
		}
		if m := jsConstRe.FindStringSubmatch(l); m != nil {
			cands = append(cands, jsCandidate{i, m[1], "var"})
			continue
		}
		if m := jsMethRe.FindStringSubmatch(l); m != nil {
			if jsKeywords[m[1]] {
				continue
			}
			cands = append(cands, jsCandidate{i, m[1], "method"})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].line < cands[b].line })

	var out []Chunk
	lastEnd := -1 // 0-based последняя строка предыдущего чанка
	for _, c := range cands {
		if c.line <= lastEnd {
			continue // вложенный кандидат внутри уже взятого чанка
		}
		end := jsExtent(lines, c.line)
		if c.kind != "component" {
			lastEnd = end // component-чанк не глотает вложенные методы
		}
		doc := jsDoc(lines, c.line)
		if doc == "" && c.kind == "component" {
			// у компонента комментарий часто оторван импортами/пустыми строками
			doc = jsDocLoose(lines, c.line)
		}
		sig := strings.TrimSpace(lines[c.line])
		ch := newChunk(relPath, lang, offset+c.line+1, offset+end+1, c.name, c.kind, sig, doc, nil, hash)
		ch.Content = joinLines(lines, c.line+1, end+1)
		out = append(out, ch)
	}
	return out
}

// jsExtent возвращает 0-based индекс последней строки конструкции,
// начинающейся на строке start: по балансу фигурных скобок либо до ';'.
func jsExtent(lines []string, start int) int {
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

// jsDocLoose — как jsDoc, но при подъёме пропускает пустые строки и import'ы
// (не более 6 строк): нужен для `export default`, чей doc-комментарий обычно
// стоит в шапке script-блока, оторванный от декларации.
func jsDocLoose(lines []string, line int) string {
	j := line - 1
	skipped := 0
	for j >= 0 && skipped < 6 {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "import(") {
			j--
			skipped++
			continue
		}
		break
	}
	if j < 0 {
		return ""
	}
	return jsDoc(lines, j+1)
}

// jsDoc собирает комментарии (// и /* */), примыкающие к строке line сверху.
func jsDoc(lines []string, line int) string {
	var out []string
	for j := line - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, "//") {
			out = append([]string{strings.TrimSpace(strings.TrimPrefix(t, "//"))}, out...)
			continue
		}
		if strings.HasSuffix(t, "*/") {
			var block []string
			for ; j >= 0; j-- {
				u := strings.TrimSpace(lines[j])
				block = append([]string{u}, block...)
				if strings.Contains(u, "/*") {
					break
				}
			}
			txt := strings.Join(block, "\n")
			txt = strings.TrimPrefix(txt, "/**")
			txt = strings.TrimPrefix(txt, "/*")
			txt = strings.TrimSuffix(txt, "*/")
			var cleaned []string
			for _, l := range strings.Split(txt, "\n") {
				l = strings.TrimSpace(l)
				l = strings.TrimSpace(strings.TrimPrefix(l, "*"))
				cleaned = append(cleaned, l)
			}
			out = append(cleaned, out...)
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
