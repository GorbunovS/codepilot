// ONNX-реализация Scorer: decision-модель Laya (encoder + decision head).
//
// Контракт модели (референс receptron/laya-onnx, пакет laya/common.py):
//
//	Входы:  input_ids [B,L] int64, attention_mask [B,L] int64,
//	        marker_pos [B,K] int64, marker_mask [B,K] bool, qtype [B] int64.
//	Выходы: logits [B,K] float32 (маскed слоты = -1e4),
//	        act_probs [B,2] float32 (или act_logits — тогда softmax на нашей стороне).
//
// Сборка последовательности (порт build_sequence из laya/common.py):
//
//	[CLS] "<type> question: <instructions>" [SEP] ([MASK] option)* [SEP] state [SEP]
//
// маркеры — позиции [MASK] перед каждой опцией. Температурная калибровка
// берётся из laya_config.json (temperature по типу вопроса +
// temperature_by_options по бакету "<type>:<размер>").
package laya

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"codepilot/internal/chunk"
	"codepilot/internal/index"

	ort "github.com/shota3506/onnxruntime-purego/onnxruntime"
	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
)

// ortAPIVersion — версия C API onnxruntime 1.23.x (см. bin/onnxruntime.dll).
const ortAPIVersion = 23

// Коды типов вопросов (QTYPES из laya/common.py).
const (
	qtypeChoice = 0
	qtypeScore  = 1
	qtypeNoul   = 2
)

var qtypeNames = []string{"choice", "score", "noul"}

// Тексты вопросов. Должны совпадать с models/laya-multilingual/SPEC.md.
const (
	scoreInstructions = "Насколько фрагмент кода релевантен запросу?"
	noulInstructions  = "Достаточно ли приведённого контекста, чтобы полно и точно ответить на вопрос?"
)

var scoreCriteria = []string{
	"совершенно нерелевантен",
	"почти не связан с запросом",
	"косвенно связан с запросом",
	"частично релевантен",
	"релевантен",
	"точно отвечает на запросу",
}

const (
	stateContentRunes = 1500 // усечение content чанка в state для score
	noulTopChunks     = 5    // сколько топ-чанков входит в state для noul
	optionMaxTokens   = 48   // кап токенов на опцию (референс: truncation max_length=48)
)

// Границы температуры из референса (TEMP_MIN/TEMP_MAX).
const (
	tempMin = 0.5
	tempMax = 5.0
)

// ModelConfig — laya_config.json рядом с моделью.
type ModelConfig struct {
	MaxLen               int                `json:"max_len"`
	HeadMaxLen           int                `json:"head_max_len"`
	Temperature          []float64          `json:"temperature"`           // по типу вопроса [choice, score, noul]
	TemperatureByOptions map[string]float64 `json:"temperature_by_options"` // ключ "<type>:<2|3-5|6-10|11+>"
}

func (c *ModelConfig) defaults() {
	if c.MaxLen <= 0 {
		c.MaxLen = 512
	}
	if c.HeadMaxLen <= 0 {
		c.HeadMaxLen = 192
	}
	for len(c.Temperature) < 3 {
		c.Temperature = append(c.Temperature, 1.0)
	}
}

// Model — ONNX-реализация laya.Scorer.
type Model struct {
	rt      *ort.Runtime
	env     *ort.Env
	session *ort.Session
	tok     *tokenizer.Tokenizer
	cfg     ModelConfig

	clsID, sepID, maskID, padID int64
	maskTok                     string

	mu       sync.Mutex // tokenizer и session не потокобезопасны
	warnOnce sync.Once
}

var _ Scorer = (*Model)(nil)
var _ index.Scorer = (*Model)(nil)

