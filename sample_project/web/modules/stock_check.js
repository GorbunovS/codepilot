// StockCheck — проверяет остатки товара.
// Utility module used by the web dashboard.

/**
 * runStockCheck выполняет обработку (остатки).
 * @param {enabled: boolean, limit: number} config
 */
export function runStockCheck(config) {
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
 * validateStockCheck проверяет конфигурацию перед запуском.
 */
export function validateStockCheck(config) {
  return config.limit > 0;
}
