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
./codepilot search "запрос" --project <path> --top 5 [--content]
./codepilot serve                      # MCP-демон на все проекты из ~/.codepilot/projects.json (без флагов)
./codepilot serve --project <path>     # однопроектный MCP (совместимость), лог вызовов в mcp-calls.jsonl
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
| `internal/mcp/` | MCP-сервер: newline-delimited JSON-RPC 2.0 (stdio) + `HandleRaw` для HTTP; 6 инструментов, лог `mcp-calls.jsonl`; мультипроект: аргумент `project` во всех инструментах + `list_projects`, индексы подключаются лениво через `ResolveFunc` и кэшируются; `reindex` — триггер фоновой переиндексации (`ReindexFunc`, подключается через `SetReindex`) |
| `internal/config/` | общий конфиг демона `~/.codepilot/config.json` + реестр проектов `projects.json` (`name` — идентификатор для агента); `Find` — маршрутизация project-аргумента (точное имя → регистр → путь → дефолт → первый) |
| `internal/web/` | веб-панель (`web`): SPA на Vue 3 + Chart.js (CDN, `static/index.html`, go:embed) + JSON API на stdlib net/http; список проектов в `~/.codepilot/projects.json`, состояние прогонов — в `~/.codepilot/jobs.json`; при старте мержит явные флаги поверх `config.json` и сохраняет его обратно (serve без флагов читает его); индексация в горутине с логом через `index.Logf` (хук глобальный — восстанавливается после прогона, одновременно один проект); кэш открытых индексов (`idxCache`, инвалидация по mtime/size и после прогона) для дешёвого polling `/api/stats`; `/api/devices` — только реально доступные устройства (`embed.AvailableProviders`, детекция один раз); `/api/search` — ручной поиск из панели (scorer — ленивый синглтон Laya); `/api/skill*` — выдача/установка скилла агента (`static/skill/SKILL.md`, go:embed) в `.kimi-code/skills/` (или существующий `.agents/skills/`); `/api/mcp/install` — мерж `mcpServers.codepilot` в `<проект>/.kimi-code/mcp.json` (сниппет — http: `{"type":"http","url":"http://<addr>/mcp"}`); `POST /mcp` — MCP-демон по HTTP на кэше индексов панели (`mcp.NewServer` + `HandleRaw`, scorer — ленивый синглтон); добавление проекта без `index.db` автоматически запускает индексацию; `low_cpu` в `/api/index` — щадящий режим (½ ядер) |
| `internal/eval/` | метрики A=fts B=vec C=hybrid D=hybrid+rerank E=hybrid+blend |
| `internal/bench/` | симуляция двух агентов; токены = байты/4; RAG-агент: hybrid+blend + FilterRelevant, при need_more — эскалация (top_k×2 + read_span) |
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
- **Walker пропускает служебные каталоги и гигантские файлы** (internal/index/index.go):
  `.git`, `node_modules`, `vendor`, `.venv`/`venv` и кэши python, `dist`/`build`/`target`/
  `.next`/`.nuxt`; файлы > `maxFileBytes` (1 МБ) пропускаются. Без этого `.venv` с torch
  давал 100k+ чужих чанков, а мегабайтные строки вешали токенизатор на минуты.
- **Текст перед токенизацией e5 режется до `maxEmbedBytes` (8 КБ)** (embed.encodeSafe):
  модель всё равно обрезает до 512 токенов, а BPE-токенизация длинного хвоста — чистая
  трата времени. Обрезка по границе руны.
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
- **pg-режим (`--store pg`).** Чанки и векторы e5 (384 dim) в Postgres+pgvector.
  **У каждого проекта своя таблица** `chunks_<hex(sha256 пути)[:16]>`
  (`chunksTable` в pgstore.go) — смешивание векторов проектов структурно
  невозможно; HNSW-индекс тоже свой (`idx_vec_<suffix>`). Общая только `meta`
  (манифест/версии, векторов там нет). Ключ проекта — абсолютный путь корня
  (в Docker это `/work/...`, снаружи — хостовый путь: это разные проекты).
  Легаси-таблица `chunks` (с колонкой project) мигрирует в per-project таблицы
  при `OpenPG` и удаляется. Эмбеддинги пересчитываются по sha256 чанка.
  Сохранение порционное (`saveCommitBatch = 2048` в pgstore.go) — обрыв не
  теряет вектора, повторный запуск продолжает с места обрыва.
  HNSW на массовой заливке (`bulkThreshold = 4096`) дропается и пересоздаётся
  в конце (`createVecIndex`, `max_parallel_maintenance_workers = 0` —
  параллельная сборка падает о дефолтный /dev/shm=64MB в Docker).
  Контракт e5: префиксы `query: `/`passage: `, mean pooling, L2-норма — не менять
  без пересборки всех векторов. Интеграционный тест pgstore требует
  `CODEPILOT_PG_TEST_DSN`, без него скипается.
- **Исключения индексации.** Встроенный skip-лист каталогов (`.git`,
  `node_modules`, `vendor`, `.venv`/`venv`, кэши python, `dist`/`build`/`target`/
  `.next`/`.nuxt`) + потолок файла 1 МБ (`maxFileBytes`) + `.codepilotignore`
  в корне проекта: шаблон на строку, `#` — комментарий; со слэшем — путь от
  корня (`sample_project/`), без слэша — имя на любом уровне или glob
  (`fixtures`, `*.min.js`), хвостовой `/` — только каталоги.
