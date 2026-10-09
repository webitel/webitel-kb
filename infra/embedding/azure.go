package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// azureRoute is the OpenAI embeddings route under the root of an Azure OpenAI
// resource.
const azureRoute = "openai/v1/embeddings"

// Azure calls an Azure OpenAI resource over its v1 API. Only embedding is
// implemented.
type Azure struct {
	client *httpClient
}

// AzureOption configures an Azure provider.
type AzureOption func(*Azure)

// WithAzureHTTPClient overrides the underlying HTTP client.
func WithAzureHTTPClient(hc *http.Client) AzureOption {
	return func(a *Azure) { a.client = newHTTPClient(0, hc) }
}

// NewAzure builds an Azure provider.
func NewAzure(opts ...AzureOption) *Azure {
	a := &Azure{client: newHTTPClient(defaultTimeout, nil)}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Embed calls the resource at the registered endpoint; the model reference is
// the deployment name.
func (a *Azure) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	if req.Endpoint == "" {
		return EmbedResult{}, errors.New("embedding: azure resource url is required")
	}

	headers := map[string]string{"api-key": req.APIKey}

	return embed(ctx, a.client, req.Endpoint, azureRoute, headers, req, req.Dimensions)
}

func (a *Azure) Rerank(context.Context, RerankRequest) (RerankResult, error) {
	return RerankResult{}, fmt.Errorf("%w: azure rerank", ErrUnsupported)
}
