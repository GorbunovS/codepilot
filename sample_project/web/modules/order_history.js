// OrderHistory — возвращает прошлые заказы.
// Utility module used by the web dashboard.

/**
 * runOrderHistory выполняет обработку (история заказов).
 * @param {enabled: boolean, limit: number} config
 */
export function runOrderHistory(config) {
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
 * validateOrderHistory проверяет конфигурацию перед запуском.
 */
export function validateOrderHistory(config) {
  return config.limit > 0;
}
