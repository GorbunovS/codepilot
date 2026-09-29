// Command codepilot — CLI: индексация проекта, отладочный поиск,
// MCP stdio-сервер для агента, eval по золотому датасету и bench токенов.
//
//	codepilot index sample_project
//	codepilot search "где проверяется токен" --mode hybrid+rerank --top 5
//	codepilot serve --project sample_project
//	codepilot eval [--laya onnx]
//	codepilot bench [--laya onnx]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codepilot/internal/bench"
	"codepilot/internal/eval"
	"codepilot/internal/index"
	"codepilot/internal/laya"
	"codepilot/internal/mcp"
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

Флаги search: --project . --mode hybrid+rerank --top 5 --content [--laya onnx]
Флаги serve:  --project . --log mcp-calls.jsonl [--laya onnx]
Флаги eval:   --project sample_project --dataset eval/golden_dataset.json [--laya onnx]
Флаги bench:  --project sample_project --dataset eval/golden_dataset.json [--laya onnx]

Слой решений Laya: --laya heuristic (по умолчанию) | --laya onnx
  [--laya-dir models/laya-multilingual]; также читается CODEPILOT_LAYA.
Режимы поиска: fts (BM25), vec (TF-IDF cosine), hybrid (RRF k=60),
hybrid+rerank (порядок задаёт Laya), hybrid+blend (0.5·Laya + 0.5·RRF).
`)
}

// layaFlags — общие флаги слоя решений для search/serve/eval/bench.
func layaFlags(fs *flag.FlagSet) (kind, dir *string) {
	kind = fs.String("laya", "", "движок слоя решений: heuristic|onnx (по умолчанию CODEPILOT_LAYA или heuristic)")
	dir = fs.String("laya-dir", "", "каталог модели Laya (по умолчанию models/laya-multilingual)")
	return kind, dir
}

func resolveScorer(kind, dir string) laya.Scorer {
	return laya.Resolve(kind, dir)
}

// closeScorer освобождает ресурсы ONNX-модели (у эвристики их нет).
func closeScorer(s laya.Scorer) {
	if c, ok := s.(interface{ Close() }); ok {
		c.Close()
	}
}

// loadIndex открывает индекс проекта или подсказывает про codepilot index.
func loadIndex(project string) (*index.Index, error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	path := index.IndexPath(abs)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("индекс не найден (%s); сначала выполните: codepilot index %s", path, project)
	}
	return index.Load(path)
}

func cmdIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	project := fs.String("project", "", "корень проекта (приоритетнее позиционного аргумента)")
	_ = fs.Parse(args)
	root := *project
	if root == "" && fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	if root == "" {
		root = "."
	}
	ix, st, err := index.Build(root)
	if err != nil {
		return err
	}
	if err := ix.Save(); err != nil {
		return fmt.Errorf("сохранение индекса: %w", err)
	}
	fmt.Printf("%s: файлов %d, чанков %d (переиндексировано %d, без изменений %d, удалено %d)\n",
		ix.ProjectRoot, st.Files, st.Chunks, st.Reindexed, st.Kept, st.Removed)
	return nil
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
	flagArgs, positional := splitFlags(fs, args)
	_ = fs.Parse(flagArgs)
	query := strings.Join(positional, " ")
	if query == "" {
		return fmt.Errorf("пустой запрос: codepilot search \"запрос\"")
	}
	ix, err := loadIndex(*project)
	if err != nil {
		return err
	}
	scorer := resolveScorer(*layaKind, *layaDir)
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
	project := fs.String("project", ".", "корень проекта")
	logPath := fs.String("log", "mcp-calls.jsonl", "jsonl-лог вызовов инструментов (пустая строка — без лога)")
	layaKind, layaDir := layaFlags(fs)
	_ = fs.Parse(args)
	ix, err := loadIndex(*project)
	if err != nil {
		return err
	}
	scorer := resolveScorer(*layaKind, *layaDir)
	defer closeScorer(scorer)
	// stdout — канал протокола MCP, всё служебное только в stderr.
	fmt.Fprintf(os.Stderr, "codepilot serve: проект %s, чанков %d; слушаю stdio\n", ix.ProjectRoot, len(ix.Chunks))
	return mcp.Serve(ix, *logPath, os.Stdin, os.Stdout, scorer)
}

func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	project := fs.String("project", "sample_project", "корень проекта")
	dataset := fs.String("dataset", "eval/golden_dataset.json", "золотой датасет")
	report := fs.String("report", "eval/report.md", "куда писать отчёт")
	top := fs.Int("top", 5, "topK поиска")
	layaKind, layaDir := layaFlags(fs)
	_ = fs.Parse(args)
	ix, ds, scorer, err := setupRun(*project, *dataset, *layaKind, *layaDir)
	if err != nil {
		return err
	}
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
	_ = fs.Parse(args)
	ix, ds, scorer, err := setupRun(*project, *dataset, *layaKind, *layaDir)
	if err != nil {
		return err
	}
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
func setupRun(project, dataset, layaKind, layaDir string) (*index.Index, *eval.Dataset, laya.Scorer, error) {
	ix, err := loadIndex(project)
	if err != nil {
		return nil, nil, nil, err
	}
	ds, err := eval.LoadDataset(dataset)
	if err != nil {
		return nil, nil, nil, err
	}
	return ix, ds, resolveScorer(layaKind, layaDir), nil
}
