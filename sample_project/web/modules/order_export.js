// OrderExport — экспортирует заказы в CSV.
// Utility module used by the web dashboard.

/**
 * runOrderExport выполняет обработку (заказ).
 * @param {enabled: boolean, limit: number} config
 */
export function runOrderExport(config) {
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
 * validateOrderExport проверяет конфигурацию перед запуском.
 */
export function validateOrderExport(config) {
  return config.limit > 0;
}