// Load загружает модель из modelDir: tokenizer.json, laya.onnx, laya_config.json.
// Путь к onnxruntime.dll: переменная CODEPILOT_ONNXRUNTIME_DLL, затем
// modelDir/onnxruntime.dll, bin/onnxruntime.dll, onnxruntime.dll из PATH.
func Load(modelDir string) (*Model, error) {
	cfg, err := loadConfig(modelDir)
	if err != nil {
		return nil, err
	}
	tokPath := filepath.Join(modelDir, "tokenizer.json")
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("laya: tokenizer.json не найден в %s: %w", modelDir, err)
	}
	onnxPath := filepath.Join(modelDir, "laya.onnx")
	if _, err := os.Stat(onnxPath); err != nil {
		return nil, fmt.Errorf("laya: laya.onnx не найден в %s: %w", modelDir, err)
	}
	dllPath := findRuntimeDLL(modelDir)
	if dllPath == "" {
		return nil, fmt.Errorf("laya: onnxruntime.dll не найден (проверены CODEPILOT_ONNXRUNTIME_DLL, %s, bin/)", modelDir)
	}

	tk, err := pretrained.FromFile(tokPath)
	if err != nil {
		return nil, fmt.Errorf("laya: tokenizer.json: %w", err)
	}
	m := &Model{tok: tk, cfg: cfg}
	if err := m.resolveSpecialTokens(tokPath); err != nil {
		return nil, err
	}

	rt, err := ort.NewRuntime(dllPath, ortAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("laya: загрузка %s: %w", dllPath, err)
	}
	m.rt = rt
	env, err := rt.NewEnv("codepilot-laya", ort.LoggingLevelWarning)
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("laya: ORT env: %w", err)
	}
	m.env = env
	sess, err := rt.NewSession(env, onnxPath, nil)
	if err != nil {
		env.Close()
		rt.Close()
		return nil, fmt.Errorf("laya: сессия ONNX %s: %w", onnxPath, err)
	}
	m.session = sess
	return m, nil
}

// Close освобождает сессию, окружение и runtime.
func (m *Model) Close() {
	if m.session != nil {
		m.session.Close()
	}
	if m.env != nil {
		m.env.Close()
	}
	if m.rt != nil {
		_ = m.rt.Close()
	}
}

func loadConfig(modelDir string) (ModelConfig, error) {
	var cfg ModelConfig
	for _, name := range []string{"laya_config.json", "rl_agent_config.json"} {
		data, err := os.ReadFile(filepath.Join(modelDir, name))
		if err != nil {
			continue
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("laya: %s: %w", name, err)
		}
		cfg.defaults()
		return cfg, nil
	}
	return cfg, fmt.Errorf("laya: laya_config.json не найден в %s", modelDir)
}

