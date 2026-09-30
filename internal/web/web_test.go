package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codepilot/internal/chunk"
	"codepilot/internal/index"
)

// postJSON шлёт POST с JSON-телом в хендлер и возвращает рекордер.
func postJSON(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestTopCounts(t *testing.T) {
	ix := &index.Index{}
	for i := 0; i < 10; i++ {
		ix.Chunks = append(ix.Chunks, chunk.Chunk{Language: "go", Kind: "func"})
	}
	for i := 0; i < 5; i++ {
		ix.Chunks = append(ix.Chunks, chunk.Chunk{Language: "py", Kind: "class"})
	}
	ix.Chunks = append(ix.Chunks, chunk.Chunk{Language: "", Kind: ""})
	langs := topCounts(ix, func(c chunkRef) string { return c.lang }, 8)
	if len(langs) == 0 || langs[0]["name"] != "go" || langs[0]["count"] != 10 {
		t.Fatalf("топ языков: %+v, ожидался go=10 первым", langs)
	}
	// пустые значения собираются в "other"
	var other bool
	for _, l := range langs {
		if l["name"] == "other" && l["count"] == 1 {
			other = true
		}
	}
	if !other {
		t.Fatalf("пустой язык должен попасть в other: %+v", langs)
	}
	// усечение по n: 3 группы при n=1 → топ-1 + «прочее»
	one := topCounts(ix, func(c chunkRef) string { return c.lang }, 1)
	if len(one) != 2 || one[1]["name"] != "прочее" || one[1]["count"] != 6 {
		t.Fatalf("усечение: %+v, ожидался топ-1 + прочее=6", one)
	}
}

func TestSkillInstallAndStatus(t *testing.T) {
	dir := t.TempDir()
	s := &server{}

	st := s.skillStatus(dir)
	if st["installed"].(bool) || st["skills_dir"].(bool) {
		t.Fatalf("свежий каталог не должен содержать скилл: %+v", st)
	}

	w := postJSON(t, s.handleSkillInstall, `{"path": "`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	want := filepath.Join(dir, ".kimi-code", "skills", "codepilot", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("скилл не записан в %s: %v", want, err)
	}
	st = s.skillStatus(dir)
	if !st["installed"].(bool) {
		t.Fatalf("после установки installed=false: %+v", st)
	}
}

func TestSkillInstallPrefersAgentsDir(t *testing.T) {
	dir := t.TempDir()
	// в проекте уже есть .agents/skills — скилл должен лечь туда, а не в .kimi-code
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &server{}
	w := postJSON(t, s.handleSkillInstall, `{"path": "`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	want := filepath.Join(dir, ".agents", "skills", "codepilot", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("скилл должен лежать в .agents/skills: %v", err)
	}
}

func TestMCPInstallMerges(t *testing.T) {
	dir := t.TempDir()
	kd := filepath.Join(dir, ".kimi-code")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		t.Fatal(err)
	}
	// существующий конфиг с чужим сервером — не должен быть затёрт
	existing := `{"mcpServers": {"other": {"command": "x"}}, "extra": 1}`
	if err := os.WriteFile(filepath.Join(kd, "mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{opts: Options{BinPath: "codepilot", MaxThreads: 4, Device: "directml"}, addr: "127.0.0.1:8080"}
	w := postJSON(t, s.handleMCPInstall, `{"path": "`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp install: %d %s", w.Code, w.Body)
	}
	data, err := os.ReadFile(filepath.Join(kd, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conf map[string]any
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("результат не JSON: %v", err)
	}
	servers := conf["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatalf("чужой сервер затёрт: %v", servers)
	}
	cp, ok := servers["codepilot"].(map[string]any)
	if !ok {
		t.Fatalf("сервер codepilot не добавлен: %v", servers)
	}
	// HTTP-демон: сниппет — только URL панели, без command/args (агенты не
	// плодят процессы; один демон обслуживает все сессии и все проекты).
	if cp["url"] != "http://127.0.0.1:8080/mcp" {
		t.Fatalf("url демона не проброшен в сниппет: %v", cp)
	}
	if _, ok := cp["command"]; ok {
		t.Fatalf("stdio-поля не должны попадать в http-сниппет: %v", cp)
	}
	if conf["extra"].(float64) != 1 {
		t.Fatalf("посторонние ключи конфига потеряны: %v", conf)
	}
}

func TestMCPInstallRejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	kd := filepath.Join(dir, ".kimi-code")
	if err := os.MkdirAll(kd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kd, "mcp.json"), []byte("{не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{}
	w := postJSON(t, s.handleMCPInstall, `{"path": "`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("битый mcp.json: %d, ожидался 400 (не затираем чужой конфиг)", w.Code)
	}
}

func TestSearchValidation(t *testing.T) {
	s := &server{}
	w := postJSON(t, s.handleSearch, `{"path": ".", "query": ""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("пустой query: %d, ожидался 400", w.Code)
	}
	w = postJSON(t, s.handleSearch, `{"path": ".", "query": "x", "mode": "nonsense"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("неизвестный mode: %d, ожидался 400", w.Code)
	}
	// проект без индекса → 400 с понятной ошибкой, а не 500/паника
	dir := t.TempDir()
	w = postJSON(t, s.handleSearch, `{"path": "`+strings.ReplaceAll(dir, `\`, `\\`)+`", "query": "x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "индекс не найден") {
		t.Fatalf("нет индекса: %d %s", w.Code, w.Body)
	}
}

func TestDevicesAlwaysHasCPU(t *testing.T) {
	s := &server{}
	r := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	w := httptest.NewRecorder()
	s.handleDevices(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("devices: %d", w.Code)
	}
	var resp struct {
		Devices []deviceInfo `json:"devices"`
		Current string       `json:"current"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var cpu bool
	for _, d := range resp.Devices {
		if d.Value == "cpu" {
			cpu = true
		}
	}
	if !cpu {
		t.Fatalf("cpu обязан быть всегда: %+v", resp.Devices)
	}
}
