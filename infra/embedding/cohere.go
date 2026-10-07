package embedding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const cohereDefaultBaseURL = "https://api.cohere.com/v2"

// Cohere calls the Cohere API. Only reranking is implemented.
type Cohere struct {
	baseURL string
	client  *httpClient
}

// CohereOption configures a Cohere provider.
type CohereOption func(*Cohere)

// WithCohereBaseURL overrides the API base URL (useful for tests).
func WithCohereBaseURL(url string) CohereOption {
	return func(c *Cohere) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithCohereHTTPClient overrides the underlying HTTP client.
func WithCohereHTTPClient(hc *http.Client) CohereOption {
	return func(c *Cohere) { c.client = newHTTPClient(0, hc) }
}

// NewCohere builds a Cohere provider.
func NewCohere(opts ...CohereOption) *Cohere {
	c := &Cohere{baseURL: cohereDefaultBaseURL, client: newHTTPClient(defaultTimeout, nil)}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *Cohere) Embed(context.Context, EmbedRequest) (EmbedResult, error) {
	return EmbedResult{}, fmt.Errorf("%w: cohere embed", ErrUnsupported)
}

func (c *Cohere) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}

	return rerank(ctx, c.client, c.baseURL, headers, req)
}