func findRuntimeDLL(modelDir string) string {
	if p := os.Getenv("CODEPILOT_ONNXRUNTIME_DLL"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, p := range []string{
		filepath.Join(modelDir, "onnxruntime.dll"),
		filepath.Join("bin", "onnxruntime.dll"),
		"onnxruntime.dll",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// resolveSpecialTokens достаёт id спецтокенов из added_tokens tokenizer.json.
func (m *Model) resolveSpecialTokens(tokPath string) error {
	data, err := os.ReadFile(tokPath)
	if err != nil {
		return err
	}
	var tj struct {
		AddedTokens []struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
		} `json:"added_tokens"`
	}
	if err := json.Unmarshal(data, &tj); err != nil {
		return fmt.Errorf("laya: tokenizer.json added_tokens: %w", err)
	}
	byContent := map[string]int64{}
	for _, t := range tj.AddedTokens {
		byContent[t.Content] = t.ID
	}
	// mmBERT/ModernBERT: [CLS] [SEP] [MASK] [PAD]; BERT: то же. Допускаем <mask>-варианты.
	lookup := func(names ...string) (int64, string, bool) {
		for _, n := range names {
			if id, ok := byContent[n]; ok {
				return id, n, true
			}
		}
		return 0, "", false
	}
	var ok bool
	if m.clsID, _, ok = lookup("[CLS]", "<cls>", "<s>"); !ok {
		return fmt.Errorf("laya: в tokenizer.json нет CLS-токена")
	}
	if m.sepID, _, ok = lookup("[SEP]", "<sep>", "</s>"); !ok {
		return fmt.Errorf("laya: в tokenizer.json нет SEP-токена")
	}
	var maskName string
	if m.maskID, maskName, ok = lookup("[MASK]", "<mask>"); !ok {
		return fmt.Errorf("laya: в tokenizer.json нет MASK-токена")
	}
	m.maskTok = maskName
	if m.padID, _, ok = lookup("[PAD]", "<pad>"); !ok {
		return fmt.Errorf("laya: в tokenizer.json нет PAD-токена")
	}
	return nil
}

// encode токенизирует текст без спецтокенов (соответствует add_special_tokens=False).
func (m *Model) encode(text string) ([]int64, error) {
	enc, err := m.tok.EncodeSingle(text, false)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(enc.Ids))
	for i, id := range enc.Ids {
		ids[i] = int64(id)
	}
	return ids, nil
}

// seqItem — одна строка батча: токены, позиции маркеров, тип вопроса.
type seqItem struct {
	ids     []int64
	markers []int64
	qtype   int64
	k       int
}

// buildSequence — порт build_sequence из laya/common.py (truncate_left=False).
func (m *Model) buildSequence(qtype int64, instructions string, options []string, stateText string) (seqItem, error) {
	optIDs := make([][]int64, len(options))
	for i, o := range options {
		ids, err := m.encode(" " + strings.ReplaceAll(o, m.maskTok, " "))
		if err != nil {
			return seqItem{}, err
		}
		if len(ids) > optionMaxTokens {
			ids = ids[:optionMaxTokens]
		}
		optIDs[i] = append([]int64{m.maskID}, ids...)
	}
	optLen := 0
	for _, o := range optIDs {
		optLen += len(o)
	}
	optBudget := m.cfg.HeadMaxLen - optLen
	if optBudget < 16 && len(optIDs) > 0 {
		per := (m.cfg.HeadMaxLen - 16) / len(optIDs)
		if per < 4 {
			per = 4
		}
		for i := range optIDs {
			if len(optIDs[i]) > per {
				optIDs[i] = optIDs[i][:per]
			}
		}
		optLen = 0
		for _, o := range optIDs {
			optLen += len(o)
		}
		optBudget = m.cfg.HeadMaxLen - optLen
	}
	headCap := optBudget
	if headCap < 8 {
		headCap = 8
	}
	headText := qtypeNames[qtype] + " question: " + strings.ReplaceAll(instructions, m.maskTok, " ")
	headIDs, err := m.encode(headText)
	if err != nil {
		return seqItem{}, err
	}
	if len(headIDs) > headCap {
		headIDs = headIDs[:headCap]
	}

	ids := []int64{m.clsID}
	ids = append(ids, headIDs...)
	ids = append(ids, m.sepID)
	markers := make([]int64, 0, len(optIDs))
	for _, o := range optIDs {
		markers = append(markers, int64(len(ids)))
		ids = append(ids, o...)
	}
	ids = append(ids, m.sepID)

	stateIDs, err := m.encode(strings.ReplaceAll(stateText, m.maskTok, " "))
	if err != nil {
		return seqItem{}, err
	}
	room := m.cfg.MaxLen - len(ids) - 1
	if room < 0 {
		room = 0
	}
	if len(stateIDs) > room {
		stateIDs = stateIDs[:room]
	}
	ids = append(ids, stateIDs...)
	ids = append(ids, m.sepID)
	if len(ids) > m.cfg.MaxLen {
		ids = ids[:m.cfg.MaxLen]
		kept := markers[:0]
		for _, mp := range markers {
			if mp < int64(m.cfg.MaxLen) {
				kept = append(kept, mp)
			}
		}
		markers = kept
	}
	return seqItem{ids: ids, markers: markers, qtype: qtype, k: len(options)}, nil
}

// runResult — результат инференса одной строки.
type runResult struct {
	probs    []float64 // softmax(logits[:k]/T)
	actProbs [2]float64
}

// infer гоняет батч строк одним прогоном сессии.
func (m *Model) infer(items []seqItem) ([]runResult, error) {
	b := len(items)
	if b == 0 {
		return nil, nil
	}
	var L, K int
	for _, it := range items {
		if len(it.ids) > L {
			L = len(it.ids)
		}
		if len(it.markers) > K {
			K = len(it.markers)
		}
	}
	if K == 0 {
		K = 1
	}
	inputIDs := make([]int64, b*L)
	for i := range inputIDs {
		inputIDs[i] = m.padID
	}
	att := make([]int64, b*L)
	mpos := make([]int64, b*K)
	mmask := make([]bool, b*K)
	qtype := make([]int64, b)
	for i, it := range items {
		copy(inputIDs[i*L:], it.ids)
		for j := range it.ids {
			att[i*L+j] = 1
		}
		for j, mp := range it.markers {
			mpos[i*K+j] = mp
			mmask[i*K+j] = true
		}
		qtype[i] = it.qtype
	}

	ctx := context.Background()
	mk := func(data any, shape []int64) (*ort.Value, error) {
		switch d := data.(type) {
		case []int64:
			return ort.NewTensorValue(m.rt, d, shape)
		case []bool:
			return ort.NewTensorValue(m.rt, d, shape)
		}
		return nil, fmt.Errorf("unsupported tensor type")
	}
	inputs := map[string]*ort.Value{}
	defs := []struct {
		name  string
		data  any
		shape []int64
	}{
		{"input_ids", inputIDs, []int64{int64(b), int64(L)}},
		{"attention_mask", att, []int64{int64(b), int64(L)}},
		{"marker_pos", mpos, []int64{int64(b), int64(K)}},
		{"marker_mask", mmask, []int64{int64(b), int64(K)}},
		{"qtype", qtype, []int64{int64(b)}},
	}
	for _, d := range defs {
		v, err := mk(d.data, d.shape)
		if err != nil {
			return nil, fmt.Errorf("laya: тензор %s: %w", d.name, err)
		}
		defer v.Close()
		inputs[d.name] = v
	}
	outputs, err := m.session.Run(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("laya: инференс: %w", err)
	}
	defer func() {
		for _, v := range outputs {
			v.Close()
		}
	}()

	logitsVal, ok := outputs["logits"]
	if !ok {
		return nil, fmt.Errorf("laya: у модели нет выхода logits (есть: %v)", m.session.OutputNames())
	}
	logits, shape, err := ort.GetTensorData[float32](logitsVal)
	if err != nil {
		return nil, err
	}
	kOut := int(shape[len(shape)-1])

	// act_probs (готовые) или act_logits (softmax на нашей стороне, как в референсе).
	var act []float32
	if v, ok := outputs["act_probs"]; ok {
		act, _, err = ort.GetTensorData[float32](v)
	} else if v, ok2 := outputs["act_logits"]; ok2 {
		var raw []float32
		raw, _, err = ort.GetTensorData[float32](v)
		if err == nil {
			act = softmax2(raw)
		}
	}
	if err != nil {
		return nil, err
	}

	res := make([]runResult, b)
	for i, it := range items {
		t := m.temperature(it.qtype, it.k)
		z := make([]float64, it.k)
		var zmax float64 = math.Inf(-1)
		for j := 0; j < it.k; j++ {
			z[j] = float64(logits[i*kOut+j]) / t
			if z[j] > zmax {
				zmax = z[j]
			}
		}
		var sum float64
		for j := range z {
			z[j] = math.Exp(z[j] - zmax)
			sum += z[j]
		}
		for j := range z {
			z[j] /= sum
		}
		res[i].probs = z
		if len(act) >= (i+1)*2 {
			res[i].actProbs = [2]float64{float64(act[i*2]), float64(act[i*2+1])}
		}
	}
	return res, nil
}

func softmax2(logits []float32) []float32 {
	out := make([]float32, len(logits))
	for i := 0; i+1 < len(logits); i += 2 {
		m := logits[i]
		if logits[i+1] > m {
			m = logits[i+1]
		}
		a := math.Exp(float64(logits[i] - m))
		b := math.Exp(float64(logits[i+1] - m))
		out[i] = float32(a / (a + b))
		out[i+1] = float32(b / (a + b))
	}
	return out
}

// temperature — калибровочная температура для типа вопроса и числа опций.
func (m *Model) temperature(qtype int64, k int) float64 {
	bucket := "2"
	switch {
	case k > 10:
		bucket = "11+"
	case k > 5:
		bucket = "6-10"
	case k > 2:
		bucket = "3-5"
	}
	key := qtypeNames[qtype] + ":" + bucket
	if t, ok := m.cfg.TemperatureByOptions[key]; ok {
		return clampTemperature(t)
	}
	return clampTemperature(m.cfg.Temperature[qtype])
}

func clampTemperature(t float64) float64 {
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return 1.0
	}
	if t < tempMin {
		return tempMin
	}
	if t > tempMax {
		return tempMax
	}
	return t
}

