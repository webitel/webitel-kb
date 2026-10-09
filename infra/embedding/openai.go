package embedding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const openAIDefaultBaseURL = "https://api.openai.com"

// openAIRoute is the embeddings route of the OpenAI API, also served by TEI,
// llama.cpp server, vLLM and Ollama.
const openAIRoute = "v1/embeddings"

// embedRequest is the OpenAI embeddings request.
type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embedResponse lists one vector per input, each with the input's position.
type embedResponse struct {
	Data []embedData `json:"data"`
}

type embedData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// embed vectorizes the texts over the OpenAI embeddings contract at root.
// dimensions is sent only when non-zero.
func embed(
	ctx context.Context, client *httpClient, root string, headers map[string]string, req EmbedRequest, dimensions int,
) (EmbedResult, error) {
	if len(req.Texts) == 0 {
		return EmbedResult{Vectors: make([][]float32, 0)}, nil
	}

	body := embedRequest{Model: req.ModelRef, Input: req.Texts, Dimensions: dimensions}

	target, err := serviceURL(root, openAIRoute)
	if err != nil {
		return EmbedResult{}, err
	}

	var out embedResponse
	if err := client.doJSON(ctx, http.MethodPost, target, headers, body, &out); err != nil {
		return EmbedResult{}, err
	}

	data, err := byIndex(out.Data, len(req.Texts), func(d embedData) int { return d.Index })
	if err != nil {
		return EmbedResult{}, err
	}

	vectors := make([][]float32, len(data))
	for i, d := range data {
		vectors[i] = d.Embedding
	}

	return EmbedResult{Vectors: vectors}, nil
}

// OpenAI calls the OpenAI API. Only embedding is implemented.
type OpenAI struct {
	baseURL string
	client  *httpClient
}

// OpenAIOption configures an OpenAI provider.
type OpenAIOption func(*OpenAI)

// WithOpenAIBaseURL overrides the API base URL (useful for tests).
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(o *OpenAI) { o.baseURL = strings.TrimRight(url, "/") }
}

// WithOpenAIHTTPClient overrides the underlying HTTP client.
func WithOpenAIHTTPClient(hc *http.Client) OpenAIOption {
	return func(o *OpenAI) { o.client = newHTTPClient(0, hc) }
}

// NewOpenAI builds an OpenAI provider.
func NewOpenAI(opts ...OpenAIOption) *OpenAI {
	o := &OpenAI{baseURL: openAIDefaultBaseURL, client: newHTTPClient(defaultTimeout, nil)}
	for _, opt := range opts {
		opt(o)
	}

	return o
}

func (o *OpenAI) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}

	return embed(ctx, o.client, o.baseURL, headers, req, req.Dimensions)
}

func (o *OpenAI) Rerank(context.Context, RerankRequest) (RerankResult, error) {
	return RerankResult{}, fmt.Errorf("%w: openai rerank", ErrUnsupported)
}
