"""Модуль invoice_email: отправляет счёт клиенту по email."""

from dataclasses import dataclass


@dataclass
class InvoiceEmailConfig:
    """Конфигурация обработки (счёт)."""

    enabled: bool = True
    limit: int = 100


def run_invoice_email(config: InvoiceEmailConfig) -> dict:
    """Основной цикл: отправляет счёт клиенту по email.

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


def validate_invoice_email(config: InvoiceEmailConfig) -> bool:
    """Проверяет конфигурацию (счёт)."""
    return config.limit > 0
