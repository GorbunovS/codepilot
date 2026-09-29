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
