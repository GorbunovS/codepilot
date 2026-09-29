package index

import "testing"

func TestTokenizeSplitsIdentifiers(t *testing.T) {
	toks := Tokenize("ValidateToken")
	want := map[string]bool{"validatetoken": true, "validate": true, "token": true}
	for _, tok := range toks {
		delete(want, tok)
	}
	if len(want) > 0 {
		t.Errorf("не хватает токенов: %v (получено %v)", want, toks)
	}
}

func TestTokenizeSnakeCase(t *testing.T) {
	toks := Tokenize("apply_vat")
	seen := map[string]bool{}
	for _, tok := range toks {
		seen[tok] = true
	}
	for _, w := range []string{"apply_vat", "apply", "vat"} {
		if !seen[w] {
			t.Errorf("нет токена %q в %v", w, toks)
		}
	}
}

func TestStemRussian(t *testing.T) {
	cases := map[string]string{
		"проверяется": "проверя", // рефлексивный глагол
		"карточке":    "карточк",
		"заказа":      "заказ",
	}
	for in, want := range cases {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestStemShortWordsUntouched(t *testing.T) {
	for _, w := range []string{"vat", "для", "go"} {
		if got := stem(w); got != w {
			t.Errorf("stem(%q) = %q, короткие слова не трогаем", w, got)
		}
	}
}

func TestExpandQueryBilingual(t *testing.T) {
	out := expandQuery([]string{"заказ"})
	seen := map[string]bool{}
	for _, tok := range out {
		seen[tok] = true
	}
	if !seen["заказ"] || !seen["order"] {
		t.Errorf("расширение заказ->order не сработало: %v", out)
	}
}
