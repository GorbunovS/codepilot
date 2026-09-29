// Package bench сравнивает расход токенов двух симулированных агентов:
// baseline (grep + чтение файлов целиком) и RAG-агент (search_code + read_span).
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"codepilot/internal/eval"
	"codepilot/internal/index"
	"codepilot/internal/laya"
)

// Result — расход токенов по одной задаче.
type Result struct {
	ID             string
	Question       string
	BaselineTokens int
	RAGTokens      int
	Ratio          float64
	NeedMore       bool
}

// taskIDs — задачи бенча: вопросы датасета на разных языках кода.
var taskIDs = []string{"q01", "q03", "q04", "q05", "q09", "q12"}

const charsPerToken = 4

// Run исполняет бенч на подмножестве вопросов датасета.
func Run(ix *index.Index, ds *eval.Dataset, scorer laya.Scorer, topK int) ([]Result, error) {
	byID := map[string]eval.Question{}
	for _, q := range ds.Questions {
		byID[q.ID] = q
	}
	var tasks []eval.Question
	for _, id := range taskIDs {
		if q, ok := byID[id]; ok {
			tasks = append(tasks, q)
		}
	}
	if len(tasks) == 0 && len(ds.Questions) > 0 {
		tasks = ds.Questions[:min(6, len(ds.Questions))]
	}
	files, err := index.SourceFiles(ix.ProjectRoot)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, q := range tasks {
		base := baselineTokens(q.Question, ix.ProjectRoot, files)
		rag, needMore, err := ragTokens(ix, q.Question, scorer, topK)
		if err != nil {
			return nil, err
		}
		ratio := 0.0
		if rag > 0 {
			ratio = float64(base) / float64(rag)
		}
		out = append(out, Result{
			ID: q.ID, Question: q.Question,
			BaselineTokens: base, RAGTokens: rag, Ratio: ratio, NeedMore: needMore,
		})
	}
	return out, nil
}

// grepTerms — слова запроса длиной > 3 (case-insensitive), как в ТЗ.
func grepTerms(query string) []string {
	var terms []string
	var cur []rune
	flush := func() {
		if len(cur) > 3 {
			terms = append(terms, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return terms
}

// baselineTokens симулирует «read/grep агента»: grep по термам запроса,
// все файлы с совпадениями читаются целиком. Если grep ничего не нашёл,
// агент вынужден читать весь проект. Токены = байты/4.
func baselineTokens(query, root string, files []string) int {
	terms := grepTerms(query)
	var grepOut, full strings.Builder
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		text := string(data)
		lower := strings.ToLower(text)
		matched := false
		for _, t := range terms {
			if strings.Contains(lower, t) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for i, line := range strings.Split(text, "\n") {
			ll := strings.ToLower(line)
			for _, t := range terms {
				if strings.Contains(ll, t) {
					fmt.Fprintf(&grepOut, "%s:%d: %s\n", rel, i+1, line)
					break
				}
			}
		}
		full.WriteString(text)
	}
	if full.Len() == 0 {
		// grep не нашёл ничего — реалистичный следующий шаг агента: читать всё.
		for _, rel := range files {
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				full.Write(data)
			}
		}
	}
	return (grepOut.Len() + full.Len()) / charsPerToken
}

type ragPayload struct {
	Query    string            `json:"query"`
	NeedMore bool              `json:"need_more"`
	Noul     float64           `json:"noul"`
	Results  []index.SearchHit `json:"results"`
}

// ragTokens симулирует RAG-агента: search_code топ-5 (hybrid+rerank),
// при need_more — один read_span расширенного чанка (±15 строк).
// Токены = payload инструментов/4.
func ragTokens(ix *index.Index, query string, scorer laya.Scorer, topK int) (int, bool, error) {
	hits, err := ix.Search(query, "hybrid+blend", topK, scorer)
	if err != nil {
		return 0, false, err
	}
	noul := scorer.Noul(query, hits)
	needMore := noul < 0.5
	payload, err := json.Marshal(ragPayload{Query: query, NeedMore: needMore, Noul: noul, Results: hits})
	if err != nil {
		return 0, false, err
	}
	tokens := len(payload) / charsPerToken
	if needMore && len(hits) > 0 {
		c := hits[0].Chunk
		span, err := index.ReadSpan(ix.ProjectRoot, c.FilePath, c.StartLine-15, c.EndLine+15)
		if err == nil {
			tokens += len(span) / charsPerToken
		}
	}
	return tokens, needMore, nil
}

// MeanMedianRatio — среднее и медиана ratio по задачам.
func MeanMedianRatio(results []Result) (mean, median float64) {
	if len(results) == 0 {
		return 0, 0
	}
	ratios := make([]float64, len(results))
	for i, r := range results {
		ratios[i] = r.Ratio
		mean += r.Ratio
	}
	mean /= float64(len(results))
	sort.Float64s(ratios)
	n := len(ratios)
	if n%2 == 1 {
		median = ratios[n/2]
	} else {
		median = (ratios[n/2-1] + ratios[n/2]) / 2
	}
	return mean, median
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
