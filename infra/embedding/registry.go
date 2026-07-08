package embedding

import (
	"fmt"
	"net/http"
)

// Provider keys.
const (
	ProviderGemini      = "gemini"
	ProviderOpenAI      = "openai"
	ProviderCohere      = "cohere"
	ProviderAzure       = "azure"
	ProviderBGEM3       = "bge-m3"
	ProviderE5          = "e5"
	ProviderBGEReranker = "bge-reranker"
	ProviderBYOM        = "byom"
)

// Registry maps a provider key to its Provider implementation.
type Registry struct {
	gemini   Provider
	endpoint Provider
}

// RegistryOption configures the shared HTTP behaviour of the registry providers.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	httpClient    *http.Client
	geminiBaseURL string
}

// WithHTTPClient sets the HTTP client used by all providers (e.g. in tests).
func WithHTTPClient(hc *http.Client) RegistryOption {
	return func(c *registryConfig) { c.httpClient = hc }
}

// WithGeminiBaseURLOption overrides the Gemini base URL (used in tests).
func WithGeminiBaseURLOption(url string) RegistryOption {
	return func(c *registryConfig) { c.geminiBaseURL = url }
}

// NewRegistry builds the provider registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	var cfg registryConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	geminiOpts := []GeminiOption{}
	if cfg.httpClient != nil {
		geminiOpts = append(geminiOpts, WithGeminiHTTPClient(cfg.httpClient))
	}
	if cfg.geminiBaseURL != "" {
		geminiOpts = append(geminiOpts, WithGeminiBaseURL(cfg.geminiBaseURL))
	}

	endpointOpts := []EndpointOption{}
	if cfg.httpClient != nil {
		endpointOpts = append(endpointOpts, WithEndpointHTTPClient(cfg.httpClient))
	}

	return &Registry{
		gemini:   NewGemini(geminiOpts...),
		endpoint: NewEndpoint(endpointOpts...),
	}
}

// ForModel returns the Provider for a model's provider key.
func (r *Registry) ForModel(provider string) (Provider, error) {
	switch provider {
	case ProviderGemini:
		return r.gemini, nil
	case ProviderBGEM3, ProviderE5, ProviderBGEReranker, ProviderBYOM:
		return r.endpoint, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, provider)
	}
}
