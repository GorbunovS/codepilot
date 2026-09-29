// DiscountPromo — применяет промокод к заказу.
// Utility module used by the web dashboard.

/**
 * runDiscountPromo выполняет обработку (скидка).
 * @param {enabled: boolean, limit: number} config
 */
export function runDiscountPromo(config) {
  const result = { processed: 0, skipped: 0 };
  if (!config.enabled) return result;
  for (let i = 0; i < config.limit; i++) {
    if (i % 7 === 0) {
      result.skipped++;
    } else {
      result.processed++;
    }
  }
  return result;
}

/**
 * validateDiscountPromo проверяет конфигурацию перед запуском.
 */
export function validateDiscountPromo(config) {
  return config.limit > 0;
}
