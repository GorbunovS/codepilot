// Package embed — bi-encoder эмбеддинги (multilingual-e5-small, ONNX) для
// векторного поиска. Инференс через vendored onnxruntime-purego (без cgo).
//
// Контракт e5: тексты запросов с префиксом "query: ", документов — "passage: ";
// пулинг — mean по attention mask, затем L2-нормализация (cosine = dot).
package embed

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"codepilot/internal/ortlib"

	ort "github.com/shota3506/onnxruntime-purego/onnxruntime"
	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
)

// Dim — размерность вектора multilingual-e5-small.
const Dim = 384

// MaxTokens — кап длины входа (контракт e5).
const MaxTokens = 512

// ortAPIVersion — версия C API onnxruntime 1.23.x.
const ortAPIVersion = 23

// Embedder — ONNX-энкодер e5.
type Embedder struct {
	rt      *ort.Runtime
	env     *ort.Env
	session *ort.Session
	tok     *tokenizer.Tokenizer

	mu      sync.Mutex // tokenizer и session не потокобезопасны
	skipped int        // текстов, на которых токенизатор упал с паникой
}

// Skipped — сколько текстов было заменено пустыми из-за паники токенизатора.
func (e *Embedder) Skipped() int { return e.skipped }

// ProvidersForDevice мапит --device (cpu|coreml|cuda) в имена провайдеров
// onnxruntime. cpu и пустая строка — дефолт (CPUExecutionProvider).
func ProvidersForDevice(device string) ([]string, error) {
	switch device {
	case "", "cpu":
		return nil, nil
	case "coreml":
		return []string{"CoreMLExecutionProvider"}, nil
	case "cuda":
		return []string{"CUDAExecutionProvider"}, nil
	default:
		return nil, fmt.Errorf("неизвестное устройство %q (cpu|coreml|cuda)", device)
	}
}

// Load загружает модель из dir: tokenizer.json + model.onnx.
// Нативная библиотека onnxruntime ищется через ortlib.Find(dir).
// providers — желаемые execution providers (напр. CoreMLExecutionProvider);
// если запрошенного нет в сборке onnxruntime — предупреждение в stderr и CPU.
func Load(dir string, providers ...string) (*Embedder, error) {
	tokPath := filepath.Join(dir, "tokenizer.json")
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("embed: tokenizer.json не найден в %s: %w", dir, err)
	}
	onnxPath := filepath.Join(dir, "model.onnx")
	if _, err := os.Stat(onnxPath); err != nil {
		return nil, fmt.Errorf("embed: model.onnx не найден в %s: %w", dir, err)
	}
	libPath := ortlib.Find(dir)
	if libPath == "" {
		return nil, fmt.Errorf("embed: onnxruntime не найден (проверены %s, %s, bin/)", ortlib.EnvVar, dir)
	}
	tk, err := pretrained.FromFile(tokPath)
	if err != nil {
		return nil, fmt.Errorf("embed: tokenizer.json: %w", err)
	}
	rt, err := ort.NewRuntime(libPath, ortAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("embed: загрузка %s: %w", libPath, err)
	}
	env, err := rt.NewEnv("codepilot-embed", ort.LoggingLevelWarning)
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("embed: ORT env: %w", err)
	}
	sess, err := rt.NewSession(env, onnxPath, SessionOptions(rt, providers))
	if err != nil {
		env.Close()
		rt.Close()
		return nil, fmt.Errorf("embed: сессия ONNX %s: %w", onnxPath, err)
	}
	return &Embedder{rt: rt, env: env, session: sess, tok: tk}, nil
}

// SessionOptions собирает ort.SessionOptions под запрошенные провайдеры.
// Если провайдера нет в сборке onnxruntime — предупреждение и fallback на CPU
// (onnxruntime сам докинет CPUExecutionProvider в конец списка).
func SessionOptions(rt *ort.Runtime, providers []string) *ort.SessionOptions {
	if len(providers) == 0 {
		return nil
	}
	avail, err := rt.GetAvailableProviders()
	if err == nil {
		set := map[string]bool{}
		for _, p := range avail {
			set[p] = true
		}
		filtered := providers[:0]
		for _, p := range providers {
			if set[p] {
				filtered = append(filtered, p)
			} else {
				fmt.Fprintf(os.Stderr, "ort: провайдер %s недоступен в этой сборке onnxruntime (есть: %v), использую CPU\n", p, avail)
			}
		}
		providers = filtered
	}
	if len(providers) == 0 {
		return nil
	}
	return &ort.SessionOptions{ExecutionProviders: providers}
}

// Close освобождает сессию, окружение и runtime.
func (e *Embedder) Close() {
	if e.session != nil {
		e.session.Close()
	}
	if e.env != nil {
		e.env.Close()
	}
	if e.rt != nil {
		_ = e.rt.Close()
	}
}

// EmbedQueries — векторы поисковых запросов (префикс "query: ").
func (e *Embedder) EmbedQueries(texts []string) ([][]float32, error) {
	return e.embedPrefixed("query: ", texts)
}

