// UserProfile — обновляет профиль пользователя.
// Utility module used by the web dashboard.

/**
 * runUserProfile выполняет обработку (профиль).
 * @param {enabled: boolean, limit: number} config
 */
export function runUserProfile(config) {
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
 * validateUserProfile проверяет конфигурацию перед запуском.
 */
export function validateUserProfile(config) {
  return config.limit > 0;
}
