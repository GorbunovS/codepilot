package chunk

import (
	"regexp"
	"strings"
)

var mdHeaderRe = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

// chunkMarkdown разбивает Markdown по заголовкам.
func chunkMarkdown(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)

	var starts []int
	for i, l := range lines {
		if mdHeaderRe.MatchString(l) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return Fallback(relPath, "markdown", src)
	}

	var out []Chunk
	for idx, s := range starts {
		end := len(lines) - 1
		if idx+1 < len(starts) {
			end = starts[idx+1] - 1
		}
		m := mdHeaderRe.FindStringSubmatch(lines[s])
		level, title := len(m[1]), strings.TrimSpace(m[2])
		ch := newChunk(relPath, "markdown", s+1, end+1, title, "h"+itoa(level), "", "", lines, hash)
		ch.Content = joinLines(lines, s+1, end+1)
		out = append(out, ch)
	}
	return out
}

func itoa(n int) string {
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
