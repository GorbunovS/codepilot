// Command codepilot — CLI: индексация проекта, отладочный поиск,
// MCP stdio-сервер для агента, eval по золотому датасету и bench токенов.
//
//	codepilot index sample_project
//	codepilot search "где проверяется токен" --mode hybrid+rerank --top 5
//	codepilot serve --project sample_project
//	codepilot eval [--laya onnx]
//	codepilot bench [--laya onnx]
//	codepilot web [--addr 127.0.0.1:8080]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"codepilot/internal/bench"
	"codepilot/internal/config"
	"codepilot/internal/embed"
	"codepilot/internal/eval"
	"codepilot/internal/index"
	"codepilot/internal/laya"
	"codepilot/internal/mcp"
	"codepilot/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "index":
		err = cmdIndex(os.Args[2:])
	case "search":
		err = cmdSearch(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "eval":
		err = cmdEval(os.Args[2:])
	case "bench":
		err = cmdBench(os.Args[2:])
	case "web":
		err = cmdWeb(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `codepilot — RAG-поиск по коду для агентов (MCP).

Команды:
  codepilot index [path]        полная/инкрементальная индексация (индекс: <path>/index.db)
  codepilot search "запрос"     отладочный поиск по индексу
  codepilot serve               MCP stdio-сервер (инструменты search_code, get_symbol,
                                find_references, read_span)
  codepilot eval                Recall@k/MRR по золотому датасету, отчёт в eval/report.md
  codepilot bench               расход токенов: baseline (grep+read) vs RAG
  codepilot web                 локальная веб-панель (десктоп-обёртка — Pake)

Флаги search: --project . --mode hybrid+rerank --top 5 --content [--laya onnx]
Флаги serve:  без --project — мультипроектный демон (настройки из
  ~/.codepilot/config.json, проекты из projects.json, проект выбирается
  аргументом project инструментов); --project . — однопроектный режим
  (совместимость); --log mcp-calls.jsonl [--laya onnx]
Флаги eval:   --project sample_project --dataset eval/golden_dataset.json [--laya onnx]
Флаги bench:  --project sample_project --dataset eval/golden_dataset.json [--laya onnx]
Флаги web:    --addr 127.0.0.1:8080 --log mcp-calls.jsonl [хранилище, --laya]

Хранилище: --store sqlite (по умолчанию) | --store pg (Postgres+pgvector,
  --pg-dsn, --embed onnx [--embed-dir models/e5-small]).
  Переменные: CODEPILOT_STORE, CODEPILOT_PG_DSN, CODEPILOT_EMBED.
  --embed-server URL — эмбеддинги через MLX-сайдкар (tools/mlx-sidecar/run.sh)
  вместо локального ONNX (CODEPILOT_EMBED_SERVER).

Устройство инференса: --device cpu (по умолчанию) | coreml | cuda | directml
  (CODEPILOT_DEVICE); если провайдера нет в сборке onnxruntime — fallback на CPU.
  --max-threads N (CODEPILOT_MAX_THREADS) — лимит потоков инференса ONNX
  (0 — все ядра); щадящий режим для слабых CPU.

Слой решений Laya: --laya heuristic (по умолчанию) | --laya onnx
  [--laya-dir models/laya-multilingual]; также читается CODEPILOT_LAYA.
Режимы поиска: fts (BM25), vec (TF-IDF cosine; в pg-режиме — e5+pgvector),
hybrid (RRF k=60), hybrid+rerank (порядок задаёт Laya), hybrid+blend
(0.5·Laya + 0.5·RRF).
`)
}

// layaFlags — общие флаги слоя решений для search/serve/eval/bench.
func layaFlags(fs *flag.FlagSet) (kind, dir *string) {
	kind = fs.String("laya", "", "движок слоя решений: heuristic|onnx (по умолчанию CODEPILOT_LAYA или heuristic)")
	dir = fs.String("laya-dir", "", "каталог модели Laya (по умолчанию models/laya-multilingual)")
	return kind, dir
}

