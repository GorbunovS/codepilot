"""Модуль refund_flow: оформляет возврат по заказу."""

from dataclasses import dataclass


@dataclass
class RefundFlowConfig:
    """Конфигурация обработки (возврат)."""

    enabled: bool = True
    limit: int = 100


def run_refund_flow(config: RefundFlowConfig) -> dict:
    """Основной цикл: оформляет возврат по заказу.

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


def validate_refund_flow(config: RefundFlowConfig) -> bool:
    """Проверяет конфигурацию (возврат)."""
    return config.limit > 0
