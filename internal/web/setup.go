package web

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// setupState — фоновая первоначальная настройка (модели + Postgres).
type setupState struct {
	mu      sync.Mutex
	Running bool     // идёт ли сейчас настройка
	Phase   string   // человекочитаемый статус
	Err     string   // ошибка последнего прогона
	Log     []string // последние строки лога
}

const maxSetupLogLines = 300

func (st *setupState) append(line string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.Log) >= maxSetupLogLines {
		st.Log = st.Log[len(st.Log)-maxSetupLogLines+1:]
	}
	st.Log = append(st.Log, line)
}

func (st *setupState) snapshot() (bool, string, string, []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.Running, st.Phase, st.Err, append([]string(nil), st.Log...)
}

func (st *setupState) set(phase, err string, running bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Phase = phase
	if err != "" {
		st.Err = err
	} else if !running {
		st.Err = ""
	}
	st.Running = running
}

func pgReady() bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:5432", time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func dockerAvailable() bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func modelsReady(modelsDir string) map[string]bool {
	d := modelsDir
	if d == "" {
		d = defaultModelsDir()
	}
	return map[string]bool{
		"e5":  fileExists(filepath.Join(d, "e5-small", "model.onnx")),
		"laya": fileExists(filepath.Join(d, "laya-multilingual", "laya.onnx")),
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func defaultModelsDir() string {
	if v := os.Getenv("CODEPILOT_MODELS_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		return filepath.Join(home, ".codepilot", "models")
	}
	return "models"
}

func (s *server) setupLogPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "setup.log"
	}
	return filepath.Join(home, ".codepilot", "setup.log")
}

func (s *server) findScript(name string) string {
	// 1. Явный каталог скриптов (флаг --scripts-dir).
	if s.opts.ScriptsDir != "" {
		p := filepath.Join(s.opts.ScriptsDir, name)
		if fileExists(p) {
			return p
		}
	}
	// 2. Рядом с бинарем: в dev это ROOT/scripts, в .app Resources/scripts.
	if s.opts.BinPath != "" {
		candidates := []string{
			filepath.Join(filepath.Dir(s.opts.BinPath), "..", "scripts", name),
			filepath.Join(filepath.Dir(s.opts.BinPath), "scripts", name),
		}
		for _, p := range candidates {
			if fileExists(p) {
				return p
			}
		}
	}
	// 3. От cwd.
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, "scripts", name)
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func (s *server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	mods := modelsReady(s.opts.EmbedDir)
	if !mods["e5"] && s.opts.EmbedDir == "" {
		mods = modelsReady(defaultModelsDir())
	}
	running, phase, err, log := s.setup.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"store":            s.opts.Store,
		"pg_ready":         pgReady(),
		"docker_available": dockerAvailable(),
		"models_dir":       s.opts.EmbedDir,
		"models_ready":     mods,
		"ready":            pgReady() && mods["e5"] && mods["laya"],
		"running":          running,
		"phase":            phase,
		"error":            err,
		"log":              log,
	})
}

func (s *server) handleSetupPostgres(w http.ResponseWriter, _ *http.Request) {
	if s.setup.Running {
		writeErr(w, http.StatusConflict, fmt.Errorf("настройка уже идёт"))
		return
	}
	script := s.findScript("setup_postgres.py")
	if script == "" {
		writeErr(w, http.StatusNotFound, fmt.Errorf("setup_postgres.py не найден"))
		return
	}
	go s.runSetup(script, "Postgres", "Запускаем базу данных")
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *server) handleSetupModels(w http.ResponseWriter, _ *http.Request) {
	if s.setup.Running {
		writeErr(w, http.StatusConflict, fmt.Errorf("настройка уже идёт"))
		return
	}
	script := s.findScript("setup_models.py")
	if script == "" {
		writeErr(w, http.StatusNotFound, fmt.Errorf("setup_models.py не найден"))
		return
	}
	go s.runSetup(script, "модели", "Скачиваем модели")
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *server) handleSetupLog(w http.ResponseWriter, _ *http.Request) {
	_, _, _, log := s.setup.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"log": log})
}

// runSetup запускает Python-скрипт настройки в фоне и собирает вывод.
func (s *server) runSetup(script, name, phase string) {
	s.setup.set(phase, "", true)
	s.setup.Log = nil
	logPath := s.setupLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		s.setup.set(fmt.Sprintf("Ошибка лога: %v", err), err.Error(), false)
		return
	}
	defer logFile.Close()

	s.setup.append(fmt.Sprintf("=== начало настройки %s ===", name))
	env := os.Environ()
	if s.opts.EmbedDir != "" {
		env = append(env, "CODEPILOT_MODELS_DIR="+s.opts.EmbedDir)
	}
	cmd := exec.Command("python3", script)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.setup.set("Ошибка запуска", err.Error(), false)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.setup.set("Ошибка запуска", err.Error(), false)
		return
	}
	if err := cmd.Start(); err != nil {
		s.setup.set("Ошибка запуска", err.Error(), false)
		return
	}

	capture := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			s.setup.append(line)
			_, _ = logFile.WriteString(line + "\n")
		}
	}
	go capture(stdout)
	go capture(stderr)

	if err := cmd.Wait(); err != nil {
		s.setup.set(fmt.Sprintf("%s: ошибка", name), err.Error(), false)
		return
	}
	s.setup.set("Готовы к работе", "", false)
}
