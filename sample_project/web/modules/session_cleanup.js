// SessionCleanup — удаляет просроченные токены.
// Utility module used by the web dashboard.

/**
 * runSessionCleanup выполняет обработку (сессия).
 * @param {enabled: boolean, limit: number} config
 */
export function runSessionCleanup(config) {
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
 * validateSessionCleanup проверяет конфигурацию перед запуском.
 */
export function validateSessionCleanup(config) {
  return config.limit > 0;
}
