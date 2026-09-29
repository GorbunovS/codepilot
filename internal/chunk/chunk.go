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
const Version = 3

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
	".go":  chunkGo,
	".py":  chunkPython,
	".js":  chunkJS,
	".vue": chunkVue,
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
