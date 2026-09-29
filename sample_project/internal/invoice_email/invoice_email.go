// Package invoice_email — счёт: отправляет счёт клиенту по email.
package invoice_email

import "time"

// InvoiceEmailConfig задаёт параметры обработки (счёт).
type InvoiceEmailConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// InvoiceEmailResult — итог обработки.
type InvoiceEmailResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunInvoiceEmail выполняет основной цикл: отправляет счёт клиенту по email.
// Returns the number of processed records.
func RunInvoiceEmail(cfg InvoiceEmailConfig) InvoiceEmailResult {
	res := InvoiceEmailResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (счёт)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateInvoiceEmail проверяет конфигурацию перед запуском.
func ValidateInvoiceEmail(cfg InvoiceEmailConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
