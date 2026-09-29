package embed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TextEmbedder — общий интерфейс эмбеддера e5: локальный ONNX (Embedder)
// или удалённый MLX-сайдкар (RemoteEmbedder). Векторный поиск в index
// типизирован этим интерфейсом.
type TextEmbedder interface {
	EmbedQueries(texts []string) ([][]float32, error)
	EmbedPassages(texts []string) ([][]float32, error)
}

// RemoteEmbedder — клиент MLX-сайдкара (tools/mlx-sidecar): те же векторы
// e5-small, что у локального ONNX-энкодера, но инференс на Apple Silicon
// через MLX (~100x быстрее). Контракт: POST /embed {"texts", "prefix"}.
type RemoteEmbedder struct {
	url string
	hc  *http.Client
}

// NewRemoteEmbedder — клиент сайдкара по URL (напр. http://127.0.0.1:8081).
func NewRemoteEmbedder(url string) *RemoteEmbedder {
	return &RemoteEmbedder{
		url: strings.TrimRight(url, "/"),
		hc: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        8,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

type embedRequest struct {
	Texts  []string `json:"texts"`
	Prefix string   `json:"prefix"`
}

type embedResponse struct {
	Vectors [][]float32 `json:"vectors"`
}

// EmbedQueries — векторы поисковых запросов (префикс "query: ").
func (r *RemoteEmbedder) EmbedQueries(texts []string) ([][]float32, error) {
	return r.embed("query: ", texts)
}

// EmbedPassages — векторы документов/чанков (префикс "passage: ").
func (r *RemoteEmbedder) EmbedPassages(texts []string) ([][]float32, error) {
	return r.embed("passage: ", texts)
}

// CheckDim — разовая проверка совместимости при старте индексации:
// сайдкар отвечает и возвращает векторы размерности Dim.
func (r *RemoteEmbedder) CheckDim() error {
	_, err := r.EmbedPassages([]string{"проверка"})
	return err
}

// Close освобождает keep-alive соединения.
func (r *RemoteEmbedder) Close() { r.hc.CloseIdleConnections() }

// PreferredBatch — сколько текстов забирать за раз из индексера. Локальный
// ONNX не реализует — там предел ставит компилятор CoreML.
func (r *RemoteEmbedder) PreferredBatch() int { return 512 }

// Подбатч и число запросов в полёте. Инференс на GPU — узкое место: больше
// двух запросов одновременно не ускоряет, но создаёт contention с GUI
// (WindowServer) — система начинает подтормаживать.
const (
	remoteSubBatch = 64
	remoteInflight = 2
)

func (r *RemoteEmbedder) embed(prefix string, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	n := (len(texts) + remoteSubBatch - 1) / remoteSubBatch
	out := make([][]float32, len(texts))
	sem := make(chan struct{}, remoteInflight)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for p := 0; p < n; p++ {
		off := p * remoteSubBatch
		end := off + remoteSubBatch
		if end > len(texts) {
			end = len(texts)
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(off, end int) {
			defer wg.Done()
			defer func() { <-sem }()
			vecs, err := r.post(prefix, texts[off:end])
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			copy(out[off:end], vecs)
		}(off, end)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (r *RemoteEmbedder) post(prefix string, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Texts: texts, Prefix: prefix})
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		resp, err = r.hc.Post(r.url+"/embed", "application/json", bytes.NewReader(body))
		if err == nil {
			break
		}
		// Один ретрай на connection refused (сайдкар мог только что подняться).
		if attempt == 0 && errors.Is(err, syscall.ECONNREFUSED) {
			continue
		}
		return nil, fmt.Errorf("MLX-сайдкар не отвечает на %s: запустите tools/mlx-sidecar/run.sh (%w)", r.url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed-server: чтение ответа: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed-server %s: HTTP %d: %s", r.url, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var out embedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("embed-server: разбор ответа: %w", err)
	}
	if len(out.Vectors) != len(texts) {
		return nil, fmt.Errorf("embed-server: векторов %d на %d текстов", len(out.Vectors), len(texts))
	}
	for i, v := range out.Vectors {
		if len(v) != Dim {
			return nil, fmt.Errorf("embed-server вернул размерность %d (вектор %d), ожидается %d (модель должна быть multilingual-e5-small)", len(v), i, Dim)
		}
	}
	return out.Vectors, nil
}