func resolveScorer(kind, dir string, threads int, providers []string) laya.Scorer {
	return laya.Resolve(kind, dir, threads, providers...)
}

// closeScorer освобождает ресурсы ONNX-модели (у эвристики их нет).
func closeScorer(s laya.Scorer) {
	if c, ok := s.(interface{ Close() }); ok {
		c.Close()
	}
}

// storeFlagsT — общие флаги хранилища и эмбеддингов для всех команд.
type storeFlagsT struct {
	store       *string
	dsn         *string
	embed       *string
	embedDir    *string
	embedServer *string
	device      *string
	maxThreads  *int
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func storeFlags(fs *flag.FlagSet) storeFlagsT {
	return storeFlagsT{
		store:       fs.String("store", envOr("CODEPILOT_STORE", "sqlite"), "хранилище индекса: sqlite|pg"),
		dsn:         fs.String("pg-dsn", envOr("CODEPILOT_PG_DSN", "postgres://codepilot:codepilot@localhost:5432/codepilot?sslmode=disable"), "DSN Postgres (режим pg)"),
		embed:       fs.String("embed", envOr("CODEPILOT_EMBED", ""), `эмбеддер: onnx|"" (по умолчанию без эмбеддингов)`),
		embedDir:    fs.String("embed-dir", "models/e5-small", "каталог ONNX-модели эмбеддингов"),
		embedServer: fs.String("embed-server", envOr("CODEPILOT_EMBED_SERVER", ""), "URL MLX-сайдкара эмбеддингов (напр. http://127.0.0.1:8081); заменяет --embed onnx"),
		device:      fs.String("device", envOr("CODEPILOT_DEVICE", "cpu"), "устройство инференса ONNX: cpu|coreml|cuda"),
		maxThreads:  fs.Int("max-threads", envOrInt("CODEPILOT_MAX_THREADS", 0), "лимит потоков инференса ONNX (0 — все ядра)"),
	}
}

// providers мапит --device в execution providers onnxruntime.
func (sf storeFlagsT) providers() ([]string, error) {
	return embed.ProvidersForDevice(*sf.device)
}

// mustProviders — то же, но с завершением процесса при невалидном --device.
func mustProviders(sf storeFlagsT) []string {
	p, err := sf.providers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	return p
}

// loadIndexStore открывает индекс проекта из выбранного хранилища.
// В режиме pg подключает эмбеддер (MLX-сайдкар по embedServer, иначе локальный
// ONNX при embedKind == "onnx") и возвращает cleanup для освобождения ресурсов.
// providers — execution providers ONNX; threads — лимит потоков инференса (0 — все ядра).
func loadIndexStore(project, store, dsn, embedKind, embedDir, embedServer string, threads int, providers []string) (*index.Index, func(), error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, nil, err
	}
	switch store {
	case "sqlite", "":
		path := index.IndexPath(abs)
		if _, err := os.Stat(path); err != nil {
			return nil, nil, fmt.Errorf("индекс не найден (%s); сначала выполните: codepilot index %s", path, project)
		}
		ix, err := index.Load(path)
		return ix, func() {}, err
	case "pg":
		pg, err := index.OpenPG(dsn)
		if err != nil {
			return nil, nil, err
		}
		cleanup := func() { pg.Close() }
		ix, err := pg.Load(abs)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		ix.PG = pg
		if embedServer != "" {
			emb := embed.NewRemoteEmbedder(embedServer)
			ix.Emb = emb
			cleanup = func() { emb.Close(); pg.Close() }
		} else if embedKind == "onnx" {
			emb, err := embed.Load(embedDir, threads, providers...)
			if err != nil {
				cleanup()
				return nil, nil, err
			}
			ix.Emb = emb
			cleanup = func() { emb.Close(); pg.Close() }
		}
		return ix, cleanup, nil
	default:
		return nil, nil, fmt.Errorf("неизвестное хранилище %q (sqlite|pg)", store)
	}
}

func cmdIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	project := fs.String("project", "", "корень проекта (приоритетнее позиционного аргумента)")
	sf := storeFlags(fs)
	flagArgs, positional := splitFlags(fs, args)
	_ = fs.Parse(flagArgs)
	root := *project
	if root == "" && len(positional) > 0 {
		root = positional[0]
	}
	if root == "" {
		root = "."
	}
	index.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	// pg-режим: предыдущее состояние (манифест+чанки) берём из Postgres,
	// иначе каждый прогон был бы полным речанкингом — локального index.db нет.
	var pg *index.PGStore
	if *sf.store == "pg" {
		var err error
		pg, err = index.OpenPG(*sf.dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
	}
	var prev *index.Index
	if pg != nil {
		abs, _ := filepath.Abs(root)
		prev = pg.Prev(abs)
	}
	ix, st, err := index.BuildPrev(root, prev)
	if err != nil {
		return err
	}
	switch *sf.store {
	case "sqlite", "":
		if err := ix.Save(); err != nil {
			return fmt.Errorf("сохранение индекса: %w", err)
		}
		fmt.Printf("%s: файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d)\n",
			ix.ProjectRoot, st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed)
	case "pg":
		var emb index.PassageEmbedder
		var skipped func() int
		if *sf.embedServer != "" {
			r := embed.NewRemoteEmbedder(*sf.embedServer)
			if err := r.CheckDim(); err != nil {
				return err
			}
			emb = r
			defer r.Close()
		} else {
			if *sf.embed != "onnx" {
				return fmt.Errorf("--store pg требует --embed onnx или --embed-server URL (векторы e5); без эмбеддингов используйте --store sqlite")
			}
			providers, err := sf.providers()
			if err != nil {
				return err
			}
			e, err := embed.Load(*sf.embedDir, *sf.maxThreads, providers...)
			if err != nil {
				return err
			}
			defer e.Close()
			emb = e
			skipped = e.Skipped
		}
		embedded, err := pg.Save(ix, emb)
		if err != nil {
			return fmt.Errorf("сохранение индекса (pg): %w", err)
		}
		fmt.Printf("%s (pg): файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d), %s\n",
			ix.ProjectRoot, st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed, vecNote(embedded))
		if skipped != nil {
			if n := skipped(); n > 0 {
				fmt.Printf("внимание: %d чанков получили нулевой вектор (паника токенизатора, см. stderr)\n", n)
			}
		}
	default:
		return fmt.Errorf("неизвестное хранилище %q (sqlite|pg)", *sf.store)
	}
	return nil
}

// vecNote — человекочитаемая строка про пересчёт векторов: ноль пересчитанных
// при живом индексе — это «всё уже посчитано», а не «эмбеддер не запустился».
func vecNote(embedded int) string {
	if embedded == 0 {
		return "векторы актуальны (пересчёт не требуется)"
	}
	return fmt.Sprintf("векторов пересчитано %d", embedded)
}

