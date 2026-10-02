package index

import "testing"

func hitsWithScores(scores ...float64) []SearchHit {
	out := make([]SearchHit, len(scores))
	for i, s := range scores {
		out[i] = SearchHit{Score: s}
	}
	return out
}

// TestFilterRelevant — адаптивная отсечка: отрыв от топ-1 и абсолютный пол.
// Распределения — из замеров на реальном проекте (pnodes, top-20).
func TestFilterRelevant(t *testing.T) {
	cases := []struct {
		name   string
		scores []float64
		want   int
	}{
		{"лидер с отрывом — только он", []float64{0.65, 0.59, 0.56, 0.56, 0.55}, 1},
		{"плотный топ держится", []float64{0.66, 0.63, 0.62, 0.56, 0.54}, 3},
		{"плато шума отрезано полом", []float64{0.55, 0.49, 0.49, 0.48, 0.47}, 1},
		{"всё выше отсечки — ничего не режем", []float64{0.62, 0.60, 0.59, 0.58}, 4},
		{"один хит остаётся как есть", []float64{0.3}, 1},
		{"пусто", nil, 0},
		{"слабый лидер не тянет хвост", []float64{0.51, 0.50, 0.50, 0.49}, 1},
	}
	for _, tc := range cases {
		if got := len(FilterRelevant(hitsWithScores(tc.scores...))); got != tc.want {
			t.Errorf("%s: получено %d хитов, ожидалось %d", tc.name, got, tc.want)
		}
	}
}

// TestFilterRelevantKeepsTopOne — даже полностью шумовая выдача отдаёт топ-1:
// пустой ответ хуже слабого кандидата (дальше решает Noul-гейт).
func TestFilterRelevantKeepsTopOne(t *testing.T) {
	got := FilterRelevant(hitsWithScores(0.2, 0.1, 0.1))
	if len(got) != 1 || got[0].Score != 0.2 {
		t.Fatalf("ожидался один лучший хит, получено %+v", got)
	}
}
