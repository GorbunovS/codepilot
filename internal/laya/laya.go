// Package laya — слой решений поверх поиска.
//
// В проде сюда встаёт ONNX-модель convaiinnovations/laya-multilingual
// с примитивами score (релевантность чанка запросу) и noul (гейт
// достаточности контекста). В прототипе модель заменена эвристикой
// за тем же интерфейсом Scorer.
package laya

import (
	"math"
	"strings"

	"codepilot/internal/chunk"
	"codepilot/internal/index"
)

// Scorer — интерфейс слоя решений Laya.
type Scorer interface {
	// Score — релевантность чанка запросу, 0..5.
	Score(query string, c chunk.Chunk) float64
	// Noul — вероятность того, что топа результатов достаточно для ответа, 0..1.
	// Ниже порога 0.5 агенту стоит дочитать контекст (need_more).
	Noul(query string, top []index.SearchHit) float64
}

// Heuristic — эвристическая реализация Scorer для MVP.
type Heuristic struct{}

var _ Scorer = Heuristic{}
var _ index.Scorer = Heuristic{}

// Score: покрытие термов запроса (symbol вес 3, signature вес 2, content вес 1),
// бонус за точное совпадение имени символа, штраф за гигантские чанки.
// Нормируется в 0..5.
func (Heuristic) Score(query string, c chunk.Chunk) float64 {
	terms := map[string]bool{}
	for _, t := range index.QueryTerms(query) {
		terms[t] = true
	}
	if len(terms) == 0 {
		return 0
	}
	sym := termSet(c.SymbolName)
	sig := termSet(c.Signature)
	body := termSet(c.FilePath + " " + c.Doc + " " + c.Content)
	var sum float64
	for t := range terms {
		switch {
		case sym[t]:
			sum += 3
		case sig[t]:
			sum += 2
		case body[t]:
			sum += 1
		}
	}
	score := 5 * sum / (3 * float64(len(terms)))
	if s := strings.ToLower(c.SymbolName); len(s) > 2 && strings.Contains(strings.ToLower(query), s) {
		score += 1.5 // точное совпадение имени символа в запросе
	}
	if lines := c.EndLine - c.StartLine + 1; lines > 120 {
		score *= 0.7
	}
	if score > 5 {
		score = 5
	}
	return score
}

// Noul: уверенность, что топа достаточно — от абсолютного уровня top-1
// и его отрыва от остальных (gap). Откалибровано под эвристический Score
// (NL-запросы дают 0.5..1.5, точное имя символа в запросе — 2.5+).
func (Heuristic) Noul(query string, top []index.SearchHit) float64 {
	if len(top) == 0 {
		return 0
	}
	top1 := top[0].Score
	var rest float64
	n := len(top)
	if n > 5 {
		n = 5
	}
	for i := 1; i < n; i++ {
		rest += top[i].Score
	}
	if n > 1 {
		rest /= float64(n - 1)
	}
	gap := top1 - rest
	z := 1.6*top1 + 1.2*gap - 1.5
	return 1 / (1 + math.Exp(-z))
}

func termSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range index.Tokenize(s) {
		set[t] = true
	}
	return set
}
