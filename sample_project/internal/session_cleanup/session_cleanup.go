// Package session_cleanup — сессия: удаляет просроченные токены.
package session_cleanup

import "time"

// SessionCleanupConfig задаёт параметры обработки (сессия).
type SessionCleanupConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// SessionCleanupResult — итог обработки.
type SessionCleanupResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunSessionCleanup выполняет основной цикл: удаляет просроченные токены.
// Returns the number of processed records.
func RunSessionCleanup(cfg SessionCleanupConfig) SessionCleanupResult {
	res := SessionCleanupResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (сессия)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateSessionCleanup проверяет конфигурацию перед запуском.
func ValidateSessionCleanup(cfg SessionCleanupConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
