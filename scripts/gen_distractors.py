"""Генератор правдоподобных файлов-«шума» для sample_project.

Офлайн-инструмент (не часть продукта). Создаёт ~60 файлов на 4 языках,
разделяющих доменную лексику с золотым датасетом (заказ, токен, счёт,
оплата, order, token, invoice...), чтобы bench измерял baseline на
корпусе реалистичного размера, а eval — на фоне дистракторов.
"""

import os
import random

random.seed(42)

ROOT = os.path.join(os.path.dirname(__file__), "..", "sample_project")

TOPICS = [
    # (slug, сущность EN, сущность RU, действие)
    ("session_cleanup", "SessionCleanup", "сессия", "удаляет просроченные токены"),
    ("order_export", "OrderExport", "заказ", "экспортирует заказы в CSV"),
    ("invoice_email", "InvoiceEmail", "счёт", "отправляет счёт клиенту по email"),
    ("payment_retry", "PaymentRetry", "оплата", "повторяет неудачную оплату"),
    ("discount_promo", "DiscountPromo", "скидка", "применяет промокод к заказу"),
    ("user_profile", "UserProfile", "профиль", "обновляет профиль пользователя"),
    ("token_refresh", "TokenRefresh", "токен", "обновляет токен доступа"),
    ("vat_report", "VatReport", "НДС", "строит отчёт по НДС за период"),
    ("order_history", "OrderHistory", "история заказов", "возвращает прошлые заказы"),
    ("stock_check", "StockCheck", "остатки", "проверяет остатки товара"),
    ("notify_user", "NotifyUser", "уведомление", "шлёт уведомление пользователю"),
    ("price_rules", "PriceRules", "цена", "пересчитывает цены по правилам"),
    ("cart_merge", "CartMerge", "корзина", "сливает корзины после логина"),
    ("refund_flow", "RefundFlow", "возврат", "оформляет возврат по заказу"),
    ("audit_log", "AuditLog", "аудит", "пишет события в журнал аудита"),
]

GO_TPL = '''// Package {pkg} — {ru}: {action}.
package {pkg}

import "time"

// {ent}Config задаёт параметры обработки ({ru}).
type {ent}Config struct {{
	Enabled  bool
	Limit    int
	Interval time.Duration
}}

// {ent}Result — итог обработки.
type {ent}Result struct {{
	Processed int
	Skipped   int
	LastRun   time.Time
}}

// Run{ent} выполняет основной цикл: {action}.
// Returns the number of processed records.
func Run{ent}(cfg {ent}Config) {ent}Result {{
	res := {ent}Result{{LastRun: time.Now()}}
	if !cfg.Enabled {{
		return res
	}}
	for i := 0; i < cfg.Limit; i++ {{
		// основная обработка записи ({ru})
		if i%7 == 0 {{
			res.Skipped++
			continue
		}}
		res.Processed++
	}}
	return res
}}

// Validate{ent} проверяет конфигурацию перед запуском.
func Validate{ent}(cfg {ent}Config) bool {{
	return cfg.Limit > 0 && cfg.Interval > 0
}}
'''

PY_TPL = '''"""Модуль {slug}: {action}."""

from dataclasses import dataclass


@dataclass
class {ent}Config:
    """Конфигурация обработки ({ru})."""

    enabled: bool = True
    limit: int = 100


def run_{slug}(config: {ent}Config) -> dict:
    """Основной цикл: {action}.

    Returns a dict with processed/skipped counters.
    """
    result = {{"processed": 0, "skipped": 0}}
    if not config.enabled:
        return result
    for i in range(config.limit):
        if i % 7 == 0:  # пропускаем каждую седьмую запись
            result["skipped"] += 1
        else:
            result["processed"] += 1
    return result


def validate_{slug}(config: {ent}Config) -> bool:
    """Проверяет конфигурацию ({ru})."""
    return config.limit > 0
'''

JS_TPL = '''// {ent} — {action}.
// Utility module used by the web dashboard.

/**
 * run{ent} выполняет обработку ({ru}).
 * @param {{enabled: boolean, limit: number}} config
 */
export function run{ent}(config) {{
  const result = {{ processed: 0, skipped: 0 }};
  if (!config.enabled) return result;
  for (let i = 0; i < config.limit; i++) {{
    if (i % 7 === 0) {{
      result.skipped++;
    }} else {{
      result.processed++;
    }}
  }}
  return result;
}}

/**
 * validate{ent} проверяет конфигурацию перед запуском.
 */
export function validate{ent}(config) {{
  return config.limit > 0;
}}
'''

VUE_TPL = '''<template>
  <div class="{slug}">
    <h3>{ent}</h3>
    <p>Статус: {{{{ status }}}}</p>
  </div>
</template>

<script>
// {ent}Panel — панель «{ru}»: {action}.
export default {{
  name: "{ent}Panel",
  data() {{
    return {{ processed: 0, skipped: 0 }};
  }},
  computed: {{
    // status отображает итог обработки.
    status() {{
      return `обработано: ${{this.processed}}, пропущено: ${{this.skipped}}`;
    }},
  }},
  methods: {{
    // refresh запускает обработку ({ru}).
    async refresh() {{
      this.processed = 0;
      this.skipped = 0;
    }},
  }},
}};
</script>

<style scoped>
.{slug} {{ padding: 0.5rem; }}
</style>
'''

def main():
    plan = []
    for i, (slug, ent, ru, action) in enumerate(TOPICS):
        plan.append((f"internal/{slug}/{slug}.go", GO_TPL))
        plan.append((f"services/{slug}.py", PY_TPL))
        plan.append((f"web/modules/{slug}.js", JS_TPL))
        plan.append((f"web/components/{ent}Panel.vue", VUE_TPL))
    for rel, tpl in plan:
        slug = os.path.splitext(os.path.basename(rel))[0]
        # для vue имя файла — не slug, достанем slug из словаря по ent
        path = os.path.join(ROOT, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
    n = 0
    for slug, ent, ru, action in TOPICS:
        files = {
            f"internal/{slug}/{slug}.go": GO_TPL.format(pkg=slug, ent=ent, ru=ru, action=action),
            f"services/{slug}.py": PY_TPL.format(slug=slug, ent=ent, ru=ru, action=action),
            f"web/modules/{slug}.js": JS_TPL.format(slug=slug, ent=ent, ru=ru, action=action),
            f"web/components/{ent}Panel.vue": VUE_TPL.format(slug=slug, ent=ent, ru=ru, action=action),
        }
        for rel, content in files.items():
            path = os.path.join(ROOT, rel)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8", newline="\n") as f:
                f.write(content)
            n += 1
    print(f"generated {n} files under {os.path.abspath(ROOT)}")

if __name__ == "__main__":
    main()
