// PriceRules — пересчитывает цены по правилам.
// Utility module used by the web dashboard.

/**
 * runPriceRules выполняет обработку (цена).
 * @param {enabled: boolean, limit: number} config
 */
export function runPriceRules(config) {
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
 * validatePriceRules проверяет конфигурацию перед запуском.
 */
export function validatePriceRules(config) {
  return config.limit > 0;
}
