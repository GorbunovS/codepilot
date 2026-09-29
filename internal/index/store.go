package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codepilot/internal/chunk"

	_ "modernc.org/sqlite" // pure-Go драйвер, CGO не нужен (Windows/macOS/Linux)
)

// schema — DDL хранилища индекса. Манифест и версия чанкера лежат в meta,
// чанки — плоской таблицей; поисковые модели (BM25/TF-IDF) строятся в памяти.
const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT
);
CREATE TABLE IF NOT EXISTS chunks (
	id          TEXT PRIMARY KEY,
	file_path   TEXT NOT NULL,
	language    TEXT,
	start_line  INTEGER,
	end_line    INTEGER,
	symbol_name TEXT,
	kind        TEXT,
	signature   TEXT,
	doc         TEXT,
	content     TEXT,
	hash        TEXT
);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_path);
`

// openStore открывает (и при необходимости создаёт) БД индекса.
// WAL + busy_timeout: post-commit hook и MCP-сервер могут работать одновременно.
func openStore(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite schema: %w", err)
	}
	return db, nil
}

// saveStore атомарно перезаписывает содержимое БД текущим состоянием индекса.
func (ix *Index) saveStore(path string) error {
	db, err := openStore(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM chunks"); err != nil {
		return err
	}
	ins, err := tx.Prepare(`INSERT INTO chunks
		(id, file_path, language, start_line, end_line, symbol_name, kind, signature, doc, content, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, c := range ix.Chunks {
		if _, err := ins.Exec(c.ID, c.FilePath, c.Language, c.StartLine, c.EndLine,
			c.SymbolName, c.Kind, c.Signature, c.Doc, c.Content, c.Hash); err != nil {
			return err
		}
	}
	manifest, err := json.Marshal(ix.Manifest)
	if err != nil {
		return err
	}
	meta := map[string]string{
		"project_root":    ix.ProjectRoot,
		"chunker_version": fmt.Sprint(ix.ChunkerVersion),
		"manifest":        string(manifest),
	}
	for k, v := range meta {
		if _, err := tx.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES (?,?)", k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadStore читает индекс из SQLite-БД.
func loadStore(path string) (*Index, error) {
	db, err := openStore(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ix := &Index{Manifest: map[string]string{}}
	rows, err := db.Query("SELECT key, value FROM meta")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		switch k {
		case "project_root":
			ix.ProjectRoot = v
		case "chunker_version":
			fmt.Sscan(v, &ix.ChunkerVersion)
		case "manifest":
			_ = json.Unmarshal([]byte(v), &ix.Manifest)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := db.Query(`SELECT id, file_path, language, start_line, end_line,
		symbol_name, kind, signature, doc, content, hash FROM chunks`)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var c chunk.Chunk
		if err := crows.Scan(&c.ID, &c.FilePath, &c.Language, &c.StartLine, &c.EndLine,
			&c.SymbolName, &c.Kind, &c.Signature, &c.Doc, &c.Content, &c.Hash); err != nil {
			return nil, err
		}
		ix.Chunks = append(ix.Chunks, c)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	if ix.Manifest == nil {
		ix.Manifest = map[string]string{}
	}
	ix.buildModel()
	return ix, nil
}

// loadJSON — легаси-загрузка индекса из JSON-файла (до миграции на SQLite).
func loadJSON(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, err
	}
	if ix.Manifest == nil {
		ix.Manifest = map[string]string{}
	}
	ix.buildModel()
	return &ix, nil
}

// exists сообщает, есть ли на диске непустой файл.
func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

// legacyJSONPath — путь к JSON-индексу старого формата рядом с БД.
func legacyJSONPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "index.json")
}

// isJSONPath определяет формат индекса по расширению пути.
func isJSONPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}
