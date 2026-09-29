"""Модуль notify_user: шлёт уведомление пользователю."""

from dataclasses import dataclass


@dataclass
class NotifyUserConfig:
    """Конфигурация обработки (уведомление)."""

    enabled: bool = True
    limit: int = 100


def run_notify_user(config: NotifyUserConfig) -> dict:
    """Основной цикл: шлёт уведомление пользователю.

    Returns a dict with processed/skipped counters.
    """
    result = {"processed": 0, "skipped": 0}
    if not config.enabled:
        return result
    for i in range(config.limit):
        if i % 7 == 0:  # пропускаем каждую седьмую запись
            result["skipped"] += 1
        else:
            result["processed"] += 1
    return result


def validate_notify_user(config: NotifyUserConfig) -> bool:
    """Проверяет конфигурацию (уведомление)."""
    return config.limit > 0
