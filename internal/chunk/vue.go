package chunk

import (
	"regexp"
	"strings"
)

var (
	vueScriptOpen  = regexp.MustCompile(`(?i)<script[^>]*>`)
	vueScriptClose = regexp.MustCompile(`(?i)</script>`)
	vueTmplOpen    = regexp.MustCompile(`(?i)<template[^>]*>`)
	vueTmplClose   = regexp.MustCompile(`(?i)</template>`)
)

// chunkVue чанкит Vue SFC: блок <script> чанкится как JS (позиции строк —
// в исходном файле), блок <template> — отдельным чанком.
func chunkVue(relPath string, src []byte) []Chunk {
	lines := Lines(src)
	hash := HashBytes(src)
	var out []Chunk
	sStart, sEnd, tStart, tEnd := -1, -1, -1, -1
	for i, l := range lines {
		if sStart < 0 && vueScriptOpen.MatchString(l) {
			sStart = i
		}
		if vueScriptClose.MatchString(l) {
			sEnd = i
		}
		if tStart < 0 && vueTmplOpen.MatchString(l) {
			tStart = i
		}
		if vueTmplClose.MatchString(l) {
			tEnd = i
		}
	}
	if sStart >= 0 && sEnd > sStart {
		sub := lines[sStart+1 : sEnd]
		jsChunks := chunkJSLines(relPath, "vue", sub, sStart+1, hash)
		out = append(out, jsChunks...)
		// Vue SFC без export default (<script setup>, Composition API):
		// компонент не попадает в индекс как символ. Добавляем чанк всего
		// script-блока с именем файла — get_symbol("NodeCard") находит его.
		hasComponent := false
		for _, ch := range jsChunks {
			if ch.Kind == "component" {
				hasComponent = true
				break
			}
		}
		if !hasComponent {
			sig := "<script>"
			if sStart+1 < len(lines) {
				sig = strings.TrimSpace(lines[sStart+1])
			}
			ch := newChunk(relPath, "vue", sStart+2, sEnd+1, componentName(relPath), "component", sig, "", lines, hash)
			ch.Content = strings.Join(sub, "\n")
			out = append(out, ch)
		}
	}
	if tStart >= 0 && tEnd > tStart {
		out = append(out, newChunk(relPath, "vue", tStart+1, tEnd+1, "template", "template", "<template>", "", lines, hash))
	}
	if len(out) == 0 {
		return Fallback(relPath, "vue", src)
	}
	return out
}
