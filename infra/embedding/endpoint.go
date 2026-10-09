package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Endpoint calls a self-hosted embedding/reranker service.
type Endpoint struct {
	client *httpClient
}

// EndpointOption configures an Endpoint provider.
type EndpointOption func(*Endpoint)

// WithEndpointHTTPClient overrides the underlying HTTP client.
func WithEndpointHTTPClient(hc *http.Client) EndpointOption {
	return func(e *Endpoint) { e.client = newHTTPClient(0, hc) }
}

// NewEndpoint builds a self-hosted endpoint provider.
func NewEndpoint(opts ...EndpointOption) *Endpoint {
	e := &Endpoint{client: newHTTPClient(defaultTimeout, nil)}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

type endpointEmbedRequest struct {
	Model      string   `json:"model"`
	Texts      []string `json:"texts"`
	Dimensions int      `json:"dimensions,omitempty"`
	Task       string   `json:"task"`
}

type endpointEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *Endpoint) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	if req.Endpoint == "" {
		return EmbedResult{}, errors.New("embedding: endpoint url is required")
	}

	if len(req.Texts) == 0 {
		return EmbedResult{Vectors: make([][]float32, 0)}, nil
	}

	body := endpointEmbedRequest{
		Model:      req.ModelRef,
		Texts:      req.Texts,
		Dimensions: req.Dimensions,
		Task:       endpointTask(req.Task),
	}

	var out endpointEmbedResponse
	if err := e.client.doJSON(ctx, http.MethodPost, endpointURL(req.Endpoint, "embed"), nil, body, &out); err != nil {
		return EmbedResult{}, err
	}

	if len(out.Embeddings) != len(req.Texts) {
		return EmbedResult{}, fmt.Errorf("embedding: endpoint returned %d vectors for %d texts", len(out.Embeddings), len(req.Texts))
	}

	return EmbedResult{Vectors: out.Embeddings}, nil
}

// Rerank speaks the Cohere rerank contract, as vLLM and llama.cpp server do.
func (e *Endpoint) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	if req.Endpoint == "" {
		return RerankResult{}, errors.New("embedding: endpoint url is required")
	}

	return rerank(ctx, e.client, req.Endpoint, nil, req)
}

func endpointTask(t TaskType) string {
	if t == TaskQuery {
		return "query"
	}

	return "document"
}

func endpointURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + path
}
