// Package chunk разбивает исходные файлы на семантические чанки.
// Реестр чанкеров по расширению файла позволяет легко добавить новый язык.
package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Version — версия логики чанкинга. Инкрементируйте при изменении чанкеров:
// индексы, собранные старой версией, пересобираются целиком.
const Version = 6

// Chunk — атомарная единица индекса: символ (функция, тип, класс) или окно.
type Chunk struct {
	ID         string `json:"id"`
	FilePath   string `json:"file_path"`
	Language   string `json:"language"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	SymbolName string `json:"symbol_name"`
	Kind       string `json:"kind"`
	Signature  string `json:"signature"`
	Doc        string `json:"doc"`
	Content    string `json:"content"`
	Hash       string `json:"hash"` // sha256 содержимого файла (для инкрементальности)
}

// Chunker разбивает содержимое одного файла на чанки.
// relPath — путь относительно корня проекта (slash-формат).
type Chunker func(relPath string, src []byte) []Chunk

var registry = map[string]Chunker{
	".go":    chunkGo,
	".py":    chunkPython,
	".js":    chunkJS,
	".jsx":   chunkJS,
	".ts":    chunkJS,
	".tsx":   chunkJS,
	".vue":   chunkVue,
	".qml":   chunkQML,
	".json":  chunkJSON,
	".md":    chunkMarkdown,
	".yaml":  chunkYAML,
	".yml":   chunkYAML,
	".toml":  chunkTOML,
	".ini":   chunkINI,
	".cfg":   chunkINI,
	".css":   chunkCSS,
	".scss":  chunkCSS,
	".less":  chunkCSS,
	".html":  chunkHTML,
	".htm":   chunkHTML,
	".sh":    chunkShell,
	".bash":  chunkShell,
	".zsh":   chunkShell,
}

// Language возвращает язык по расширению файла (для fallback-чанков).
func Language(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".vue":
		return "vue"
	case ".qml":
		return "qml"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".ini", ".cfg":
		return "ini"
	case ".css", ".scss", ".less":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".sh", ".bash", ".zsh":
		return "shell"
	default:
		return "text"
	}
}

// ChunkFile — обёртка чанкера: если чанкер не нашёл ни одного символа
// (все чанки — fallback-окна), добавляет чанк всего файла с именем файла.
// Иначе get_symbol и поиск по имени файла не сработают.
func ChunkFile(relPath string, src []byte) []Chunk {
	ch, ok := ForFile(relPath)
	if !ok {
		return Fallback(relPath, Language(relPath), src)
	}
	out := ch(relPath, src)
	hasSymbol := false
	for _, c := range out {
		if c.Kind != "window" {
			hasSymbol = true
			break
		}
	}
	if hasSymbol {
		return out
	}
	lines := Lines(src)
	name := componentName(relPath)
	fileChunk := newChunk(relPath, Language(relPath), 1, len(lines), name, "file", name, "", lines, HashBytes(src))
	return append([]Chunk{fileChunk}, out...)
}

// SupportedExts возвращает отсортированный список поддерживаемых расширений.
func SupportedExts() []string {
	exts := make([]string, 0, len(registry))
	for e := range registry {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return exts
}

// ForFile возвращает чанкер для файла по его расширению.
func ForFile(path string) (Chunker, bool) {
	c, ok := registry[strings.ToLower(filepath.Ext(path))]
	return c, ok
}

// HashBytes — sha256 содержимого файла.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Lines делит исходник на строки (нормализуя CRLF).
func Lines(src []byte) []string {
	return strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
}

func joinLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

func newChunk(relPath, lang string, start, end int, symbol, kind, sig, doc string, lines []string, hash string) Chunk {
	return Chunk{
		ID:         fmt.Sprintf("%s:%d:%s", relPath, start, symbol),
		FilePath:   relPath,
		Language:   lang,
		StartLine:  start,
		EndLine:    end,
		SymbolName: symbol,
		Kind:       kind,
		Signature:  sig,
		Doc:        doc,
		Content:    joinLines(lines, start, end),
		Hash:       hash,
	}
}
