package chunk

import (
	"regexp"
	"strings"
)

var (
	yamlDocSepRe = regexp.MustCompile(`^---\s*$`)
	yamlTopKeyRe = regexp.MustCompile(`^([A-Za-z_][\w]*)\s*:`)
	tomlSectionRe = regexp.MustCompile(`^\[(\[+)?([^\]]+)\]\]?`)
	iniSectionRe  = regexp.MustCompile(`^\[([^\]]+)\]`)
	cssSelectorRe = regexp.MustCompile(`^([^{]+)\{`)
	shellFuncRe   = regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_][\w]*)\s*\(\)`)
)

// chunkYAML — YAML: разделение по `---` и верхнеуровневым ключам.
func chunkYAML(relPath string, src []byte) []Chunk {
	return chunkByTopKeys(relPath, "yaml", src, yamlDocSepRe, yamlTopKeyRe)
}

// chunkTOML — TOML: разделение по секциям `[section]`.
func chunkTOML(relPath string, src []byte) []Chunk {
	return chunkBySections(relPath, "toml", src, tomlSectionRe)
}

// chunkINI — INI/CFG: разделение по секциям `[section]`.
func chunkINI(relPath string, src []byte) []Chunk {
	return chunkBySections(relPath, "ini", src, iniSectionRe)
}

// chunkCSS — CSS/SCSS/LESS: разделение по блокам правил.
func chunkCSS(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	var out []Chunk
	lastEnd := -1
	for i, l := range lines {
		if i <= lastEnd {
			continue
		}
		m := cssSelectorRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		end := cssExtent(lines, i)
		lastEnd = end
		sel := strings.TrimSpace(m[1])
		ch := newChunk(relPath, "css", i+1, end+1, sel, "rule", sel, "", lines, hash)
		ch.Content = joinLines(lines, i+1, end+1)
		out = append(out, ch)
	}
	if len(out) == 0 {
		return Fallback(relPath, "css", src)
	}
	return out
}

// chunkShell — shell/bash/zsh: по функциям.
func chunkShell(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	var out []Chunk
	lastEnd := -1
	for i, l := range lines {
		if i <= lastEnd {
			continue
		}
		m := shellFuncRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		end := shellExtent(lines, i)
		lastEnd = end
		ch := newChunk(relPath, "shell", i+1, end+1, m[1], "function", strings.TrimSpace(l), "", lines, hash)
		ch.Content = joinLines(lines, i+1, end+1)
		out = append(out, ch)
	}
	if len(out) == 0 {
		return Fallback(relPath, "shell", src)
	}
	return out
}

// chunkHTML — HTML/HTM: по крупным тегам (h1-h6, section, article, script, style, div).
func chunkHTML(relPath string, src []byte) []Chunk {
	re := regexp.MustCompile(`<(?:h[1-6]|section|article|script|style|div)(?:\s|>|/)`)
	lines := Lines(src)
	hash := HashBytes(src)
	var starts []int
	for i, l := range lines {
		if re.MatchString(strings.ToLower(l)) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return Fallback(relPath, "html", src)
	}
	var out []Chunk
	for idx, s := range starts {
		end := len(lines) - 1
		if idx+1 < len(starts) {
			end = starts[idx+1] - 1
		}
		ch := newChunk(relPath, "html", s+1, end+1, "block@"+itoa(s+1), "block", "", "", lines, hash)
		ch.Content = joinLines(lines, s+1, end+1)
		out = append(out, ch)
	}
	return out
}

// chunkByTopKeys — общий YAML-подобный чанкер.
func chunkByTopKeys(relPath, lang string, src []byte, docSep, keyRe *regexp.Regexp) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	docs := []int{-1}
	for i, l := range lines {
		if docSep.MatchString(l) {
			docs = append(docs, i)
		}
	}

	var out []Chunk
	for d, docStart := range docs {
		docEnd := len(lines) - 1
		if d+1 < len(docs) {
			docEnd = docs[d+1] - 1
		}
		for i := docStart + 1; i <= docEnd; i++ {
			m := keyRe.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			j := i + 1
			for ; j <= docEnd; j++ {
				if keyRe.MatchString(lines[j]) {
					break
				}
			}
			end := j - 1
			i = end
			ch := newChunk(relPath, lang, i+1, end+1, m[1], "key", strings.TrimSpace(lines[i]), "", lines, hash)
			ch.Content = joinLines(lines, i+1, end+1)
			out = append(out, ch)
		}
	}
	if len(out) == 0 {
		return Fallback(relPath, lang, src)
	}
	return out
}

// chunkBySections — общий INI/TOML-подобный чанкер.
func chunkBySections(relPath, lang string, src []byte, sectionRe *regexp.Regexp) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	var starts []struct {
		line int
		name string
	}
	for i, l := range lines {
		if m := sectionRe.FindStringSubmatch(l); m != nil {
			name := m[len(m)-1]
			starts = append(starts, struct{ line int; name string }{i, name})
		}
	}
	if len(starts) == 0 {
		return Fallback(relPath, lang, src)
	}
	var out []Chunk
	for idx, s := range starts {
		end := len(lines) - 1
		if idx+1 < len(starts) {
			end = starts[idx+1].line - 1
		}
		ch := newChunk(relPath, lang, s.line+1, end+1, s.name, "section", "", "", lines, hash)
		ch.Content = joinLines(lines, s.line+1, end+1)
		out = append(out, ch)
	}
	return out
}

func cssExtent(lines []string, start int) int {
	depth := 0
	seen := false
	for j := start; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{':
				depth++
				seen = true
			case '}':
				depth--
			}
		}
		if seen && depth <= 0 {
			return j
		}
	}
	return len(lines) - 1
}

func shellExtent(lines []string, start int) int {
	depth := 0
	for j := start; j < len(lines); j++ {
		for _, r := range lines[j] {
			switch r {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if depth <= 0 && j > start {
			return j
		}
	}
	return len(lines) - 1
}
