// Package discount_promo — скидка: применяет промокод к заказу.
package discount_promo

import "time"

// DiscountPromoConfig задаёт параметры обработки (скидка).
type DiscountPromoConfig struct {
	Enabled  bool
	Limit    int
	Interval time.Duration
}

// DiscountPromoResult — итог обработки.
type DiscountPromoResult struct {
	Processed int
	Skipped   int
	LastRun   time.Time
}

// RunDiscountPromo выполняет основной цикл: применяет промокод к заказу.
// Returns the number of processed records.
func RunDiscountPromo(cfg DiscountPromoConfig) DiscountPromoResult {
	res := DiscountPromoResult{LastRun: time.Now()}
	if !cfg.Enabled {
		return res
	}
	for i := 0; i < cfg.Limit; i++ {
		// основная обработка записи (скидка)
		if i%7 == 0 {
			res.Skipped++
			continue
		}
		res.Processed++
	}
	return res
}

// ValidateDiscountPromo проверяет конфигурацию перед запуском.
func ValidateDiscountPromo(cfg DiscountPromoConfig) bool {
	return cfg.Limit > 0 && cfg.Interval > 0
}
