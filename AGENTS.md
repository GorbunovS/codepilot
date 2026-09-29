# AGENTS.md — CodePilot RAG

Проект для будущих агентов, работающих с этим репозиторием. Читать до внесения изменений.

## Что это

MVP-прототип RAG-поиска по коду для AI-агентов. Гипотеза: агент, использующий
MCP-инструменты codepilot (`search_code`, `get_symbol`, `find_references`, `read_span`),
тратит существенно меньше токенов, чем агент с grep + чтением файлов целиком.

Пользовательский сценарий: развернуть codepilot → проиндексировать папку проекта →
поднять MCP stdio-сервер → агент ходит в него вместо grep.

## Команды

```bash
go build ./...                          # сборка всех пакетов
go build -o codepilot ./cmd/codepilot   # бинарь CLI
go vet ./... && go test ./internal/...  # проверки (тесты: chunk, index/tokenize, инкремент)

./codepilot index <path>               # индексация (инкремент по sha256-манифесту)
./codepilot search "запрос" --project <path> --mode hybrid+rerank --top 5 [--content]
./codepilot serve --project <path>     # MCP stdio-сервер, лог вызовов в mcp-calls.jsonl
./codepilot eval [--laya onnx]         # Recall@k/MRR по eval/golden_dataset.json
./codepilot bench [--laya onnx]        # токены baseline (grep+read) vs RAG
```

## Карта репозитория

| путь | назначение |
|---|---|
| `cmd/codepilot/main.go` | CLI (stdlib `flag`; `splitFlags` допускает флаги после позиционного запроса) |
| `internal/chunk/` | чанкеры: `.go` (go/parser+ast), `.py`/`.js` (regex), `.vue` (SFC); fallback-окна 60/10 |
| `internal/index/` | индекс: SQLite `index.db` (store.go) или Postgres+pgvector (pgstore.go), BM25 k1=1.5 b=0.75, TF-IDF, гибрид RRF k=60 (search.go), лексикон синонимов (lexicon.go) |
| `internal/embed/` | bi-encoder эмбеддинги e5-small (ONNX): префиксы `query: `/`passage: `, mean pooling, L2-норма; `remote.go` — HTTP-клиент MLX-сайдкара (`RemoteEmbedder`, общий интерфейс `TextEmbedder`) |
| `internal/ortlib/` | поиск нативной onnxruntime (.dll/.dylib/.so), env `CODEPILOT_ONNXRUNTIME_DLL` |
| `internal/laya/` | слой решений: интерфейс `Scorer` (Score 0..5, Noul 0..1), эвристика (laya.go), ONNX-модель (onnx.go) |
| `internal/mcp/` | MCP stdio-сервер: newline-delimited JSON-RPC 2.0, 4 инструмента, лог `mcp-calls.jsonl` |
| `internal/web/` | веб-панель (`web`): SPA на Vue 3/Chart.js (CDN, `static/index.html`, go:embed) + JSON API на stdlib net/http; список проектов в `~/.codepilot/projects.json`, индексация в горутине с логом через `index.Logf` (хук глобальный — восстанавливается после прогона, одновременно один проект) |
| `internal/eval/` | метрики A=fts B=vec C=hybrid D=hybrid+rerank E=hybrid+blend |
| `internal/bench/` | симуляция двух агентов; токены = байты/4; RAG-агент всегда `hybrid+blend` |
| `sample_project/` | демо-репозиторий для eval/bench |
| `scripts/post-commit` | git hook: инкрементальное обновление индекса после коммита |
| `scripts/download_models.sh` | скачивание e5-small ONNX в `models/` |
| `scripts/download_laya_src.sh` | скачивание чекпоинта Laya с HF (resume-цикл curl `-C -`, ~650 МБ) |
| `Dockerfile`, `docker-compose.yml` | стенд: Postgres+pgvector (db) + CLI (app) |
| `third_party/onnxruntime-purego` | vendored ONNX runtime (см. ниже) |
| `tools/laya-export` | офлайн-экспорт/квантизация модели Laya (`requirements.txt` — torch 2.x + laya + onnxscript, Python ≥3.12) |
| `tools/mlx-sidecar` | MLX-сайдкар эмбеддингов e5 (FastAPI на 127.0.0.1:8081, `run.sh` — venv + uvicorn); инференс на Apple GPU, ~100x к локальному ONNX; подключается флагом `--embed-server URL` (env `CODEPILOT_EMBED_SERVER`) |

## Инварианты и специфика (важно)

- **Без cgo.** Все зависимости pure-Go: `modernc.org/sqlite`, `sugarme/tokenizer`,
  vendored `onnxruntime-purego` (replace в go.mod). Новые зависимости — только
  после проверки отсутствия cgo.
- **stdout у `serve` — канал MCP-протокола.** Любой служебный вывод — только в stderr,
  иначе сломается JSON-RPC. stdout у `web` не протокольный, но служебный вывод панели
  тоже только в stderr — панель не должна ломать остальные команды CLI.
