// CartMerge — сливает корзины после логина.
// Utility module used by the web dashboard.

/**
 * runCartMerge выполняет обработку (корзина).
 * @param {enabled: boolean, limit: number} config
 */
export function runCartMerge(config) {
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
 * validateCartMerge проверяет конфигурацию перед запуском.
 */
export function validateCartMerge(config) {
  return config.limit > 0;
}
