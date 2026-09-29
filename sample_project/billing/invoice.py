"""Модуль выставления счетов (billing).

Generates invoices for orders and applies VAT (НДС).
"""

from dataclasses import dataclass, field
from datetime import datetime

VAT_RATE = 0.20  # ставка НДС 20%


@dataclass
class InvoiceLine:
    """Строка счёта: описание и сумма."""

    description: str
    amount: float


@dataclass
class Invoice:
    """Счёт на оплату для клиента."""

    number: str
    customer_id: str
    lines: list[InvoiceLine] = field(default_factory=list)
    created_at: datetime = field(default_factory=datetime.utcnow)

    @property
    def subtotal(self) -> float:
        """Сумма без НДС."""
        return sum(line.amount for line in self.lines)


def apply_vat(amount: float, rate: float = VAT_RATE) -> float:
    """Добавляет НДС к сумме и возвращает итог.

    Args:
        amount: сумма без налога
        rate: налоговая ставка (по умолчанию 20%)
    """
    return round(amount * (1 + rate), 2)


def generate_invoice(number: str, customer_id: str, order_total: float) -> Invoice:
    """Создаёт счёт по итоговой сумме заказа.

    The invoice contains a single line with the order total
    plus a VAT line computed via apply_vat.
    """
    inv = Invoice(number=number, customer_id=customer_id)
    inv.lines.append(InvoiceLine(description="Order total", amount=order_total))
    vat = apply_vat(order_total) - order_total
    inv.lines.append(InvoiceLine(description="НДС 20%", amount=round(vat, 2)))
    return inv
