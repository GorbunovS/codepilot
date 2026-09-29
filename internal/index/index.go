// Package index хранит индекс чанков (SQLite-файл index.db; легаси index.json
// читается и мигрирует), строит BM25/TF-IDF модели и реализует гибридный поиск.
package index

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codepilot/internal/chunk"
	"codepilot/internal/embed"
)

// DefaultIndexName — имя файла индекса в корне проекта.
const DefaultIndexName = "index.db"

// Index — сериализуемое состояние + построенные в памяти модели поиска.
type Index struct {
	ProjectRoot    string            `json:"project_root"`
	ChunkerVersion int               `json:"chunker_version"` // см. chunk.Version
	Manifest       map[string]string `json:"manifest"`        // file -> sha256
	Chunks         []chunk.Chunk     `json:"chunks"`

	// Emb и PG подключают векторный режим (e5 + pgvector); nil — легаси TF-IDF.
	// Заполняются снаружи (CLI) после Load, в хранилище не сериализуются.
	Emb *embed.Embedder `json:"-"`
	PG  *PGStore        `json:"-"`

	tf    []map[string]int
	dlen  []int
	df    map[string]int
	avgdl float64
	idf   map[string]float64
	vecs  []map[string]float64
	bySym map[string][]int
	byID  map[string]int
}

// Stats — статистика одного прогона индексации.
type Stats struct {
	Files     int
	Chunks    int
	Reindexed int
	Kept      int
	Removed   int
}

// IndexPath — путь к файлу индекса для корня проекта.
func IndexPath(root string) string { return filepath.Join(root, DefaultIndexName) }

// SourceFiles возвращает отсортированный список индексируемых файлов (rel, slash).
func SourceFiles(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := chunk.ForFile(path); !ok {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// Logf — необязательный лог прогресса индексации; по умолчанию молчит.
// CLI подставляет вывод в stderr, веб-панель — свой буфер.
// Не потокобезопасно: подменять до запуска индексации.
var Logf = func(format string, args ...any) {}

// CheckAbort — необязательный хук отмены индексации: если не nil и вернул
// true, Build/PGStore.Save прерываются с ErrAborted. nil — не отменять.
// Не потокобезопасно: подменять до запуска индексации (веб-панель
// сериализует индексацию глобальным гейтом).
var CheckAbort func() bool

// ErrAborted — индексация прервана пользователем (через CheckAbort).
var ErrAborted = errors.New("индексация отменена")

// Build выполняет полную или инкрементальную индексацию: по manifest
// переиндексируются только изменённые/новые файлы, удалённые выбрасываются.
// Файл индекса читается, но не сохраняется (для этого — Save).
func Build(root string) (*Index, Stats, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, Stats{}, err
	}
	ix := &Index{ProjectRoot: abs, Manifest: map[string]string{}, ChunkerVersion: chunk.Version}
	old := map[string][]chunk.Chunk{}
	if prev, err := loadPrev(IndexPath(abs)); err == nil && prev.ChunkerVersion == chunk.Version {
		// версия чанкера сошлась — можно переиспользовать неизменённые файлы
		for _, c := range prev.Chunks {
			old[c.FilePath] = append(old[c.FilePath], c)
		}
	}
	var st Stats
	files, err := SourceFiles(abs)
	if err != nil {
		return nil, st, err
	}
	Logf("index: %s: файлов к обходу %d", abs, len(files))
	seen := map[string]bool{}
	for _, rel := range files {
		if CheckAbort != nil && st.Files%200 == 0 && CheckAbort() {
			return nil, st, ErrAborted
		}
		src, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(rel)))
		if err != nil {
			return nil, st, err
		}
		h := chunk.HashBytes(src)
		seen[rel] = true
		st.Files++
		if st.Files%500 == 0 {
			Logf("index: обработано %d/%d файлов", st.Files, len(files))
		}
		if kept, ok := old[rel]; ok && len(kept) > 0 && kept[0].Hash == h {
			ix.Chunks = append(ix.Chunks, kept...)
			st.Kept++
		} else {
			ch, _ := chunk.ForFile(rel)
			ix.Chunks = append(ix.Chunks, ch(rel, src)...)
			st.Reindexed++
		}
		ix.Manifest[rel] = h
	}
	for rel := range old {
		if !seen[rel] {
			st.Removed++
		}
	}
	sort.Slice(ix.Chunks, func(a, b int) bool {
		if ix.Chunks[a].FilePath != ix.Chunks[b].FilePath {
			return ix.Chunks[a].FilePath < ix.Chunks[b].FilePath
		}
		return ix.Chunks[a].StartLine < ix.Chunks[b].StartLine
	})
	st.Chunks = len(ix.Chunks)
	ix.buildModel()
	Logf("index: чанкинг завершён: файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d)",
		st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed)
	return ix, st, nil
}

