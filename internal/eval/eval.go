// Package eval прогоняет золотой датасет через все режимы поиска
// и считает Recall@k и MRR со срезами по языкам.
package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"codepilot/internal/index"
)

// Question — один вопрос золотого датасета.
type Question struct {
	ID              string   `json:"id"`
	Question        string   `json:"question"`
	QuestionLang    string   `json:"question_lang"`
	Type            string   `json:"type"`
	Language        string   `json:"language"`
	ExpectedFiles   []string `json:"expected_files"`
	ExpectedSymbols []string `json:"expected_symbols"`
}

// Dataset — золотой датасет.
type Dataset struct {
	Questions []Question `json:"questions"`
}

// LoadDataset читает golden_dataset.json.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

// Config — одна оцениваемая конфигурация поиска.
type Config struct {
	Label string
	Mode  string
}

// Configs — A/B/C/D/E из методологии оценки.
var Configs = []Config{
	{"A", "fts"},
	{"B", "vec"},
	{"C", "hybrid"},
	{"D", "hybrid+rerank"},
	{"E", "hybrid+blend"},
}

// Failure — вопрос, не найденный в топ-5 конфигурации D.
type Failure struct {
	ID       string
	Question string
	Expected []string
	Got      []string
	Note     string
}

// ModeResult — метрики одной конфигурации.
type ModeResult struct {
	Label      string
	Mode       string
	RecallAt   map[int]float64 // k -> recall
	MRR        float64
	ByCodeLang map[string]float64 // recall@5
	ByQuesLang map[string]float64 // recall@5
	Failures   []Failure
}

var ks = []int{1, 3, 5}

// Run прогоняет все вопросы датасета через все конфигурации.
func Run(ix *index.Index, ds *Dataset, scorer index.Scorer, topK int) ([]*ModeResult, error) {
	results := make([]*ModeResult, 0, len(Configs))
	for _, cfg := range Configs {
		mr := &ModeResult{
			Label:      cfg.Label,
			Mode:       cfg.Mode,
			RecallAt:   map[int]float64{},
			ByCodeLang: map[string]float64{},
			ByQuesLang: map[string]float64{},
		}
		var rrSum float64
		hitsAt := map[int]int{}
		codeLangHit := map[string]int{}
		codeLangN := map[string]int{}
		quesLangHit := map[string]int{}
		quesLangN := map[string]int{}
		for _, q := range ds.Questions {
			hits, err := ix.Search(q.Question, cfg.Mode, topK, scorer)
			if err != nil {
				return nil, err
			}
			rank := firstExpectedRank(hits, q.ExpectedFiles)
			for _, k := range ks {
				if rank >= 1 && rank <= k {
					hitsAt[k]++
				}
			}
			if rank >= 1 {
				rrSum += 1 / float64(rank)
			}
			codeLangN[q.Language]++
			quesLangN[q.QuestionLang]++
			if rank >= 1 && rank <= 5 {
				codeLangHit[q.Language]++
				quesLangHit[q.QuestionLang]++
			}
			if cfg.Mode == "hybrid+rerank" && (rank < 1 || rank > 5) {
				f := Failure{ID: q.ID, Question: q.Question, Expected: q.ExpectedFiles}
				for _, h := range hits {
					f.Got = append(f.Got, fmt.Sprintf("%s:%s", h.Chunk.FilePath, h.Chunk.SymbolName))
				}
				if rank < 1 {
					f.Note = "ожидаемый файл отсутствует в выдаче (нет совпадений термов?)"
				} else {
					f.Note = fmt.Sprintf("ожидаемый файл найден только на позиции %d", rank)
				}
				mr.Failures = append(mr.Failures, f)
			}
		}
		n := float64(len(ds.Questions))
		for _, k := range ks {
			mr.RecallAt[k] = float64(hitsAt[k]) / n
		}
		mr.MRR = rrSum / n
		for lang, total := range codeLangN {
			mr.ByCodeLang[lang] = float64(codeLangHit[lang]) / float64(total)
		}
		for lang, total := range quesLangN {
			mr.ByQuesLang[lang] = float64(quesLangHit[lang]) / float64(total)
		}
		results = append(results, mr)
	}
	return results, nil
}

// firstExpectedRank — позиция первого чанка из ожидаемого файла (0 = не найден).
func firstExpectedRank(hits []index.SearchHit, expected []string) int {
	for i, h := range hits {
		for _, e := range expected {
			if h.Chunk.FilePath == e {
				return i + 1
			}
		}
	}
	return 0
}

// Print печатает сводную таблицу и срезы в w.
func Print(w io.Writer, results []*ModeResult) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "config\tmode\tRecall@1\tRecall@3\tRecall@5\tMRR")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%.2f\t%.2f\t%.2f\t%.2f\n",
			r.Label, r.Mode, r.RecallAt[1], r.RecallAt[3], r.RecallAt[5], r.MRR)
	}
	tw.Flush()
	for _, r := range results {
		fmt.Fprintf(w, "\n%s (%s) — Recall@5 по языку кода: %s; по языку вопроса: %s\n",
			r.Label, r.Mode, formatSlice(r.ByCodeLang), formatSlice(r.ByQuesLang))
	}
}

func formatSlice(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for i, k := range keys {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s %.2f", k, m[k])
	}
	return s
}

// WriteReport пишет eval/report.md: таблица конфигураций, срезы, провалы в D.
func WriteReport(path string, results []*ModeResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, "# CodePilot RAG — eval report")
	fmt.Fprintln(f)
	fmt.Fprintln(f, "| config | mode | Recall@1 | Recall@3 | Recall@5 | MRR |")
	fmt.Fprintln(f, "|---|---|---|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(f, "| %s | %s | %.2f | %.2f | %.2f | %.2f |\n",
			r.Label, r.Mode, r.RecallAt[1], r.RecallAt[3], r.RecallAt[5], r.MRR)
	}
	fmt.Fprintln(f)
	fmt.Fprintln(f, "## Срезы Recall@5")
	fmt.Fprintln(f)
	fmt.Fprintln(f, "| config | по языку кода | по языку вопроса |")
	fmt.Fprintln(f, "|---|---|---|")
	for _, r := range results {
		fmt.Fprintf(f, "| %s (%s) | %s | %s |\n", r.Label, r.Mode, formatSlice(r.ByCodeLang), formatSlice(r.ByQuesLang))
	}
	fmt.Fprintln(f)
	fmt.Fprintln(f, "## Провалы конфигурации D (hybrid+rerank)")
	fmt.Fprintln(f)
	var fails []Failure
	for _, r := range results {
		if r.Mode == "hybrid+rerank" {
			fails = r.Failures
		}
	}
	if len(fails) == 0 {
		fmt.Fprintln(f, "Провалов нет: все вопросы найдены в топ-5.")
	} else {
		for _, fl := range fails {
			fmt.Fprintf(f, "- **%s** %s\n  - ожидалось: %v\n  - топ-5: %v\n  - причина: %s\n",
				fl.ID, fl.Question, fl.Expected, fl.Got, fl.Note)
		}
	}
	return nil
}
