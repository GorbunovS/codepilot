// Package config — общий конфиг и реестр проектов codepilot
// (~/.codepilot/config.json и ~/.codepilot/projects.json).
//
// Конфиг пишет панель (web) из эффективных флагов; serve без флагов читает
// его и работает мультипроектным демоном: агент передаёт только имя проекта
// в аргументах инструментов, не зная ни путей, ни DSN, ни устройств.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config — настройки демона. Нулевые значения = «не задано», применяются
// дефолты/флаги вызывающей стороны.
type Config struct {
	Store       string `json:"store,omitempty"`        // sqlite | pg
	PGDSN       string `json:"pg_dsn,omitempty"`       // DSN Postgres (режим pg)
	Embed       string `json:"embed,omitempty"`        // onnx | "" (без эмбеддингов)
	EmbedDir    string `json:"embed_dir,omitempty"`    // каталог ONNX-модели e5
	EmbedServer string `json:"embed_server,omitempty"` // URL MLX-сайдкара
	Laya        string `json:"laya,omitempty"`         // onnx | heuristic
	LayaDir     string `json:"laya_dir,omitempty"`     // каталог ONNX-модели Laya
	Device      string `json:"device,omitempty"`       // cpu | directml | cuda | coreml
	MaxThreads  int    `json:"max_threads,omitempty"`  // лимит потоков инференса (0 — все ядра)
	Default     string `json:"default_project"`        // имя или путь проекта по умолчанию
}

// Project — проект в реестре. Name — короткий идентификатор для агента
// (по умолчанию basename пути: «backend», «frontend»).
type Project struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	AddedAt string `json:"added_at,omitempty"`
}

// Dir — каталог конфигурации (~/.codepilot), создаётся при необходимости.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".codepilot")
	return d, os.MkdirAll(d, 0o755)
}

// Load читает config.json; отсутствие файла — не ошибка (нулевой Config).
func Load() (Config, error) {
	var c Config
	d, err := Dir()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(filepath.Join(d, "config.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

// Save атомарно записывает config.json.
func Save(c Config) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(d, "config.json")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ProjectsPath — путь к реестру проектов.
func ProjectsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "projects.json"), nil
}

// LoadProjects читает реестр; проектам без имени проставляется basename пути.
// Отсутствие файла — пустой список без ошибки.
func LoadProjects() ([]Project, error) {
	p, err := ProjectsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []Project
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == "" {
			list[i].Name = DefaultName(list[i].Path)
		}
	}
	return list, nil
}

// SaveProjects атомарно записывает реестр.
func SaveProjects(list []Project) error {
	p, err := ProjectsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DefaultName — имя проекта по умолчанию: basename нормализованного пути.
func DefaultName(path string) string {
	norm := strings.TrimRight(filepath.ToSlash(path), "/")
	if norm == "" {
		return "project"
	}
	return filepath.Base(norm)
}

// Find ищет проект по имени (точное, затем без регистра) или по пути.
// nameOrPath == "" возвращает def (имя/путь дефолтного), а при пустом def —
// первый проект списка. ok == false, если ничего не подошло.
func Find(list []Project, nameOrPath, def string) (Project, bool) {
	want := strings.TrimSpace(nameOrPath)
	if want == "" {
		if def != "" {
			if p, ok := Find(list, def, ""); ok {
				return p, true
			}
		}
		if len(list) > 0 {
			return list[0], true
		}
		return Project{}, false
	}
	for _, p := range list {
		if p.Name == want {
			return p, true
		}
	}
	for _, p := range list {
		if strings.EqualFold(p.Name, want) {
			return p, true
		}
	}
	if abs, err := filepath.Abs(want); err == nil {
		for _, p := range list {
			if pp, err := filepath.Abs(p.Path); err == nil && pp == abs {
				return p, true
			}
		}
	}
	for _, p := range list {
		if p.Path == want {
			return p, true
		}
	}
	return Project{}, false
}