// Save записывает индекс в <project_root>/index.db (SQLite).
func (ix *Index) Save() error {
	return ix.saveStore(IndexPath(ix.ProjectRoot))
}

// Load читает индекс и перестраивает модели поиска. Формат определяется
// по расширению пути: .json — легаси-формат, иначе SQLite.
func Load(path string) (*Index, error) {
	if isJSONPath(path) {
		return loadJSON(path)
	}
	return loadStore(path)
}

// loadPrev загружает предыдущее состояние индекса для инкрементальной
// индексации: сначала SQLite, при его отсутствии — легаси index.json рядом
// (после первого Save старый файл можно удалить).
func loadPrev(dbPath string) (*Index, error) {
	if exists(dbPath) {
		return loadStore(dbPath)
	}
	if jp := legacyJSONPath(dbPath); exists(jp) {
		return loadJSON(jp)
	}
	return nil, fmt.Errorf("no previous index at %s", dbPath)
}

// buildModel строит BM25-статистики и TF-IDF векторы по корпусу чанков.
// Текст чанка: symbol_name x3 (взвешивание повторением) + signature + doc + content.
func (ix *Index) buildModel() {
	n := len(ix.Chunks)
	ix.tf = make([]map[string]int, n)
	ix.dlen = make([]int, n)
	ix.df = map[string]int{}
	ix.bySym = map[string][]int{}
	ix.byID = map[string]int{}
	for i, c := range ix.Chunks {
		var toks []string
		sym := Tokenize(c.SymbolName)
		for r := 0; r < 3; r++ { // symbol_name вес x3
			toks = append(toks, sym...)
		}
		path := Tokenize(c.FilePath)
		for r := 0; r < 2; r++ { // сегменты пути (web/api/client) вес x2
			toks = append(toks, path...)
		}
		toks = append(toks, Tokenize(c.Signature)...)
		toks = append(toks, Tokenize(c.Doc)...)
		toks = append(toks, Tokenize(c.Content)...)
		tf := map[string]int{}
		for _, t := range toks {
			tf[t]++
		}
		ix.tf[i] = tf
		ix.dlen[i] = len(toks)
		for t := range tf {
			ix.df[t]++
		}
		ix.bySym[strings.ToLower(c.SymbolName)] = append(ix.bySym[strings.ToLower(c.SymbolName)], i)
		ix.byID[c.ID] = i
	}
	var total int
	for _, l := range ix.dlen {
		total += l
	}
	if n > 0 {
		ix.avgdl = float64(total) / float64(n)
	}
	ix.idf = map[string]float64{}
	N := float64(n)
	for t, df := range ix.df {
		ix.idf[t] = math.Log(1+(N-float64(df)+0.5)/(float64(df)+0.5))
	}
	ix.vecs = make([]map[string]float64, n)
	for i := 0; i < n; i++ {
		v := map[string]float64{}
		var norm float64
		for t, f := range ix.tf[i] {
			w := float64(f) * ix.idf[t]
			v[t] = w
			norm += w * w
		}
		norm = math.Sqrt(norm)
		if norm > 0 {
			for t := range v {
				v[t] /= norm
			}
		}
		ix.vecs[i] = v
	}
}

// FindSymbol возвращает чанки с точным (case-insensitive) именем символа.
func (ix *Index) FindSymbol(name string) []chunk.Chunk {
	var out []chunk.Chunk
	for _, i := range ix.bySym[strings.ToLower(name)] {
		out = append(out, ix.Chunks[i])
	}
	return out
}

// ReadSpan читает точные строки файла (1-based, включительно).
func ReadSpan(root, rel string, start, end int) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", fmt.Errorf("invalid span %d-%d (file has %d lines)", start, end, len(lines))
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}
