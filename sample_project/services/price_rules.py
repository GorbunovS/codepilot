"""Модуль price_rules: пересчитывает цены по правилам."""

from dataclasses import dataclass


@dataclass
class PriceRulesConfig:
    """Конфигурация обработки (цена)."""

    enabled: bool = True
    limit: int = 100


def run_price_rules(config: PriceRulesConfig) -> dict:
    """Основной цикл: пересчитывает цены по правилам.

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


def validate_price_rules(config: PriceRulesConfig) -> bool:
    """Проверяет конфигурацию (цена)."""
    return config.limit > 0
