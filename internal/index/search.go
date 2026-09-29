package index

import (
	"fmt"
	"math"
	"sort"

	"codepilot/internal/chunk"
)

// SearchHit — чанк с итоговым скором.
type SearchHit struct {
	Chunk chunk.Chunk `json:"chunk"`
	Score float64     `json:"score"`
}

// Scorer — минимальный интерфейс реранкера (его реализует слой Laya).
type Scorer interface {
	Score(query string, c chunk.Chunk) float64 // 0..5
}

// BatchScorer — опциональное расширение Scorer: батчевый скоринг пула
// одним вызовом (ONNX-инференс всех кандидатов за один прогон модели).
type BatchScorer interface {
	ScoreBatch(query string, cs []chunk.Chunk) ([]float64, error)
}

const (
	bm25K1      = 1.5
	bm25B       = 0.75
	rrfK        = 60.0
	rrfListSize = 50
	rerankPool  = 20
)

// QueryTerms — термы запроса после токенизации и лексиконного расширения.
// Единая точка входа: поиск и слой Laya обязаны видеть один и тот же запрос.
func QueryTerms(query string) []string {
	return expandQuery(uniqTokens(Tokenize(query)))
}

// Search выполняет поиск в одном из режимов:
// "fts" (BM25), "vec" (TF-IDF cosine; при подключённых PG+Emb — e5+pgvector),
// "hybrid" (RRF слияние), "hybrid+rerank"/"hybrid+blend" (реранк слоем решений).
func (ix *Index) Search(query, mode string, topK int, scorer Scorer) ([]SearchHit, error) {
	qt := QueryTerms(query)
	switch mode {
	case "fts":
		return ix.topHits(ix.bm25(qt), topK), nil
	case "vec":
		if ix.PG != nil && ix.Emb != nil {
			return ix.pgVecSearch(query, topK)
		}
		return ix.topHits(ix.cosine(qt), topK), nil
	case "hybrid":
		return ix.hybrid(query, qt, topK), nil
	case "hybrid+rerank":
		// Чистый реранк слоем решений: порядок полностью задаёт Laya.
		return ix.rerank(ix.hybrid(query, qt, rerankPool), query, topK, scorer, 1.0)
	case "hybrid+blend":
		// Смесь alpha*Laya + (1-alpha)*RRF: пока Laya zero-shot, retrieval-ранк
		// удерживает точность топ-1; после файнтюна alpha можно поднять.
		return ix.rerank(ix.hybrid(query, qt, rerankPool), query, topK, scorer, blendAlpha)
	default:
		return nil, fmt.Errorf("unknown search mode %q", mode)
	}
}

// pgVecSearch — векторный поиск через pgvector: сырой запрос -> e5 -> ANN.
func (ix *Index) pgVecSearch(query string, k int) ([]SearchHit, error) {
	vecs, err := ix.Emb.EmbedQueries([]string{query})
	if err != nil {
		return nil, err
	}
	vhits, err := ix.PG.VecSearch(ix.ProjectRoot, vecs[0], k)
	if err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(vhits))
	for _, vh := range vhits {
		if d, ok := ix.byID[vh.ID]; ok {
			hits = append(hits, SearchHit{Chunk: ix.Chunks[d], Score: vh.Score})
		}
	}
	return hits, nil
}

// vecRanked — топ-k индексов чанков векторным поиском для RRF-слияния:
// pgvector при наличии, иначе легаси TF-IDF cosine.
func (ix *Index) vecRanked(query string, qt []string, k int) []int {
	if ix.PG != nil && ix.Emb != nil {
		if hits, err := ix.pgVecSearch(query, k); err == nil {
			ids := make([]int, 0, len(hits))
			for _, h := range hits {
				ids = append(ids, ix.byID[h.Chunk.ID])
			}
			return ids
		}
	}
	return ranked(ix.cosine(qt), k)
}

// blendAlpha — вес Laya в режиме hybrid+blend (0..1).
const blendAlpha = 0.5

