package config

import (
	"path/filepath"
	"testing"
)

func testProjects() []Project {
	return []Project{
		{Name: "backend", Path: filepath.Join("srv", "back")},
		{Name: "frontend", Path: filepath.Join("srv", "front")},
	}
}

// TestFind — маршрутизация project-аргумента: точное имя, регистр, путь,
// дефолт и пустой запрос.
func TestFind(t *testing.T) {
	list := testProjects()

	if p, ok := Find(list, "backend", ""); !ok || p.Name != "backend" {
		t.Fatalf("точное имя: %v %v", p, ok)
	}
	if p, ok := Find(list, "BACKEND", ""); !ok || p.Name != "backend" {
		t.Fatalf("регистронезависимое имя: %v %v", p, ok)
	}
	// Пустой запрос → дефолт.
	if p, ok := Find(list, "", "frontend"); !ok || p.Name != "frontend" {
		t.Fatalf("дефолт: %v %v", p, ok)
	}
	// Пустой запрос без дефолта → первый проект.
	if p, ok := Find(list, "", ""); !ok || p.Name != "backend" {
		t.Fatalf("первый проект: %v %v", p, ok)
	}
	// Дефолт ссылается на отсутствующий проект → первый.
	if p, ok := Find(list, "", "ghost"); !ok || p.Name != "backend" {
		t.Fatalf("битый дефолт: %v %v", p, ok)
	}
	// Неизвестное имя — не находим.
	if _, ok := Find(list, "gamma", ""); ok {
		t.Fatal("gamma не должна находиться")
	}
	// Пустой реестр.
	if _, ok := Find(nil, "", ""); ok {
		t.Fatal("пустой реестр не должен находить")
	}
}

// TestFindByPath — проект находится по абсолютному пути.
func TestFindByPath(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("srv", "front"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := Find(testProjects(), abs, "")
	if !ok || p.Name != "frontend" {
		t.Fatalf("поиск по пути: %v %v", p, ok)
	}
}

func TestDefaultName(t *testing.T) {
	if got := DefaultName(`C:\Users\me\pnodes`); got != "pnodes" {
		t.Fatalf("DefaultName windows: %q", got)
	}
	if got := DefaultName("/srv/front/"); got != "front" {
		t.Fatalf("DefaultName trailing slash: %q", got)
	}
	if got := DefaultName(""); got != "project" {
		t.Fatalf("DefaultName пустой: %q", got)
	}
}
