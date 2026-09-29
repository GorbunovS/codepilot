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
internal/index/         индекс (SQLite index.db + manifest; опц. Postgres+pgvector в pgstore.go),
                        BM25 (k1=1.5, b=0.75), TF-IDF, гибрид RRF (k=60)
internal/embed/         bi-encoder эмбеддинги multilingual-e5-small (ONNX; префиксы query:/passage:,
                        mean pooling, L2-норма)
internal/ortlib/        поиск нативной onnxruntime (.dll/.dylib/.so, env CODEPILOT_ONNXRUNTIME_DLL)
internal/laya/          слой решений: интерфейс Scorer (Score 0..5, Noul 0..1) + эвристика
internal/mcp/           минимальный MCP stdio-сервер
internal/eval/          метрики по золотому датасету
internal/bench/         сравнение токенов baseline vs RAG
```

## Компонент ТЗ → прототип → прод

| Компонент (ТЗ) | В прототипе | В проде |
|---|---|---|
| tree-sitter чанкинг | go/parser+go/ast для Go, regex для Python/JS/Vue, fallback-окна | tree-sitter для всех языков |
| Postgres + pgvector | SQLite `index.db` (pure-Go `modernc.org/sqlite`) + in-memory BM25/TF-IDF; **есть** pg-режим `--store pg` (pgvector HNSW + e5) | Postgres FTS + файнтюн эмбеддера на логах |
| ONNX Laya (convaiinnovations/laya-multilingual) | **есть**: ONNX-инференс в Go (`--laya onnx`), эвристика как fallback | + файнтюн на теневых логах, int8, GPU EP |
| mcp-go | свой минимальный MCP stdio (JSON-RPC по строкам) | официальный SDK mcp-go |
| LSP (определения/референсы) | индексные символы + word-boundary grep без строк/комментариев | LSP-серверы |
| Эмбеддинги | TF-IDF (sqlite-режим) или e5-small ONNX (pg-режим, `--embed onnx`) | файнтюн bi-encoder на теневых логах проекта |

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

## Векторный режим: Postgres + pgvector + e5 (Docker)

По умолчанию индекс живёт в `<path>/index.db` (SQLite), а «векторный» поиск —
TF-IDF в памяти. Режим `--store pg` — настоящая векторная БД: чанки и векторы
`multilingual-e5-small` (ONNX, 384 dim, fp32) в Postgres + pgvector (HNSW),
BM25 по-прежнему строится в памяти. Один Postgres обслуживает несколько
проектов (ключ — абсолютный путь корня).

```bash
./scripts/download_models.sh      # один раз: e5-small ONNX (~470 МБ)
docker compose up -d db           # Postgres + pgvector
docker compose build app          # codepilot + onnxruntime для linux
docker compose run --rm app index sample_project
docker compose run --rm app eval
docker compose run --rm app bench
docker compose run --rm app search "запрос" --project sample_project --mode vec
docker compose run --rm -T app serve --project sample_project   # MCP по stdio
```

Локально без Docker (нужны Postgres с pgvector и нативная onnxruntime в
`bin/` или по пути из `CODEPILOT_ONNXRUNTIME_DLL`):

```bash
./codepilot index sample_project --store pg --embed onnx
./codepilot search "запрос" --project sample_project --store pg --embed onnx --mode vec
```

Переменные окружения: `CODEPILOT_STORE`, `CODEPILOT_PG_DSN`, `CODEPILOT_EMBED`.
Эмбеддинги пересчитываются только для изменённых чанков (по sha256 файла);
в sqlite-режиме всё работает как раньше, векторного поиска там нет.
Сохранение в pg идёт порционными транзакциями (по 2048 чанков): обрыв
индексации не теряет уже посчитанные векторы — повторный запуск продолжит
с места обрыва.

## Устройство инференса: --device cpu|coreml|cuda

Флаг `--device` (env `CODEPILOT_DEVICE`) выбирает execution provider
onnxruntime для e5 и Laya. На Apple Silicon доступен `coreml` (GPU/ANE):
e5-small работает на CoreML из коробки (448/623 узлов графа), Laya — только
после склейки в однофайловую модель (CoreML не читает внешние веса
`.onnx.data`):

```bash
tools/laya-export/.venv/bin/python tools/laya-export/merge_external_data.py models/laya-multilingual
./codepilot index <проект> --store pg --embed onnx --device coreml
```

Если запрошенного провайдера нет в сборке onnxruntime — предупреждение в
stderr и fallback на CPU. Замеры на M-серии (pnodes, 133k чанков): e5 на
CoreML **медленнее** CPU — переменная длина последовательности заставляет
CoreML перекомпилировать партиции почти на каждый батч; для индексации
оставляем CPU. Laya под CoreML работает, но компиляция 252 партиций на
старте (~7 мин) съедает выгоду при коротких сессиях.

Результаты на том же датасете (12 вопросов, эвристика; E0 = sqlite/TF-IDF,
E3 = pg/e5-small):

| режим | E0 Recall@1 | E3 Recall@1 | E0 MRR | E3 MRR |
|---|---|---|---|---|
| fts | 0.83 | 0.83 | 0.90 | 0.90 |
| vec | 0.75 | **0.92** | 0.85 | **0.96** |
| hybrid | 0.75 | **0.92** | 0.86 | **0.96** |
| hybrid+blend | 0.75 | **0.83** | 0.86 | **0.90** |

Замена TF-IDF на настоящие эмбеддинги подняла vec-режим с 0.75 до 0.92 по
Recall@1 без всякой Laya; индексация sample_project — ~16 с, повторная — <1 с.

## Настоящая Laya (ONNX) вместо эвристики

Модель `convaiinnovations/laya-multilingual` экспортирована в ONNX и лежит в
`models/laya-multilingual/` (контракт препроцессинга и паритет с PyTorch —
в `models/laya-multilingual/SPEC.md`). Установка из чекпоинта с Hugging Face:

```bash
./scripts/download_laya_src.sh                       # чекпоинт ~650 МБ в models/laya-multilingual-src/
uv venv --python 3.12 tools/laya-export/.venv        # нужен Python ≥3.12 (для torch 2.x)
uv pip install --python tools/laya-export/.venv/bin/python -r tools/laya-export/requirements.txt
tools/laya-export/.venv/bin/python tools/laya-export/export_onnx.py \
    models/laya-multilingual-src models/laya-multilingual
