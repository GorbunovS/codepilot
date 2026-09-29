package index

import (
	"strings"
	"unicode"
)

// Tokenize разбивает текст на слова (unicode, работает для RU+EN), приводит
// к lowercase, дробит идентификаторы snake_case/camelCase на составные части
// (оригинал сохраняется) и добавляет лёгкую стеммированную форму слова —
// чтобы «оплаты» находило «оплату», а «calculated» — «CalculateDiscount».
func Tokenize(s string) []string {
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, splitIdent(string(cur))...)
			cur = cur[:0]
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func splitIdent(ident string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		w = strings.ToLower(w)
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
		if st := stem(w); st != w {
			seen[st] = true
			out = append(out, st)
		}
	}
	add(ident)
	for _, seg := range strings.Split(ident, "_") {
		add(seg)
		var cur []rune
		prevType := 0 // 1 lower, 2 upper, 3 digit
		flushSeg := func() {
			if len(cur) > 0 {
				add(string(cur))
				cur = cur[:0]
			}
		}
		runes := []rune(seg)
		for i, r := range runes {
			t := 1
			switch {
			case unicode.IsUpper(r):
				t = 2
			case unicode.IsDigit(r):
				t = 3
			}
			boundary := false
			if len(cur) > 0 {
				switch {
				case t == 3 || prevType == 3:
					boundary = t != prevType
				case t == 2 && prevType == 1:
					boundary = true
				case t == 2 && prevType == 2 && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
					boundary = true // конец аббревиатуры: HTMLParser -> HTML, Parser
				}
			}
			if boundary {
				flushSeg()
			}
			cur = append(cur, r)
			prevType = t
		}
		flushSeg()
	}
	return out
}

var ruSuffixes = []string{
	"иями", "ями", "ами", "ого", "его", "ому", "ему", "ыми", "ими",
	"иях", "ять", "ует", "ает", "ится", "иться",
	"ость", "есть", "иям", "ять", "ать", "ять", "еть", "ить", "оть", "ути",
	"ешь", "ишь", "ете", "ите", "ёт", "ет", "ит", "ат", "ят", "ем", "им",
	"ом", "ам", "ям", "ах", "ях", "ов", "ев", "ей", "ой", "ая", "яя",
	"ое", "ее", "ие", "ые", "ий", "ый", "а", "я", "о", "е", "у", "ю",
	"ы", "и", "ь", "й",
}

var enSuffixes = []string{
	"ingly", "edly", "ing", "ed", "ies", "es", "s", "ly", "ers", "er", "e",
}

// stem — очень грубый стеммер RU/EN: снимает один суффлекс,
// если остаток не короче 3 букв. Слова до 4 букв не трогаем.
func stem(w string) string {
	r := []rune(w)
	if len(r) <= 4 || !unicode.IsLetter(r[0]) {
		return w
	}
	cut := func(suf string) string {
		if strings.HasSuffix(w, suf) {
			if rest := w[:len(w)-len(suf)]; len([]rune(rest)) >= 3 {
				return rest
			}
		}
		return ""
	}
	// рефлексивные глаголы: проверяется -> проверяет
	if strings.HasSuffix(w, "ся") || strings.HasSuffix(w, "сь") {
		w = w[:len(w)-len("ся")]
	}
	if r[0] < 128 {
		for _, suf := range enSuffixes {
			if s := cut(suf); s != "" {
				return s
			}
		}
		return w
	}
	for _, suf := range ruSuffixes {
		if s := cut(suf); s != "" {
			return s
		}
	}
	return w
}

func uniqTokens(tokens []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