// rerank пересортировывает пул: alpha=1 — чистый скор Laya, иначе смесь
// нормированных скоров Laya (0..5 -> 0..1) и RRF (сохраняет сигнал retrieval).
func (ix *Index) rerank(pool []SearchHit, query string, topK int, scorer Scorer, alpha float64) ([]SearchHit, error) {
	if scorer == nil || len(pool) == 0 {
		return firstK(pool, topK), nil
	}
	var layaScores []float64
	if bs, ok := scorer.(BatchScorer); ok {
		cs := make([]chunk.Chunk, len(pool))
		for i, h := range pool {
			cs[i] = h.Chunk
		}
		scores, err := bs.ScoreBatch(query, cs)
		if err != nil {
			return nil, fmt.Errorf("rerank: %w", err)
		}
		layaScores = scores
	} else {
		layaScores = make([]float64, len(pool))
		for i := range pool {
			layaScores[i] = scorer.Score(query, pool[i].Chunk)
		}
	}
	maxRRF := pool[0].Score // пул отсортирован по RRF по убыванию
	for i := range pool {
		layaN := layaScores[i] / 5
		rrfN := 0.0
		if maxRRF > 0 {
			rrfN = pool[i].Score / maxRRF
		}
		pool[i].Score = alpha*layaN + (1-alpha)*rrfN
	}
	sort.SliceStable(pool, func(a, b int) bool { return pool[a].Score > pool[b].Score })
	return firstK(pool, topK), nil
}

func (ix *Index) bm25(qt []string) []float64 {
	scores := make([]float64, len(ix.Chunks))
	if ix.avgdl == 0 {
		return scores
	}
	for _, t := range qt {
		idf, ok := ix.idf[t]
		if !ok {
			continue
		}
		for d, tf := range ix.tf {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			den := f + bm25K1*(1-bm25B+bm25B*float64(ix.dlen[d])/ix.avgdl)
			scores[d] += idf * f * (bm25K1 + 1) / den
		}
	}
	return scores
}

func (ix *Index) cosine(qt []string) []float64 {
	scores := make([]float64, len(ix.Chunks))
	tf := map[string]int{}
	for _, t := range qt {
		tf[t]++
	}
	qv := map[string]float64{}
	var norm float64
	for t, f := range tf {
		idf, ok := ix.idf[t]
		if !ok {
			continue
		}
		w := float64(f) * idf
		qv[t] = w
		norm += w * w
	}
	if norm == 0 {
		return scores
	}
	norm = math.Sqrt(norm)
	for d, v := range ix.vecs {
		var dot float64
		for t, w := range qv {
			dot += (w / norm) * v[t]
		}
		scores[d] = dot
	}
	return scores
}

// hybrid — RRF (k=60) слияние топ-50 BM25 и топ-50 vector.
func (ix *Index) hybrid(query string, qt []string, topK int) []SearchHit {
	acc := map[int]float64{}
	for r, d := range ranked(ix.bm25(qt), rrfListSize) {
		acc[d] += 1 / (rrfK + float64(r) + 1)
	}
	for r, d := range ix.vecRanked(query, qt, rrfListSize) {
		acc[d] += 1 / (rrfK + float64(r) + 1)
	}
	hits := make([]SearchHit, 0, len(acc))
	for d, s := range acc {
		hits = append(hits, SearchHit{Chunk: ix.Chunks[d], Score: s})
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].Score > hits[b].Score })
	return firstK(hits, topK)
}

// ranked возвращает индексы документов по убыванию скора (только > 0), топ-k.
func ranked(scores []float64, k int) []int {
	idx := make([]int, 0, len(scores))
	for d, s := range scores {
		if s > 0 {
			idx = append(idx, d)
		}
	}
	sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	if len(idx) > k {
		idx = idx[:k]
	}
	return idx
}

func (ix *Index) topHits(scores []float64, k int) []SearchHit {
	var hits []SearchHit
	for _, d := range ranked(scores, k) {
		hits = append(hits, SearchHit{Chunk: ix.Chunks[d], Score: scores[d]})
	}
	return hits
}

func firstK(hits []SearchHit, k int) []SearchHit {
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}
