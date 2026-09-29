package chunk

import "fmt"

const (
	fallbackWindow  = 60
	fallbackOverlap = 10
)

// Fallback — чанкинг окнами по ~60 строк с перекрытием 10,
// используется, когда файл не парсится языковым чанкером.
func Fallback(relPath, lang string, src []byte) []Chunk {
	lines := Lines(src)
	if len(lines) == 0 {
		return nil
	}
	hash := HashBytes(src)
	var out []Chunk
	step := fallbackWindow - fallbackOverlap
	for start := 1; start <= len(lines); start += step {
		end := start + fallbackWindow - 1
		if end > len(lines) {
			end = len(lines)
		}
		sym := fmt.Sprintf("window@%d", start)
		sig := fmt.Sprintf("%s lines %d-%d", relPath, start, end)
		out = append(out, newChunk(relPath, lang, start, end, sym, "window", sig, "", lines, hash))
		if end == len(lines) {
			break
		}
	}
	return out
}
