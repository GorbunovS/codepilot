// Package notify_user — уведомление: шлёт уведомление пользователю.
package notify_user

import "time"

// NotifyUserConfig задаёт параметры обработки (уведомление).
type NotifyUserConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// NotifyUserResult — итог обработки.
type NotifyUserResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunNotifyUser выполняет основной цикл: шлёт уведомление пользователю.
// Returns the number of processed records.
func RunNotifyUser(cfg NotifyUserConfig) NotifyUserResult {
	res := NotifyUserResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (уведомление)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateNotifyUser проверяет конфигурацию перед запуском.
func ValidateNotifyUser(cfg NotifyUserConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
