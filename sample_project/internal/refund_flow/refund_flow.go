// Package refund_flow — возврат: оформляет возврат по заказу.
package refund_flow

import "time"

// RefundFlowConfig задаёт параметры обработки (возврат).
type RefundFlowConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// RefundFlowResult — итог обработки.
type RefundFlowResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunRefundFlow выполняет основной цикл: оформляет возврат по заказу.
// Returns the number of processed records.
func RunRefundFlow(cfg RefundFlowConfig) RefundFlowResult {
	res := RefundFlowResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (возврат)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateRefundFlow проверяет конфигурацию перед запуском.
func ValidateRefundFlow(cfg RefundFlowConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