- **SQLite — не векторная база.** `index.db` хранит чанки и манифест; BM25/TF-IDF
  перестраиваются в памяти при каждом `Load` (`buildModel`). «Векторный» поиск —
  TF-IDF cosine, настоящих эмбеддингов нет.
- **`chunk.Version`** (internal/chunk/chunk.go) инкрементировать при любом изменении
  чанкеров — иначе инкрементальная индексация молча переиспользует старые чанки.
  Новый язык = запись в `registry` + bump `Version`.
- **`.gitignore`: бинарь заанкорен как `/codepilot`.** Паттерн без слеша игнорировал
  каталог `cmd/codepilot/` — так CLI был потерян из репо. Не убирать слеш.
- **`eval` перезаписывает `eval/report.md`.** Закоммиченный отчёт — от прогона с
  ONNX Laya (`--laya onnx`). Не коммитить перезаписи от эвристики без необходимости.
- **ONNX опционален.** Модель (`models/laya-multilingual/`, ~1.3 ГБ) и рантайм
  (`bin/`) не в репо. `--laya onnx` или `CODEPILOT_LAYA=onnx`; при любой ошибке
  загрузки — автоматический fallback на эвристику с warning в stderr.
  Windows: `bin/onnxruntime.dll`; unix: dlopen (dlopen_unix.go), нужен
  `.dylib`/`.so`. Режимы финальной выдачи: `hybrid+rerank` (чистый порядок Laya)
  и `hybrid+blend` (`blendAlpha = 0.5` в search.go; рекомендуемый дефолт — blend,
  пока Laya zero-shot).
- **Индекс лежит в корне индексируемого проекта** (`<path>/index.db`) и валяется
  там рядом с чужим кодом. Для прода планируется централизованное хранилище.
- **pg-режим (`--store pg`).** Чанки и векторы e5 (384 dim) в Postgres+pgvector,
  ключ проекта — абсолютный путь корня (в Docker это `/work/...`, снаружи —
  хостовый путь: это разные ключи). Эмбеддинги пересчитываются по sha256 чанка.
  Сохранение порционное (`saveCommitBatch = 2048` в pgstore.go) — обрыв не
  теряет вектора, повторный запуск продолжает с места обрыва.
  Контракт e5: префиксы `query: `/`passage: `, mean pooling, L2-норма — не менять
  без пересборки всех векторов. Интеграционный тест pgstore требует
  `CODEPILOT_PG_TEST_DSN`, без него скипается.
- **`--embed-server URL`** (env `CODEPILOT_EMBED_SERVER`) — эмбеддинги e5 через
  MLX-сайдкар (`tools/mlx-sidecar/run.sh`, только Apple Silicon) вместо локального
  ONNX; `--embed onnx` тогда не нужен. Поле `Index.Emb` типизировано интерфейсом
  `embed.TextEmbedder` (локальный `Embedder` и `RemoteEmbedder` взаимозаменяемы).
  При старте индексации размерность сверяется с `embed.Dim` (384), векторы
  совместимы с ONNX (паритет cos ≈ 0.99).
- **`--device cpu|coreml|cuda`** (env `CODEPILOT_DEVICE`) — execution provider
  onnxruntime для e5 и Laya (`embed.ProvidersForDevice`, `embed.SessionOptions`).
  CoreML не читает внешние веса `.onnx.data`: Laya под CoreML берёт
  `laya.single.onnx` (склейка — `tools/laya-export/merge_external_data.py`).
  Токенизатор e5/Laya может паниковать на экзотике юникода — panic ловится
  в `embed.encodeSafe`, чанк получает нулевой вектор, счётчик — `Skipped()`.
- Комментарии в коде и сообщения коммитов — на русском.

## Контракты, от которых зависят другие части

- `index.QueryTerms(query)` — единая точка токенизации/расширения запроса; поиск и
  Laya обязаны видеть один и тот же запрос.
- `SearchHit` сортируется по `Score` по убыванию; пул реранка = топ-20 hybrid.
- Noul-гейт: `noul < 0.5` → `need_more: true` (агенту предлагается дочитать контекст).
  Порог зашит в MCP-сервере и bench.
- ONNX-контракт модели (входы/выходы, сборка последовательности) — порт
  `laya/common.py`, должен совпадать с `models/laya-multilingual/SPEC.md`.

## Известные ограничения (не «чинить» молча)

- JS/Python/Vue чанкеры — regex-based, границы чанков могут плыть (fallback-окна как страховка).
- `find_references` не понимает блочные комментарии и не различает одноимённые символы в разных файлах.
- Эвристический `Noul` откалиброван под эвристический `Score` — меняя один, проверяй другой.
- Эвристика даёт R@1 0.75; Laya fp32 blend — 0.83. Чистый zero-shot rerank Laya (0.42)
  хуже эвристики — поэтому дефолтный режим в MCP `hybrid+rerank` + рекомендация blend в bench.

## Smoke-проверка после изменений

```bash
go build -o codepilot ./cmd/codepilot
./codepilot index sample_project
./codepilot search "где проверяется токен" --project sample_project --top 3   # топ-1: internal/auth/auth.go ValidateToken
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | ./codepilot serve --project sample_project
./codepilot eval && ./codepilot bench
```
