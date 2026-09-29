// RefundFlow — оформляет возврат по заказу.
// Utility module used by the web dashboard.

/**
 * runRefundFlow выполняет обработку (возврат).
 * @param {enabled: boolean, limit: number} config
 */
export function runRefundFlow(config) {
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
 * validateRefundFlow проверяет конфигурацию перед запуском.
 */
export function validateRefundFlow(config) {
  return config.limit > 0;
}