// splitFlags раскладывает аргументы на флаги и позиционные: стандартный
// flag-пакет прекращает разбор на первом позиционном, а README допускает
// `search "запрос" --mode ...`. Флаги со значением забирают следующий
// аргумент (кроме bool-флагов и формы --flag=value).
func splitFlags(fs *flag.FlagSet, args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // неизвестный флаг — пусть flag.Parse отрапортует
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, positional
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	project := fs.String("project", ".", "корень проекта")
	mode := fs.String("mode", "hybrid+rerank", "fts|vec|hybrid|hybrid+rerank|hybrid+blend")
	top := fs.Int("top", 5, "сколько результатов показать")
	content := fs.Bool("content", false, "печатать содержимое чанков")
	layaKind, layaDir := layaFlags(fs)
	sf := storeFlags(fs)
	flagArgs, positional := splitFlags(fs, args)
	_ = fs.Parse(flagArgs)
	query := strings.Join(positional, " ")
	if query == "" {
		return fmt.Errorf("пустой запрос: codepilot search \"запрос\"")
	}
	ix, cleanup, err := loadIndexStore(*project, *sf.store, *sf.dsn, *sf.embed, *sf.embedDir, *sf.embedServer, *sf.maxThreads, mustProviders(sf))
	if err != nil {
		return err
	}
	defer cleanup()
	scorer := resolveScorer(*layaKind, *layaDir, *sf.maxThreads, mustProviders(sf))
	defer closeScorer(scorer)
	hits, err := ix.Search(query, *mode, *top, scorer)
	if err != nil {
		return err
	}
	noul := scorer.Noul(query, hits)
	fmt.Printf("mode=%s noul=%.2f need_more=%v\n\n", *mode, noul, noul < 0.5)
	for i, h := range hits {
		c := h.Chunk
		fmt.Printf("[%d] %.4f  %s:%d-%d  %s %s\n", i+1, h.Score, c.FilePath, c.StartLine, c.EndLine, c.Kind, c.SymbolName)
		if c.Signature != "" {
			fmt.Printf("    %s\n", c.Signature)
		}
		if c.Doc != "" {
			fmt.Printf("    doc: %s\n", c.Doc)
		}
		if *content {
			fmt.Printf("    ---\n")
			for _, line := range strings.Split(c.Content, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	project := fs.String("project", "", "корень проекта (однопроектный режим); пусто — мультипроектный демон по ~/.codepilot")
	logPath := fs.String("log", "mcp-calls.jsonl", "jsonl-лог вызовов инструментов (пустая строка — без лога)")
	layaKind, layaDir := layaFlags(fs)
	sf := storeFlags(fs)
	_ = fs.Parse(args)
	if *project != "" {
		// Однопроектный режим (обратная совместимость со старыми mcp.json).
		ix, _, err := loadIndexStore(*project, *sf.store, *sf.dsn, *sf.embed, *sf.embedDir, *sf.embedServer, *sf.maxThreads, mustProviders(sf))
		if err != nil {
			return err
		}
		scorer := resolveScorer(*layaKind, *layaDir, *sf.maxThreads, mustProviders(sf))
		// stdout — канал протокола MCP, всё служебное только в stderr.
		fmt.Fprintf(os.Stderr, "codepilot serve: проект %s, чанков %d; слушаю stdio\n", ix.ProjectRoot, len(ix.Chunks))
		// Ресурсы ONNX/pg при выходе сознательно не закрываем (см. serveMulti).
		return mcp.Serve(ix, *logPath, os.Stdin, os.Stdout, scorer)
	}
	return serveMulti(fs, *logPath, layaKind, layaDir, sf)
}

// serveMulti — мультипроектный MCP-демон: настройки из ~/.codepilot/config.json
// (явно заданные флаги перекрывают), проекты — из projects.json (перечитывается
// на каждый вызов: панель может добавить проект, пока serve жив). Индексы и
// эмбеддеры подключаются лениво и кэшируются на всё время жизни процесса.
func serveMulti(fs *flag.FlagSet, logPath string, layaKind, layaDir *string, sf storeFlagsT) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Эффективные настройки: явный флаг > конфиг панели > дефолт флага (env).
	store, dsn := *sf.store, *sf.dsn
	embedKind, embedDir, embedServer := *sf.embed, *sf.embedDir, *sf.embedServer
	device, threads := *sf.device, *sf.maxThreads
	layaK, layaD := *layaKind, *layaDir
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["store"] && cfg.Store != "" {
		store = cfg.Store
	}
	if !set["pg-dsn"] && cfg.PGDSN != "" {
		dsn = cfg.PGDSN
	}
	if !set["embed"] && cfg.Embed != "" {
		embedKind = cfg.Embed
	}
	if !set["embed-dir"] && cfg.EmbedDir != "" {
		embedDir = cfg.EmbedDir
	}
	if !set["embed-server"] && cfg.EmbedServer != "" {
		embedServer = cfg.EmbedServer
	}
	if !set["device"] && cfg.Device != "" {
		device = cfg.Device
	}
	if !set["max-threads"] && cfg.MaxThreads != 0 {
		threads = cfg.MaxThreads
	}
	if !set["laya"] && cfg.Laya != "" {
		layaK = cfg.Laya
	}
	if !set["laya-dir"] && cfg.LayaDir != "" {
		layaD = cfg.LayaDir
	}
	// Относительные каталоги моделей резолвим от каталога бинаря: агент
	// запускает serve из произвольной рабочей директории.
	execDir := ""
	if exe, err := os.Executable(); err == nil {
		execDir = filepath.Dir(exe)
	}
	absDir := func(dir string) string {
		if dir == "" || filepath.IsAbs(dir) || execDir == "" {
			return dir
		}
		return filepath.Join(execDir, dir)
	}
	embedDir, layaD = absDir(embedDir), absDir(layaD)
	providers, err := embed.ProvidersForDevice(device)
	if err != nil {
		return err
	}

	projects, err := config.LoadProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fmt.Errorf("реестр проектов пуст (~/.codepilot/projects.json): добавьте проект через панель (codepilot web) или запустите serve --project <path>")
	}

	// Общие ресурсы pg-режима: одно подключение и один эмбеддер на демон.
	// При выходе (EOF на stdin) их НЕ закрываем: фоновый Laya-реранк после
	// таймаута search_code может ещё идти, и Close сессии под работающим
	// ORT Run — access violation в нативном коде (чистит ОС при exit).
	var sharedPG *index.PGStore
	var sharedEmb embed.TextEmbedder
	if store == "pg" {
		sharedPG, err = index.OpenPG(dsn)
		if err != nil {
			return err
		}
		if embedServer != "" {
			sharedEmb = embed.NewRemoteEmbedder(embedServer)
		} else if embedKind == "onnx" {
			e, err := embed.Load(embedDir, threads, providers...)
			if err != nil {
				return fmt.Errorf("эмбеддер e5: %w", err)
			}
			sharedEmb = e
		}
	}

	var mu sync.Mutex
	cache := map[string]*index.Index{}
	resolve := func(nameOrPath string) (*index.Index, error) {
		list, err := config.LoadProjects() // реестр живой: панель может дописать
		if err != nil {
			return nil, err
		}
		p, ok := config.Find(list, nameOrPath, cfg.Default)
		if !ok {
			names := make([]string, 0, len(list))
			for _, pr := range list {
				names = append(names, pr.Name)
			}
			return nil, fmt.Errorf("проект %q не найден; доступные: %s", nameOrPath, strings.Join(names, ", "))
		}
		abs, err := filepath.Abs(p.Path)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if ix, ok := cache[abs]; ok {
			return ix, nil
		}
		var ix *index.Index
		if store == "pg" {
			ix, err = sharedPG.Load(abs)
			if err != nil {
				return nil, fmt.Errorf("проект %s: %w", p.Name, err)
			}
			ix.PG = sharedPG
			ix.Emb = sharedEmb
		} else {
			path := index.IndexPath(abs)
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("проект %s: индекс не найден (%s); запустите индексацию через панель", p.Name, path)
			}
			ix, err = index.Load(path)
			if err != nil {
				return nil, fmt.Errorf("проект %s: %w", p.Name, err)
			}
		}
		cache[abs] = ix
		fmt.Fprintf(os.Stderr, "codepilot serve: проект %s (%s), чанков %d\n", p.Name, abs, len(ix.Chunks))
		return ix, nil
	}
	listProjects := func() []mcp.Project {
		list, _ := config.LoadProjects()
		out := make([]mcp.Project, 0, len(list))
		for _, p := range list {
			out = append(out, mcp.Project{Name: p.Name, Path: p.Path})
		}
		return out
	}

	scorer := resolveScorer(layaK, layaD, threads, providers)

	// Триггер переиндексации от агента (инструмент reindex): инкрементальный
	// прогон в фоне, по завершении кэш индекса сбрасывается — следующий
	// запрос поднимет свежий. Одна переиндексация за раз.
	var reindexBusy atomic.Bool
	reindexFn := func(nameOrPath string) error {
		list, err := config.LoadProjects()
		if err != nil {
			return err
		}
		p, ok := config.Find(list, nameOrPath, cfg.Default)
		if !ok {
			names := make([]string, 0, len(list))
			for _, pr := range list {
				names = append(names, pr.Name)
			}
			return fmt.Errorf("проект %q не найден; доступные: %s", nameOrPath, strings.Join(names, ", "))
		}
		if !reindexBusy.CompareAndSwap(false, true) {
			return fmt.Errorf("переиндексация уже идёт, повторите позже")
		}
		abs, err := filepath.Abs(p.Path)
		if err != nil {
			reindexBusy.Store(false)
			return err
		}
		go func() {
			defer reindexBusy.Store(false)
			index.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
			var prev *index.Index
			if store == "pg" {
				prev = sharedPG.Prev(abs)
			}
			ix, st, err := index.BuildPrev(abs, prev)
			if err != nil {
				fmt.Fprintf(os.Stderr, "reindex %s: %v\n", p.Name, err)
				return
			}
			if store == "pg" {
				if _, err := sharedPG.Save(ix, sharedEmb); err != nil {
					fmt.Fprintf(os.Stderr, "reindex %s: сохранение: %v\n", p.Name, err)
					return
				}
			} else if err := ix.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "reindex %s: сохранение: %v\n", p.Name, err)
				return
			}
			mu.Lock()
			delete(cache, abs)
			mu.Unlock()
			fmt.Fprintf(os.Stderr, "reindex %s: файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d)\n",
				p.Name, st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed)
		}()
		return nil
	}

	mcpSrv := mcp.NewServer(resolve, listProjects, cfg.Default, scorer)
	mcpSrv.SetReindex(reindexFn)
	fmt.Fprintf(os.Stderr, "codepilot serve: мультипроектный режим, проектов %d, дефолт %q; слушаю stdio\n",
		len(projects), cfg.Default)
	return mcp.RunServer(mcpSrv, logPath, os.Stdin, os.Stdout)
}

