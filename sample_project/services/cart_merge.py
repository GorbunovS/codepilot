"""Модуль cart_merge: сливает корзины после логина."""

from dataclasses import dataclass


@dataclass
class CartMergeConfig:
    """Конфигурация обработки (корзина)."""

    enabled: bool = True
    limit: int = 100


def run_cart_merge(config: CartMergeConfig) -> dict:
    """Основной цикл: сливает корзины после логина.

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


def validate_cart_merge(config: CartMergeConfig) -> bool:
    """Проверяет конфигурацию (корзина)."""
    return config.limit > 0
