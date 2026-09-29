"""Модуль discount_promo: применяет промокод к заказу."""

from dataclasses import dataclass


@dataclass
class DiscountPromoConfig:
    """Конфигурация обработки (скидка)."""

    enabled: bool = True
    limit: int = 100


def run_discount_promo(config: DiscountPromoConfig) -> dict:
    """Основной цикл: применяет промокод к заказу.

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


def validate_discount_promo(config: DiscountPromoConfig) -> bool:
    """Проверяет конфигурацию (скидка)."""
    return config.limit > 0
