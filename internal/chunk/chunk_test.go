package chunk

import (
	"testing"
)

// TestChunkGoSymbols — снапшот структуры чанков для Go-файла.
func TestChunkGoSymbols(t *testing.T) {
	src := []byte(`// Package demo тестовый пакет.
package demo

// Hello приветствует.
func Hello(name string) string {
	return "hi " + name
}

// Thing — тип.
type Thing struct{ X int }

// Do делает вещь.
func (t Thing) Do() {}
`)
	chunks := chunkGo("demo.go", src)
	syms := map[string]string{}
	for _, c := range chunks {
		syms[c.SymbolName] = c.Kind
	}
	for name, kind := range map[string]string{"Hello": "func", "Thing": "type", "Do": "method"} {
		if syms[name] != kind {
			t.Errorf("символ %s: kind=%q, ожидалось %q (все чанки: %v)", name, syms[name], kind, syms)
		}
	}
}

// TestChunkVueScriptBlock — Vue SFC: script-блок чанкится как JS,
// export default становится component-чанком с именем из файла.
func TestChunkVueScriptBlock(t *testing.T) {
	src := []byte(`<template>
  <div>{{ msg }}</div>
</template>

<script>
// MyWidget — виджет приветствия.
export default {
  name: "MyWidget",
  methods: {
    // greet здоровается.
    greet() { return "hi"; },
  },
};
</script>
`)
	chunks := chunkVue("web/components/MyWidget.vue", src)
	syms := map[string]string{}
	for _, c := range chunks {
		syms[c.SymbolName] = c.Kind
	}
	if syms["MyWidget"] != "component" {
		t.Errorf("нет component-чанка MyWidget: %v", syms)
	}
	if syms["greet"] != "method" {
		t.Errorf("нет method-чанка greet: %v", syms)
	}
	if syms["template"] != "template" {
		t.Errorf("нет template-чанка: %v", syms)
	}
	// doc-комментарий из шапки script-блока прикреплён к компоненту
	for _, c := range chunks {
		if c.SymbolName == "MyWidget" && c.Doc == "" {
			t.Errorf("у component-чанка пустой doc (комментарий шапки потерян)")
		}
	}
}

// TestFallbackWindows — непарсящийся файл режется окнами.
func TestFallbackWindows(t *testing.T) {
	var src []byte
	for i := 0; i < 200; i++ {
		src = append(src, []byte("line\n")...)
	}
	chunks := Fallback("plain.go", "go", src)
	if len(chunks) < 3 {
		t.Errorf("ожидалось >= 3 окна на 200 строк, получено %d", len(chunks))
	}
}
