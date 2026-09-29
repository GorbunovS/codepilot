// TokenRefresh — обновляет токен доступа.
// Utility module used by the web dashboard.

/**
 * runTokenRefresh выполняет обработку (токен).
 * @param {enabled: boolean, limit: number} config
 */
export function runTokenRefresh(config) {
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
 * validateTokenRefresh проверяет конфигурацию перед запуском.
 */
export function validateTokenRefresh(config) {
  return config.limit > 0;
}