// --- Сериализация state ---

// jsonStr — JSON-строка без HTML-эскейпинга (аналог json.dumps(ensure_ascii=False)).
func jsonStr(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// scoreState — state для вопроса о релевантности одного чанка.
func scoreState(query string, c chunk.Chunk) string {
	var b strings.Builder
	b.WriteString(`{"query":`)
	b.WriteString(jsonStr(query))
	b.WriteString(`,"chunk":{"file":`)
	b.WriteString(jsonStr(c.FilePath))
	b.WriteString(`,"symbol":`)
	b.WriteString(jsonStr(c.SymbolName))
	b.WriteString(`,"kind":`)
	b.WriteString(jsonStr(c.Kind))
	b.WriteString(`,"language":`)
	b.WriteString(jsonStr(c.Language))
	b.WriteString(`,"signature":`)
	b.WriteString(jsonStr(c.Signature))
	b.WriteString(`,"doc":`)
	b.WriteString(jsonStr(c.Doc))
	b.WriteString(`,"content":`)
	b.WriteString(jsonStr(truncateRunes(c.Content, stateContentRunes)))
	b.WriteString(`}}`)
	return b.String()
}

// noulState — state для гейта достаточности: запрос + краткие представления топа.
func noulState(query string, top []index.SearchHit) string {
	n := len(top)
	if n > noulTopChunks {
		n = noulTopChunks
	}
	var b strings.Builder
	b.WriteString(`{"query":`)
	b.WriteString(jsonStr(query))
	b.WriteString(`,"results":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		c := top[i].Chunk
		b.WriteString(`{"file":`)
		b.WriteString(jsonStr(c.FilePath))
		b.WriteString(`,"symbol":`)
		b.WriteString(jsonStr(c.SymbolName))
		b.WriteString(`,"signature":`)
		b.WriteString(jsonStr(c.Signature))
		b.WriteString(`,"doc":`)
		b.WriteString(jsonStr(truncateRunes(c.Doc, 300)))
		b.WriteString(`,"content":`)
		b.WriteString(jsonStr(truncateRunes(c.Content, 600)))
		b.WriteByte('}')
	}
	b.WriteString(`]}`)
	return b.String()
}

func scoreOptions() []string {
	opts := make([]string, len(scoreCriteria))
	for i, c := range scoreCriteria {
		opts[i] = fmt.Sprintf("level %d: %s", i, c)
	}
	return opts
}

// noulOptions — референсный порядок [false, true] с дефолтными критериями.
func noulOptions() []string {
	return []string{
		"false: no, the statement does not hold",
		"true: yes, the statement holds",
	}
}

// ScoreBatch — батчевый скоринг: все чанки одним прогоном модели.
func (m *Model) ScoreBatch(query string, chunks []chunk.Chunk) ([]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]seqItem, len(chunks))
	for i, c := range chunks {
		it, err := m.buildSequence(qtypeScore, scoreInstructions, scoreOptions(), scoreState(query, c))
		if err != nil {
			return nil, fmt.Errorf("laya: токенизация чанка %s: %w", c.ID, err)
		}
		items[i] = it
	}
	res, err := m.infer(items)
	if err != nil {
		return nil, err
	}
	scores := make([]float64, len(chunks))
	for i, r := range res {
		// матожидание уровня (референс: score = sum(i * p_i))
		for j, p := range r.probs {
			scores[i] += float64(j) * p
		}
	}
	return scores, nil
}

// Score реализует Scorer: релевантность чанка запросу, 0..5.
func (m *Model) Score(query string, c chunk.Chunk) float64 {
	scores, err := m.ScoreBatch(query, []chunk.Chunk{c})
	if err != nil {
		m.warnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "warning: laya onnx inference failed: %v (scores будут 0)\n", err)
		})
		return 0
	}
	return scores[0]
}

// Noul реализует Scorer: P(контекста достаточно) по топу выдачи, 0..1.
func (m *Model) Noul(query string, top []index.SearchHit) float64 {
	if len(top) == 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, err := m.buildSequence(qtypeNoul, noulInstructions, noulOptions(), noulState(query, top))
	if err != nil {
		m.warnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "warning: laya onnx tokenization failed: %v\n", err)
		})
		return 0
	}
	res, err := m.infer([]seqItem{it})
	if err != nil {
		m.warnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "warning: laya onnx inference failed: %v\n", err)
		})
		return 0
	}
	if len(res[0].probs) < 2 {
		return 0
	}
	return res[0].probs[1] // P(true)
}

// Resolve выбирает реализацию Scorer.
//
// kind: "onnx" — нейросеть из modelDir, "heuristic"/"" — эвристика.
// Пустой kind читается из CODEPILOT_LAYA. При любой ошибке загрузки ONNX —
// предупреждение в stderr и graceful fallback на Heuristic.
func Resolve(kind, modelDir string) Scorer {
	if kind == "" {
		kind = os.Getenv("CODEPILOT_LAYA")
	}
	if kind == "" || kind == "heuristic" {
		return Heuristic{}
	}
	if kind != "onnx" {
		fmt.Fprintf(os.Stderr, "warning: неизвестный движок laya %q, использую heuristic\n", kind)
		return Heuristic{}
	}
	if modelDir == "" {
		modelDir = "models/laya-multilingual"
	}
	m, err := Load(modelDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: laya onnx недоступна (%v); fallback на heuristic\n", err)
		return Heuristic{}
	}
	fmt.Fprintf(os.Stderr, "laya: ONNX-модель загружена из %s\n", modelDir)
	return m
}