// cmdWeb — локальная веб-панель: проекты, индексация, статистика MCP-вызовов.
// stdout у web не протокольный, но служебный вывод всё равно идёт в stderr.
// Эффективные настройки (явные флаги поверх ~/.codepilot/config.json)
// сохраняются обратно в конфиг — serve без флагов подхватывает их оттуда.
func cmdWeb(args []string) error {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "адрес HTTP-сервера панели")
	logPath := fs.String("log", "mcp-calls.jsonl", "jsonl-лог вызовов инструментов (для статистики нагрузки)")
	layaKind, layaDir := layaFlags(fs)
	sf := storeFlags(fs)
	_ = fs.Parse(args)
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	// mergeStr: явный флаг → в конфиг; иначе конфиг перекрывает дефолт флага.
	// Эффективное значение всегда фиксируется в конфиге — он самодостаточен
	// для serve без флагов (в т.ч. дефолтный pg-dsn).
	mergeStr := func(name string, flagVal *string, cfgVal *string) string {
		if set[name] {
			*cfgVal = *flagVal
			return *flagVal
		}
		if *cfgVal != "" {
			return *cfgVal
		}
		*cfgVal = *flagVal
		return *flagVal
	}
	store := mergeStr("store", sf.store, &cfg.Store)
	dsn := mergeStr("pg-dsn", sf.dsn, &cfg.PGDSN)
	embedKind := mergeStr("embed", sf.embed, &cfg.Embed)
	embedDir := mergeStr("embed-dir", sf.embedDir, &cfg.EmbedDir)
	embedServer := mergeStr("embed-server", sf.embedServer, &cfg.EmbedServer)
	layaK := mergeStr("laya", layaKind, &cfg.Laya)
	layaD := mergeStr("laya-dir", layaDir, &cfg.LayaDir)
	device := mergeStr("device", sf.device, &cfg.Device)
	threads := *sf.maxThreads
	if set["max-threads"] {
		cfg.MaxThreads = threads
	} else if cfg.MaxThreads != 0 {
		threads = cfg.MaxThreads
	} else {
		cfg.MaxThreads = threads
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: не удалось сохранить config.json: %v\n", err)
	}
	return web.Serve(*addr, web.Options{
		Store:       store,
		PGDSN:       dsn,
		Embed:       embedKind,
		EmbedDir:    embedDir,
		EmbedServer: embedServer,
		Laya:        layaK,
		LayaDir:     layaD,
		Device:      device,
		MaxThreads:  threads,
		LogPath:     *logPath,
		BinPath:     bin,
	})
}

