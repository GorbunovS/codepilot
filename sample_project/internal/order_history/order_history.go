// Package order_history — история заказов: возвращает прошлые заказы.
package order_history

import "time"

// OrderHistoryConfig задаёт параметры обработки (история заказов).
type OrderHistoryConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// OrderHistoryResult — итог обработки.
type OrderHistoryResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunOrderHistory выполняет основной цикл: возвращает прошлые заказы.
// Returns the number of processed records.
func RunOrderHistory(cfg OrderHistoryConfig) OrderHistoryResult {
	res := OrderHistoryResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (история заказов)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateOrderHistory проверяет конфигурацию перед запуском.
func ValidateOrderHistory(cfg OrderHistoryConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
