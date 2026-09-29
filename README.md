# CodePilot RAG (MVP)

Прототип для проверки гипотезы: агент, работающий через RAG-поиск по коду
со слоем решений, тратит существенно меньше токенов, чем агент, читающий
файлы напрямую (grep + read whole file).

Зависимости минимальны и без cgo: `sugarme/tokenizer`, vendored
`onnxruntime-purego` (см. ниже), `golang.org/x/sys`. Сборка — `go build`.

## Сборка и запуск

```bash
go build ./...
go build -o codepilot ./cmd/codepilot

./codepilot index sample_project        # индексация (повторный запуск — no-op)
./codepilot search "где проверяется токен" --mode hybrid+rerank --top 5
./codepilot serve --project sample_project   # MCP stdio-сервер
./codepilot eval                        # метрики по eval/golden_dataset.json
./codepilot bench                       # сравнение токенов baseline vs RAG
```

## Команды

| команда | что делает |
|---|---|
| `index [path]` | Полная/инкрементальная индексация. Manifest (file→sha256) и чанки в SQLite-файле `<path>/index.db` (pure-Go драйвер, внешний сервер не нужен); пересчитываются только изменённые файлы. Легаси `index.json` подхватывается и мигрирует при первом запуске. |
| `search "запрос"` | Отладочный поиск. Режимы: `fts` (BM25), `vec` (TF-IDF cosine), `hybrid` (RRF k=60), `hybrid+rerank` (порядок задаёт Laya), `hybrid+blend` (0.5·Laya + 0.5·RRF). |
| `serve` | MCP-сервер по stdio (newline-delimited JSON-RPC 2.0). Инструменты: `search_code`, `get_symbol`, `find_references`, `read_span`. Вызовы логируются в `mcp-calls.jsonl`. |
| `eval` | Прогон 12 вопросов датасета через конфигурации A=fts, B=vec, C=hybrid, D=hybrid+rerank, E=hybrid+blend. Recall@1/3/5, MRR, срезы по языку кода и вопроса, отчёт в `eval/report.md`. |
| `bench` | Симуляция двух агентов на 6 задачах: baseline (grep → чтение файлов целиком) vs RAG (`search_code` топ-5 + `read_span` при need_more). Токены = байты/4. |

## Архитектура

```
cmd/codepilot/main.go   CLI
internal/chunk/         чанкеры (.go — go/parser+ast; .py/.js — regex; .vue — SFC; fallback — окна 60/10)
internal/index/         индекс (SQLite index.db + manifest), BM25 (k1=1.5, b=0.75), TF-IDF, гибрид RRF (k=60)
internal/laya/          слой решений: интерфейс Scorer (Score 0..5, Noul 0..1) + эвристика
internal/mcp/           минимальный MCP stdio-сервер
internal/eval/          метрики по золотому датасету
internal/bench/         сравнение токенов baseline vs RAG
```

## Компонент ТЗ → прототип → прод

| Компонент (ТЗ) | В прототипе | В проде |
|---|---|---|
| tree-sitter чанкинг | go/parser+go/ast для Go, regex для Python/JS/Vue, fallback-окна | tree-sitter для всех языков |
| Postgres + pgvector | SQLite `index.db` (pure-Go `modernc.org/sqlite`) + in-memory BM25/TF-IDF | Postgres + pgvector для больших репо |
| ONNX Laya (convaiinnovations/laya-multilingual) | **есть**: ONNX-инференс в Go (`--laya onnx`), эвристика как fallback | + файнтюн на теневых логах, int8, GPU EP |
| mcp-go | свой минимальный MCP stdio (JSON-RPC по строкам) | официальный SDK mcp-go |
| LSP (определения/референсы) | индексные символы + word-boundary grep без строк/комментариев | LSP-серверы |
| Эмбеддинги | TF-IDF векторы (cosine) как замена | векторные эмбеддинги + pgvector |

## Ограничения прототипа

