package embedding

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
)

const (
	geminiDefaultBaseURL = "https://generativelanguage.googleapis.com"
	// geminiNativeDim is the largest native dimension; below it Gemini does not
	// unit-normalize the returned vectors, so we normalize them ourselves.
	geminiNativeDim = 3072
)

// Gemini is the Google AI (generativelanguage) embedding provider. It supports
// embeddings only; Rerank returns ErrUnsupported.
type Gemini struct {
	baseURL string
	client  *httpClient
}

// GeminiOption configures a Gemini provider.
type GeminiOption func(*Gemini)

// WithGeminiBaseURL overrides the API base URL (used in tests).
func WithGeminiBaseURL(url string) GeminiOption {
	return func(g *Gemini) { g.baseURL = strings.TrimRight(url, "/") }
}

// WithGeminiHTTPClient overrides the underlying HTTP client.
func WithGeminiHTTPClient(hc *http.Client) GeminiOption {
	return func(g *Gemini) { g.client = newHTTPClient(0, hc) }
}

// NewGemini builds a Gemini provider with a default timeout and tracing.
func NewGemini(opts ...GeminiOption) *Gemini {
	g := &Gemini{baseURL: geminiDefaultBaseURL, client: newHTTPClient(defaultTimeout, nil)}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbedRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	TaskType             string        `json:"taskType,omitempty"`
	OutputDimensionality int           `json:"outputDimensionality,omitempty"`
}

type geminiBatchRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiBatchResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

func (g *Gemini) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	if len(req.Texts) == 0 {
		return EmbedResult{Vectors: [][]float32{}}, nil
	}

	model := geminiModelPath(req.ModelRef)
	taskType := geminiTaskType(req.Task)

	batch := geminiBatchRequest{Requests: make([]geminiEmbedRequest, 0, len(req.Texts))}
	for _, text := range req.Texts {
		batch.Requests = append(batch.Requests, geminiEmbedRequest{
			Model:                model,
			Content:              geminiContent{Parts: []geminiPart{{Text: text}}},
			TaskType:             taskType,
			OutputDimensionality: req.Dimensions,
		})
	}

	url := fmt.Sprintf("%s/v1beta/%s:batchEmbedContents", g.baseURL, model)
	headers := map[string]string{"x-goog-api-key": req.APIKey}

	var out geminiBatchResponse
	if err := g.client.doJSON(ctx, http.MethodPost, url, headers, batch, &out); err != nil {
		return EmbedResult{}, err
	}

	if len(out.Embeddings) != len(req.Texts) {
		return EmbedResult{}, fmt.Errorf("embedding: gemini returned %d vectors for %d texts", len(out.Embeddings), len(req.Texts))
	}

	// Gemini does not unit-normalize sub-native output dimensions.
	normalize := req.Dimensions > 0 && req.Dimensions < geminiNativeDim

	vectors := make([][]float32, len(out.Embeddings))
	for i, e := range out.Embeddings {
		vectors[i] = e.Values
		if normalize {
			l2Normalize(vectors[i])
		}
	}
	return EmbedResult{Vectors: vectors}, nil
}

func (g *Gemini) Rerank(context.Context, RerankRequest) (RerankResult, error) {
	return RerankResult{}, fmt.Errorf("%w: gemini rerank", ErrUnsupported)
}

// geminiModelPath ensures the "models/" prefix expected by the API.
func geminiModelPath(ref string) string {
	if strings.HasPrefix(ref, "models/") {
		return ref
	}
	return "models/" + ref
}

func geminiTaskType(t TaskType) string {
	if t == TaskQuery {
		return "RETRIEVAL_QUERY"
	}
	return "RETRIEVAL_DOCUMENT"
}

// l2Normalize scales v in place to unit length; a zero vector is left unchanged.
func l2Normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	norm := math.Sqrt(sum)
	for i, x := range v {
		v[i] = float32(float64(x) / norm)
	}
}
