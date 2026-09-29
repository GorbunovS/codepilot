"""Модуль session_cleanup: удаляет просроченные токены."""

from dataclasses import dataclass


@dataclass
class SessionCleanupConfig:
    """Конфигурация обработки (сессия)."""

    enabled: bool = True
    limit: int = 100


def run_session_cleanup(config: SessionCleanupConfig) -> dict:
    """Основной цикл: удаляет просроченные токены.

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


def validate_session_cleanup(config: SessionCleanupConfig) -> bool:
    """Проверяет конфигурацию (сессия)."""
    return config.limit > 0