- **`--embed-server URL`** (env `CODEPILOT_EMBED_SERVER`) — эмбеддинги e5 через
  MLX-сайдкар (`tools/mlx-sidecar/run.sh`, только Apple Silicon) вместо локального
  ONNX; `--embed onnx` тогда не нужен. Поле `Index.Emb` типизировано интерфейсом
  `embed.TextEmbedder` (локальный `Embedder` и `RemoteEmbedder` взаимозаменяемы).
  При старте индексации размерность сверяется с `embed.Dim` (384), векторы
  совместимы с ONNX (паритет cos ≈ 0.99).
- **`--device cpu|coreml|cuda|directml`** (env `CODEPILOT_DEVICE`) — execution provider
  onnxruntime для e5 и Laya (`embed.ProvidersForDevice`, `embed.SessionOptions`).
  CoreML не читает внешние веса `.onnx.data`: Laya под CoreML берёт
  `laya.single.onnx` (склейка — `tools/laya-export/merge_external_data.py`).
  Токенизатор e5/Laya может паниковать на экзотике юникода — panic ловится
  в `embed.encodeSafe`, чанк получает нулевой вектор, счётчик — `Skipped()`.
- **`--max-threads N`** (env `CODEPILOT_MAX_THREADS`, 0 — все ядра) — лимит
  `IntraOpNumThreads` для сессий ONNX (e5 и Laya). В веб-панели это чекбокс
  «щадящий режим» (½ ядер) — иначе индексация на CPU вешает слабую машину.
- Комментарии в коде и сообщения коммитов — на русском.
- **Состояние прогонов панели (`~/.codepilot/jobs.json`) сохраняется между
  перезапусками.** После рестарта `./codepilot web` `last_result` и хвост лога
  каждого проекта восстанавливаются из файла; живой прогресс, как и раньше,
  транслируется через in-memory задачу.
- **Windows: MLX-сайдкар не работает.** Дома на Windows используй `--embed onnx`
  (нужен `bin/onnxruntime.dll` или `CODEPILOT_ONNXRUNTIME_DLL`) либо SQLite/TF-IDF
  без `--store pg`. Панель (`./codepilot.exe web`) и MCP (`serve`) работают как
  есть; конфиг и проекты лежат в `%USERPROFILE%\.codepilot`.

## Контракты, от которых зависят другие части

- `index.QueryTerms(query)` — единая точка токенизации/расширения запроса; поиск и
  Laya обязаны видеть один и тот же запрос.
- `SearchHit` сортируется по `Score` по убыванию; пул реранка = топ-20 hybrid.
- Noul-гейт: `noul < 0.5` → `need_more: true` (агенту предлагается дочитать контекст).
  Порог зашит в MCP-сервере и bench. Noul считается по хитам ПОСЛЕ
  адаптивной отсечки — по тому, что реально увидит агент.
- **MCP: адаптивная отсечка выдачи** (`index.FilterRelevant`, константы
  `RelevantFloor`/`RelevantMargin` в search.go). Zero-shot скоры Laya сжаты
  (топ-1 ~0.55–0.65, за ним плато шума 0.50–0.55), поэтому отсекаем хвост по
  связке «пол 0.5 + отрыв от топ-1 0.05», минимум 1 хит. Ответ — slim-проекция
  (без id/hash), поле `more_available: true` = выдача обрезана, агент может
  повторить с большим top_k. К деградированному hybrid-порядку (RRF-шкала)
  фильтр не применяется.
- **MCP: бюджет search_code** (`internal/mcp/server.go`): Laya-реранк на CPU идёт
  десятки секунд — дольше таймаута MCP-клиента (~60с). По дедлайну
  (`CODEPILOT_MCP_SEARCH_TIMEOUT`, дефолт 25с) и при занятом реранке отдаём
  чистый hybrid-порядок с `degraded: rerank_timeout|rerank_busy`, Noul —
  эвристикой. ORT Run не отменяется — досчитывается в фоне.
- **MCP мультипроектный.** `serve` без `--project` читает настройки из
  `~/.codepilot/config.json` (его пишет панель при старте; явные флаги serve
  перекрывают) и реестр `projects.json` (перечитывается на каждый вызов —
  панель может добавить проект, пока serve жив). Агент передаёт имя проекта
  в аргументе `project` любого инструмента; пустой — `default_project` из
  конфига, иначе первый в реестре. Неизвестный проект → ошибка со списком
  имён. Индексы открываются лениво и кэшируются на процесс; в pg-режиме
  подключение и эмбеддер общие на все проекты. Инвалидации кэша после
  переиндексации нет — serve перечитывает индекс только после перезапуска
  (агентский клиент сам пересоздаёт процесс при реконнекте).
- **MCP-демон — это панель.** Основной транспорт — HTTP: `POST /mcp` на
  `codepilot web` (один JSON-RPC на запрос, notifications → 202). Сниппет
  mcp.json — только `{"type":"http","url":...}`: агенты не плодят процессы
  codepilot.exe на сессию. HTTP обслуживает тот же `mcp.Server`
  (`HandleRaw`) на общем кэше индексов панели — статистика, ручной поиск и
  MCP видят одни данные. stdio (`serve`) остаётся для клиентов без
  http-транспорта. serve при выходе НЕ закрывает ONNX-сессии/pg: фоновый
  реранк может ещё идти, Close под ORT Run = access violation.
- **Инструмент `reindex` — триггер из скилла.** Агент вызывает его, когда
  задача завершена и код изменился (или пользователь просит пуш): в панели
  это тот же фоновый прогон, что по кнопке (глобальный гейт), в stdio-serve —
  горутина с инкрементальным BuildPrev + сбросом кэша индекса (свежий
  поднимется на следующем запросе). Без изменённых файлов — дешёвый no-op.
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
