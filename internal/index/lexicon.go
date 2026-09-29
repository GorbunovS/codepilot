package index

// Доменный лексикон синонимов (RU+EN) для query expansion — аналог
// тезауруса ts_thesaurus в Postgres, зафиксированного в ТЗ как часть
// FTS-канала. Расширяется по мере накопления логов промахов.
var lexicon = map[string][]string{
	"frontend":   {"web", "ui", "client"},
	"backend":    {"server", "api"},
	"send":       {"fetch", "post", "request"},
	"отправ":     {"fetch", "post", "send"},
	"create":     {"new", "make"},
	"созда":      {"create", "new"},
	"error":      {"ошибк", "fail"},
	"ошибк":      {"error", "fail"},
	"user":       {"пользовател"},
	"пользовател": {"user"},
	"token":      {"токен"},
	"токен":      {"token"},
	"order":      {"заказ"},
	"заказ":      {"order"},
	"invoice":    {"счёт", "счет", "bill"},
	"счёт":       {"invoice", "bill"},
	"счет":       {"invoice", "bill"},
	"payment":    {"оплат", "платёж", "платеж"},
	"оплат":      {"payment", "pay"},
	"price":      {"цен"},
	"цен":        {"price", "cost"},
	"discount":   {"скидк"},
	"скидк":      {"discount"},
}

// expandQuery дополняет термы запроса синонимами из лексикона.
// Синонимы добавляются как обычные термы: редкие получат высокий IDF,
// мусорные — низкий, поэтому расширение безопасно для BM25/TF-IDF.
func expandQuery(terms []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		for _, syn := range lexicon[t] {
			if !seen[syn] {
				seen[syn] = true
				out = append(out, syn)
			}
		}
	}
	return out
}
