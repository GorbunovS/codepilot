// Package stock_check — остатки: проверяет остатки товара.
package stock_check

import "time"

// StockCheckConfig задаёт параметры обработки (остатки).
type StockCheckConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// StockCheckResult — итог обработки.
type StockCheckResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunStockCheck выполняет основной цикл: проверяет остатки товара.
// Returns the number of processed records.
func RunStockCheck(cfg StockCheckConfig) StockCheckResult {
	res := StockCheckResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (остатки)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateStockCheck проверяет конфигурацию перед запуском.
func ValidateStockCheck(cfg StockCheckConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
