// Package price_rules — цена: пересчитывает цены по правилам.
package price_rules

import "time"

// PriceRulesConfig задаёт параметры обработки (цена).
type PriceRulesConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// PriceRulesResult — итог обработки.
type PriceRulesResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunPriceRules выполняет основной цикл: пересчитывает цены по правилам.
// Returns the number of processed records.
func RunPriceRules(cfg PriceRulesConfig) PriceRulesResult {
	res := PriceRulesResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (цена)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidatePriceRules проверяет конфигурацию перед запуском.
func ValidatePriceRules(cfg PriceRulesConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