- TF-IDF вместо настоящих эмбеддингов: семантика ловится через
  совпадение лексем, сглаженное стеммингом RU/EN, дроблением идентификаторов,
  токенами пути файла и доменным лексиконом синонимов (`internal/index/lexicon.go`,
  аналог тезауруса Postgres FTS). В проде этот зазор закрывают эмбеддинги.
- JS/Python/Vue чанкеры — regex-based, без полноценного AST; на сложном
  коде возможны неточные границы чанков (fallback-окна как страховка).
- Версия чанкера (`chunk.Version`) хранится в индексе: при изменении
  чанкеров индекс пересобирается целиком, иначе — инкрементально по хешам.

## Скилл для агента

`.kimi/skills/codepilot-rag/SKILL.md` — инструкция агенту: когда и как
пользоваться MCP-инструментами codepilot вместо чтения файлов целиком.

## Настоящая Laya (ONNX) вместо эвристики

Модель `convaiinnovations/laya-multilingual` экспортирована в ONNX и лежит в
`models/laya-multilingual/` (контракт препроцессинга и паритет с PyTorch —
в `models/laya-multilingual/SPEC.md`). Запуск с нейросетевым слоем решений:

```bash
./codepilot eval --laya onnx     # реранк Score + гейт Noul выполняет Laya
./codepilot bench --laya onnx
./codepilot serve --laya onnx    # MCP с нейрореранком
```

- Движок инференса: `shota3506/onnxruntime-purego` (pure Go, без cgo) +
  нативная `bin/onnxruntime.dll` (v1.23.x). Пакет vendored в `third_party/`
  с патчем: загрузка DLL через `LoadLibrary` и путь к модели в UTF-16
  (в апстриме Windows не поддержан).
- Токенизатор: `sugarme/tokenizer` (pure Go), читает `tokenizer.json` чекпоинта.
- Реранк батчевый: 20 кандидатов одним прогоном модели.
- При отсутствии модели/DLL — автоматический fallback на эвристику.
- Режимы финальной выдачи: `hybrid+rerank` (чистый порядок Laya) и
  `hybrid+blend` (0.5·Laya + 0.5·RRF; bench и рекомендуемый дефолт — blend).
- Результаты на нашем датасете (12 вопросов, RU+EN):

  | scorer | режим | Recall@1 | Recall@5 | MRR | bench (экономия токенов) |
  |---|---|---|---|---|---|
  | heuristic | rerank | 0.75 | 1.00 | 0.86 | 6.24x |
  | Laya fp32 | rerank | 0.42 | 1.00 | 0.62 | 6.43x |
  | **Laya fp32** | **blend** | **0.83** | **1.00** | **0.87** | **6.65x** |
  | Laya int8 | blend | 0.67 | 0.92 | 0.73 | — |

  Выводы: (1) zero-shot Laya не портит состав топ-5, но проигрывает в
  точности топ-1 — риск ТЗ «слабый реранк zero-shot» подтверждён, лечение —
  файнтюн на теневых логах (цикл 6.5 ТЗ); до него рекомендуем blend.
  (2) int8-квантизация (1.29 ГБ → 0.88 ГБ, ускорение 2.2x) **отклонена по
  критерию ТЗ**: метрики просели (max |Δlogits| = 1.49, Recall@5 blend
  1.00 → 0.92). Скрипт `tools/laya-export/quantize_int8.py` сохранён —
  после файнтюна квантизацию нужно переоценить.
- Latency CPU (12 ядер): ~85–190 мс на инференс при L≈110; реранк пула
  из 20 чанков — секунды (для прода: GPU EP, обрезка max_len).
- `find_references` не понимает блочные комментарии и не различает
  одноимённые символы в разных файлах.

## Тесты и автообновление индекса

```bash
go test ./internal/...     # чанкеры (Go/Vue/fallback), токенизатор, инкремент, read_span
```

Git post-commit hook — `scripts/post-commit` (скопировать в `.git/hooks/`):
после коммита индекс обновляется инкрементально, новый код сразу находится
поиском (проверено на демо-репозитории `sample_project/`).
