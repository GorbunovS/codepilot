"""Модуль vat_report: строит отчёт по НДС за период."""

from dataclasses import dataclass


@dataclass
class VatReportConfig:
    """Конфигурация обработки (НДС)."""

    enabled: bool = True
    limit: int = 100


def run_vat_report(config: VatReportConfig) -> dict:
    """Основной цикл: строит отчёт по НДС за период.

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


def validate_vat_report(config: VatReportConfig) -> bool:
    """Проверяет конфигурацию (НДС)."""
    return config.limit > 0
