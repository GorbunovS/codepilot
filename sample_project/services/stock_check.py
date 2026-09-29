"""Модуль stock_check: проверяет остатки товара."""

from dataclasses import dataclass


@dataclass
class StockCheckConfig:
    """Конфигурация обработки (остатки)."""

    enabled: bool = True
    limit: int = 100


def run_stock_check(config: StockCheckConfig) -> dict:
    """Основной цикл: проверяет остатки товара.

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


def validate_stock_check(config: StockCheckConfig) -> bool:
    """Проверяет конфигурацию (остатки)."""
    return config.limit > 0
