// Package audit_log — аудит: пишет события в журнал аудита.
package audit_log

import "time"

// AuditLogConfig задаёт параметры обработки (аудит).
type AuditLogConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// AuditLogResult — итог обработки.
type AuditLogResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunAuditLog выполняет основной цикл: пишет события в журнал аудита.
// Returns the number of processed records.
func RunAuditLog(cfg AuditLogConfig) AuditLogResult {
	res := AuditLogResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (аудит)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateAuditLog проверяет конфигурацию перед запуском.
func ValidateAuditLog(cfg AuditLogConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
