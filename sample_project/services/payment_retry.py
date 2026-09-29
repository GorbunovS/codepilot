"""Модуль payment_retry: повторяет неудачную оплату."""

from dataclasses import dataclass


@dataclass
class PaymentRetryConfig:
    """Конфигурация обработки (оплата)."""

    enabled: bool = True
    limit: int = 100


def run_payment_retry(config: PaymentRetryConfig) -> dict:
    """Основной цикл: повторяет неудачную оплату.

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


def validate_payment_retry(config: PaymentRetryConfig) -> bool:
    """Проверяет конфигурацию (оплата)."""
    return config.limit > 0
