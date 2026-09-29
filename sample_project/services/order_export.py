"""Модуль order_export: экспортирует заказы в CSV."""

from dataclasses import dataclass


@dataclass
class OrderExportConfig:
    """Конфигурация обработки (заказ)."""

    enabled: bool = True
    limit: int = 100


def run_order_export(config: OrderExportConfig) -> dict:
    """Основной цикл: экспортирует заказы в CSV.

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


def validate_order_export(config: OrderExportConfig) -> bool:
    """Проверяет конфигурацию (заказ)."""
    return config.limit > 0