```

Экспорт проверяет паритет с PyTorch (у нас: max |Δlogits| = 4.2e-05).
Запуск с нейросетевым слоем решений:

```bash
./codepilot eval --laya onnx     # реранк Score + гейт Noul выполняет Laya
./codepilot bench --laya onnx
./codepilot serve --laya onnx    # MCP с нейрореранком
```

- Движок инференса: `shota3506/onnxruntime-purego` (pure Go, без cgo) +
  нативная onnxruntime v1.23.x — `bin/onnxruntime.dll` (Windows) или
  `bin/libonnxruntime.dylib` (macOS) / `libonnxruntime.so` (Linux); поиск —
  `internal/ortlib` (env `CODEPILOT_ONNXRUNTIME_DLL`). Пакет vendored в
  `third_party/` с патчем: загрузка DLL через `LoadLibrary` и путь к модели
  в UTF-16 (в апстриме Windows не поддержан).
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
- Полный стек (pg + e5-эмбеддинги + Laya, тот же датасет): blend даёт
  Recall@1 = 0.92, Recall@5 = 1.00, MRR = 0.94 — но plain hybrid на e5-пуле
  без Laya уже даёт 0.92/1.00/0.96, а чистый реранк просаживает Recall@5 до
  0.92 (zero-shot Laya выталкивает правильный чанк из топ-5). Вывод: на
  сильном эмбеддере ценность zero-shot Laya — не реранк, а гейт Noul и цель
  для файнтюна на теневых логах.
- Latency CPU (12 ядер): ~85–190 мс на инференс при L≈110; реранк пула
  из 20 чанков — секунды (для прода: GPU EP, обрезка max_len).
- `find_references` не понимает блочные комментарии и не различает
  одноимённые символы в разных файлах.

## Веб-панель и десктоп (Pake)

Локальная панель для управления проектами: добавление через мини-проводник,
запуск индексации с живым логом, статистика индекса и нагрузки по вызовам
MCP-инструментов (графики), готовый сниппет MCP-конфига с кнопкой копирования.
Список проектов — в `~/.codepilot/projects.json`.

```bash
./codepilot web                       # панель на http://127.0.0.1:8080
./codepilot web --addr 127.0.0.1:8080 --log mcp-calls.jsonl [--store pg --embed onnx] [--laya onnx]
```

Десктоп-обёртка (Pake, окно поверх панели — бэкенд не поднимает):

```bash
./codepilot web &       # панель должна работать и при сборке, и при запуске приложения
./scripts/build_app.sh  # go build + pnpm dlx pake-cli http://127.0.0.1:8080 --name CodePilot
```

## Тесты и автообновление индекса

```bash
go test ./internal/...     # чанкеры (Go/Vue/fallback), токенизатор, инкремент, read_span
```

Git post-commit hook — `scripts/post-commit` (скопировать в `.git/hooks/`):
после коммита индекс обновляется инкрементально, новый код сразу находится
поиском (проверено на демо-репозитории `sample_project/`).