// EmbedPassages — векторы документов/чанков (префикс "passage: ").
func (e *Embedder) EmbedPassages(texts []string) ([][]float32, error) {
	return e.embedPrefixed("passage: ", texts)
}

// embedPrefixed токенизирует батч (спецтокены постпроцессором), гоняет
// одним прогоном сессии и возвращает L2-нормированные mean-pooled векторы.
func (e *Embedder) embedPrefixed(prefix string, texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := len(texts)
	if b == 0 {
		return nil, nil
	}
	encs := make([][]int64, b)
	var L int
	for i, t := range texts {
		ids, err := e.encodeSafe(prefix + t)
		if err != nil {
			return nil, fmt.Errorf("embed: токенизация: %w", err)
		}
		if len(ids) > MaxTokens {
			ids = ids[:MaxTokens]
		}
		row := make([]int64, len(ids))
		for j, id := range ids {
			row[j] = int64(id)
		}
		encs[i] = row
		if len(row) > L {
			L = len(row)
		}
	}
	// Паддинг до фиксированных ведёр, а не до точного максимума батча:
	// у onnxruntime/CoreML форма входа стабильная (3 варианта вместо тысяч),
	// иначе CoreML перекомпилирует партиции почти на каждый батч.
	L = bucketLen(L)
	inputIDs := make([]int64, b*L)
	att := make([]int64, b*L)
	ttype := make([]int64, b*L)
	for i, row := range encs {
		copy(inputIDs[i*L:], row)
		for j := range row {
			att[i*L+j] = 1
		}
	}
	shape := []int64{int64(b), int64(L)}
	mk := func(data []int64) (*ort.Value, error) {
		return ort.NewTensorValue(e.rt, data, shape)
	}
	idsV, err := mk(inputIDs)
	if err != nil {
		return nil, err
	}
	defer idsV.Close()
	attV, err := mk(att)
	if err != nil {
		return nil, err
	}
	defer attV.Close()
	ttV, err := mk(ttype)
	if err != nil {
		return nil, err
	}
	defer ttV.Close()
	inputs := map[string]*ort.Value{
		"input_ids":      idsV,
		"attention_mask": attV,
		"token_type_ids": ttV,
	}
	outputs, err := e.session.Run(context.Background(), inputs)
	if err != nil {
		return nil, fmt.Errorf("embed: инференс: %w", err)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()
	hidden, ok := outputs["last_hidden_state"]
	if !ok {
		return nil, fmt.Errorf("embed: у модели нет выхода last_hidden_state (есть: %v)", e.session.OutputNames())
	}
	data, hshape, err := ort.GetTensorData[float32](hidden)
	if err != nil {
		return nil, err
	}
	if len(hshape) != 3 || int(hshape[2]) != Dim {
		return nil, fmt.Errorf("embed: неожиданная форма last_hidden_state %v", hshape)
	}
	out := make([][]float32, b)
	for i := range out {
		out[i] = meanPoolNorm(data[i*L*Dim:(i+1)*L*Dim], att[i*L:(i+1)*L])
	}
	return out, nil
}

// encodeSafe токенизирует текст, перехватывая панику sugarme/tokenizer на
// «ядовитых» входах (напр. Metaspace: slice bounds out of range на экзотике
// юникода). Такой текст заменяется пустым — вектор получается нулевым,
// но индексация проекта продолжается.
func (e *Embedder) encodeSafe(text string) (ids []int, err error) {
	defer func() {
		if r := recover(); r != nil {
			e.skipped++
			fmt.Fprintf(os.Stderr, "embed: паника токенизатора на тексте %d байт, чанк получит нулевой вектор: %v\n", len(text), r)
			enc, e2 := e.tok.EncodeSingle("", true)
			if e2 != nil {
				err = e2
				return
			}
			ids = enc.Ids
		}
	}()
	enc, err := e.tok.EncodeSingle(text, true)
	if err != nil {
		return nil, err
	}
	return enc.Ids, nil
}

// padBuckets — фиксированные длины входа эмбеддера. Ограничение набора форм
// критично для CoreML (компиляция под каждую форму) и уменьшает лишний
// паддинг на CPU. Последнее ведро = MaxTokens.
var padBuckets = []int{128, 256, MaxTokens}

// bucketLen округляет длину последовательности вверх до ближайшего ведра.
func bucketLen(n int) int {
	for _, b := range padBuckets {
		if n <= b {
			return b
		}
	}
	return MaxTokens
}

// meanPoolNorm — mean pooling по маске + L2-нормализация (cosine = dot).
func meanPoolNorm(hidden []float32, mask []int64) []float32 {
	vec := make([]float32, Dim)
	var n float64
	for j, m := range mask {
		if m == 0 {
			continue
		}
		row := hidden[j*Dim : (j+1)*Dim]
		for d := 0; d < Dim; d++ {
			vec[d] += row[d]
		}
		n++
	}
	if n == 0 {
		return vec
	}
	inv := float32(1 / n)
	var norm float64
	for d := 0; d < Dim; d++ {
		vec[d] *= inv
		norm += float64(vec[d]) * float64(vec[d])
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for d := range vec {
			vec[d] /= float32(norm)
		}
	}
	return vec
}
