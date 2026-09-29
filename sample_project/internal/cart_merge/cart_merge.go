// Package cart_merge — корзина: сливает корзины после логина.
package cart_merge

import "time"

// CartMergeConfig задаёт параметры обработки (корзина).
type CartMergeConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// CartMergeResult — итог обработки.
type CartMergeResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunCartMerge выполняет основной цикл: сливает корзины после логина.
// Returns the number of processed records.
func RunCartMerge(cfg CartMergeConfig) CartMergeResult {
	res := CartMergeResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (корзина)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateCartMerge проверяет конфигурацию перед запуском.
func ValidateCartMerge(cfg CartMergeConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
