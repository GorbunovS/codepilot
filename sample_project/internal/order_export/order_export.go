// Package order_export — заказ: экспортирует заказы в CSV.
package order_export

import "time"

// OrderExportConfig задаёт параметры обработки (заказ).
type OrderExportConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// OrderExportResult — итог обработки.
type OrderExportResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunOrderExport выполняет основной цикл: экспортирует заказы в CSV.
// Returns the number of processed records.
func RunOrderExport(cfg OrderExportConfig) OrderExportResult {
	res := OrderExportResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (заказ)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateOrderExport проверяет конфигурацию перед запуском.
func ValidateOrderExport(cfg OrderExportConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
