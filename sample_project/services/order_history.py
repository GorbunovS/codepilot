"""Модуль order_history: возвращает прошлые заказы."""

from dataclasses import dataclass


@dataclass
class OrderHistoryConfig:
    """Конфигурация обработки (история заказов)."""

    enabled: bool = True
    limit: int = 100


def run_order_history(config: OrderHistoryConfig) -> dict:
    """Основной цикл: возвращает прошлые заказы.

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


def validate_order_history(config: OrderHistoryConfig) -> bool:
    """Проверяет конфигурацию (история заказов)."""
    return config.limit > 0
