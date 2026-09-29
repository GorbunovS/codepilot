// InvoiceEmail — отправляет счёт клиенту по email.
// Utility module used by the web dashboard.

/**
 * runInvoiceEmail выполняет обработку (счёт).
 * @param {enabled: boolean, limit: number} config
 */
export function runInvoiceEmail(config) {
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
 * validateInvoiceEmail проверяет конфигурацию перед запуском.
 */
export function validateInvoiceEmail(config) {
  return config.limit > 0;
}
