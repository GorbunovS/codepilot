// VatReport — строит отчёт по НДС за период.
// Utility module used by the web dashboard.

/**
 * runVatReport выполняет обработку (НДС).
 * @param {enabled: boolean, limit: number} config
 */
export function runVatReport(config) {
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
 * validateVatReport проверяет конфигурацию перед запуском.
 */
export function validateVatReport(config) {
  return config.limit > 0;
}
