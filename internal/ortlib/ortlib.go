// Package ortlib ищет нативную библиотеку onnxruntime для pure-Go биндинга.
// Общий хелпер для Laya (internal/laya) и эмбеддера (internal/embed).
package ortlib

import (
	"os"
	"path/filepath"
	"runtime"
)

// EnvVar — переменная окружения с явным путём к библиотеке.
// Имя историческое (DLL), но принимает путь к .dll/.dylib/.so.
const EnvVar = "CODEPILOT_ONNXRUNTIME_DLL"

// names — кандидаты имён библиотеки по платформе.
func names() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"onnxruntime.dll"}
	case "darwin":
		return []string{"libonnxruntime.dylib", "onnxruntime.dylib"}
	default:
		return []string{"libonnxruntime.so", "onnxruntime.so"}
	}
}

// Find ищет библиотеку: $CODEPILOT_ONNXRUNTIME_DLL, затем hintDir, bin/,
// текущий каталог и системные lib-пути. Пустая строка — не найдена.
func Find(hintDir string) string {
	if p := os.Getenv(EnvVar); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	dirs := []string{hintDir, "bin", ".", "/usr/local/lib", "/usr/lib"}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for _, n := range names() {
			p := filepath.Join(d, n)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
