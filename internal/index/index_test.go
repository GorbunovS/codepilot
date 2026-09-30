package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIncrementalIndex — изменение файла переиндексирует только его,
// повторный запуск на неизменном репозитории — no-op.
func TestIncrementalIndex(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n\n// A первая.\nfunc A() {}\n")
	write("b.go", "package b\n\n// B вторая.\nfunc B() {}\n")

	ix, st1, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Reindexed != 2 {
		t.Fatalf("первая индексация: reindexed=%d, ожидалось 2", st1.Reindexed)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}

	_, st2, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Reindexed != 0 || st2.Removed != 0 {
		t.Fatalf("повторная индексация должна быть no-op: %+v", st2)
	}

	write("b.go", "package b\n\n// B2 изменена.\nfunc B2() {}\n")
	ix3, st3, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st3.Reindexed != 1 || st3.Kept != 1 {
		t.Fatalf("ожидалось reindexed=1 kept=1, получено %+v", st3)
	}
	found := false
	for _, c := range ix3.Chunks {
		if c.SymbolName == "B2" {
			found = true
		}
	}
	if !found {
		t.Errorf("новый символ B2 не попал в индекс")
	}
}

// TestReadSpan — точные строки файла (1-based, включительно).
func TestReadSpan(t *testing.T) {
	dir := t.TempDir()
	content := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSpan(dir, "f.go", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "two\nthree" {
		t.Errorf("ReadSpan(2,3) = %q, ожидалось %q", got, "two\\nthree")
	}
	// кламп границ
	got, err = ReadSpan(dir, "f.go", -5, 99)
	if err != nil {
		t.Fatal(err)
	}
	if got != content[:len(content)-1] && got != content {
		t.Errorf("ReadSpan clamp: %q", got)
	}
}

// TestSourceFilesSkips — walker пропускает служебные каталоги (.venv, build,
// node_modules и т.п.) и файлы крупнее maxFileBytes.
func TestSourceFilesSkips(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", 64)
	write(".venv/lib/site-packages/torch/big.py", 64)
	write("build/out/app.js", 64)
	write("node_modules/dep/index.js", 64)
	write("minified.js", maxFileBytes+1)

	files, err := SourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("SourceFiles = %v, ожидался только main.go", files)
	}
}

// TestCodepilotIgnore — .codepilotignore исключает каталоги от корня,
// имена на любом уровне и glob по имени файла.
func TestCodepilotIgnore(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go")
	write("sample_project/demo.go")
	write("docs/internal/notes.go")
	write("fixtures/data.go")
	write("web/app.min.js")
	if err := os.WriteFile(filepath.Join(dir, IgnoreFileName), []byte(
		"# демо-репозиторий\nsample_project/\ndocs/internal\nfixtures\n*.min.js\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := SourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	// web/app.min.js выкинут glob'ом, .codepilotignore не индексируется
	// (нет чанкера) — остаётся только main.go
	if len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("SourceFiles = %v, ожидался только main.go", files)
	}
}
