// Package token_refresh — токен: обновляет токен доступа.
package token_refresh

import "time"

// TokenRefreshConfig задаёт параметры обработки (токен).
type TokenRefreshConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// TokenRefreshResult — итог обработки.
type TokenRefreshResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunTokenRefresh выполняет основной цикл: обновляет токен доступа.
// Returns the number of processed records.
func RunTokenRefresh(cfg TokenRefreshConfig) TokenRefreshResult {
	res := TokenRefreshResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (токен)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateTokenRefresh проверяет конфигурацию перед запуском.
func ValidateTokenRefresh(cfg TokenRefreshConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
