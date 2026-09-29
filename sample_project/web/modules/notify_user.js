// NotifyUser — шлёт уведомление пользователю.
// Utility module used by the web dashboard.

/**
 * runNotifyUser выполняет обработку (уведомление).
 * @param {enabled: boolean, limit: number} config
 */
export function runNotifyUser(config) {
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
 * validateNotifyUser проверяет конфигурацию перед запуском.
 */
export function validateNotifyUser(config) {
  return config.limit > 0;
}
