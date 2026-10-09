package embedding

import (
	"context"
	"errors"
	"net/http"
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

// Embed speaks the OpenAI embeddings contract. Self-hosted servers take the
// size from the model itself, so none is asked for; the caller checks it.
func (e *Endpoint) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	if req.Endpoint == "" {
		return EmbedResult{}, errors.New("embedding: endpoint url is required")
	}

	return embed(ctx, e.client, req.Endpoint, nil, req, 0)
}

// Rerank speaks the Cohere rerank contract, as vLLM and llama.cpp server do.
func (e *Endpoint) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	if req.Endpoint == "" {
		return RerankResult{}, errors.New("embedding: endpoint url is required")
	}

	return rerank(ctx, e.client, req.Endpoint, nil, req)
}