func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	project := fs.String("project", "sample_project", "корень проекта")
	dataset := fs.String("dataset", "eval/golden_dataset.json", "золотой датасет")
	report := fs.String("report", "eval/report.md", "куда писать отчёт")
	top := fs.Int("top", 5, "topK поиска")
	layaKind, layaDir := layaFlags(fs)
	sf := storeFlags(fs)
	_ = fs.Parse(args)
	ix, ds, scorer, cleanup, err := setupRun(*project, *dataset, *layaKind, *layaDir, sf)
	if err != nil {
		return err
	}
	defer cleanup()
	defer closeScorer(scorer)
	results, err := eval.Run(ix, ds, scorer, *top)
	if err != nil {
		return err
	}
	eval.Print(os.Stdout, results)
	if err := eval.WriteReport(*report, results); err != nil {
		return err
	}
	fmt.Printf("\nотчёт записан в %s\n", *report)
	return nil
}

func cmdBench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	project := fs.String("project", "sample_project", "корень проекта")
	dataset := fs.String("dataset", "eval/golden_dataset.json", "золотой датасет")
	top := fs.Int("top", 5, "topK поиска")
	layaKind, layaDir := layaFlags(fs)
	sf := storeFlags(fs)
	_ = fs.Parse(args)
	ix, ds, scorer, cleanup, err := setupRun(*project, *dataset, *layaKind, *layaDir, sf)
	if err != nil {
		return err
	}
	defer cleanup()
	defer closeScorer(scorer)
	results, err := bench.Run(ix, ds, scorer, *top)
	if err != nil {
		return err
	}
	fmt.Println("task\tbaseline\tRAG\tratio\tneed_more")
	for _, r := range results {
		fmt.Printf("%s\t%d\t%d\t%.2fx\t%v\n", r.ID, r.BaselineTokens, r.RAGTokens, r.Ratio, r.NeedMore)
	}
	mean, median := bench.MeanMedianRatio(results)
	fmt.Printf("\nсреднее %.2fx, медиана %.2fx (токены baseline/RAG)\n", mean, median)
	return nil
}

// setupRun — общая подготовка eval/bench: индекс, датасет, слой решений.
func setupRun(project, dataset, layaKind, layaDir string, sf storeFlagsT) (*index.Index, *eval.Dataset, laya.Scorer, func(), error) {
	ix, cleanup, err := loadIndexStore(project, *sf.store, *sf.dsn, *sf.embed, *sf.embedDir, *sf.embedServer, *sf.maxThreads, mustProviders(sf))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ds, err := eval.LoadDataset(dataset)
	if err != nil {
		cleanup()
		return nil, nil, nil, nil, err
	}
	return ix, ds, resolveScorer(layaKind, layaDir, *sf.maxThreads, mustProviders(sf)), cleanup, nil
}
