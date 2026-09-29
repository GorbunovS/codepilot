// Package user_profile — профиль: обновляет профиль пользователя.
package user_profile

import "time"

// UserProfileConfig задаёт параметры обработки (профиль).
type UserProfileConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// UserProfileResult — итог обработки.
type UserProfileResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunUserProfile выполняет основной цикл: обновляет профиль пользователя.
// Returns the number of processed records.
func RunUserProfile(cfg UserProfileConfig) UserProfileResult {
	res := UserProfileResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (профиль)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateUserProfile проверяет конфигурацию перед запуском.
func ValidateUserProfile(cfg UserProfileConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
