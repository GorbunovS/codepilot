// Package payment_retry — оплата: повторяет неудачную оплату.
package payment_retry

import "time"

// PaymentRetryConfig задаёт параметры обработки (оплата).
type PaymentRetryConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// PaymentRetryResult — итог обработки.
type PaymentRetryResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunPaymentRetry выполняет основной цикл: повторяет неудачную оплату.
// Returns the number of processed records.
func RunPaymentRetry(cfg PaymentRetryConfig) PaymentRetryResult {
	res := PaymentRetryResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (оплата)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidatePaymentRetry проверяет конфигурацию перед запуском.
func ValidatePaymentRetry(cfg PaymentRetryConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
