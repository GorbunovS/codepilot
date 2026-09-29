// PaymentRetry — повторяет неудачную оплату.
// Utility module used by the web dashboard.

/**
 * runPaymentRetry выполняет обработку (оплата).
 * @param {enabled: boolean, limit: number} config
 */
export function runPaymentRetry(config) {
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
 * validatePaymentRetry проверяет конфигурацию перед запуском.
 */
export function validatePaymentRetry(config) {
  return config.limit > 0;
}
