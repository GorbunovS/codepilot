// Package vat_report — НДС: строит отчёт по НДС за период.
package vat_report

import "time"

// VatReportConfig задаёт параметры обработки (НДС).
type VatReportConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// VatReportResult — итог обработки.
type VatReportResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunVatReport выполняет основной цикл: строит отчёт по НДС за период.
// Returns the number of processed records.
func RunVatReport(cfg VatReportConfig) VatReportResult {
	res := VatReportResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (НДС)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateVatReport проверяет конфигурацию перед запуском.
func ValidateVatReport(cfg VatReportConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
