package embedding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const cohereDefaultBaseURL = "https://api.cohere.com/v2"

// Cohere calls the Cohere API.
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

type cohereEmbedRequest struct {
	Model           string   `json:"model"`
	Texts           []string `json:"texts"`
	InputType       string   `json:"input_type"`
	EmbeddingTypes  []string `json:"embedding_types"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

type cohereEmbedResponse struct {
	Embeddings struct {
		Float [][]float32 `json:"float"`
	} `json:"embeddings"`
}

// Embed vectorizes the texts in request order. The size is asked for only from
// the models that take one (embed-v4 and newer); the older ones have a fixed size.
func (c *Cohere) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	if len(req.Texts) == 0 {
		return EmbedResult{Vectors: make([][]float32, 0)}, nil
	}

	body := cohereEmbedRequest{
		Model:          req.ModelRef,
		Texts:          req.Texts,
		InputType:      cohereInputType(req.Task),
		EmbeddingTypes: []string{"float"},
	}

	if cohereSizesOutput(req.ModelRef) {
		body.OutputDimension = req.Dimensions
	}

	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}

	var out cohereEmbedResponse
	if err := c.client.doJSON(ctx, http.MethodPost, c.baseURL+"/embed", headers, body, &out); err != nil {
		return EmbedResult{}, err
	}

	if len(out.Embeddings.Float) != len(req.Texts) {
		return EmbedResult{}, fmt.Errorf("embedding: cohere returned %d vectors for %d texts",
			len(out.Embeddings.Float), len(req.Texts))
	}

	return EmbedResult{Vectors: out.Embeddings.Float}, nil
}

func (c *Cohere) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}

	return rerank(ctx, c.client, c.baseURL, headers, req)
}

func cohereInputType(t TaskType) string {
	if t == TaskQuery {
		return "search_query"
	}

	return "search_document"
}

// cohereSizesOutput reports whether a model takes the output size: the
// embed-v<N> line does, the v3 models named by language do not.
func cohereSizesOutput(modelRef string) bool {
	return strings.HasPrefix(modelRef, "embed-v")
}
