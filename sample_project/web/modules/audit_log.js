// AuditLog — пишет события в журнал аудита.
// Utility module used by the web dashboard.

/**
 * runAuditLog выполняет обработку (аудит).
 * @param {enabled: boolean, limit: number} config
 */
export function runAuditLog(config) {
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
 * validateAuditLog проверяет конфигурацию перед запуском.
 */
export function validateAuditLog(config) {
  return config.limit > 0;
}
