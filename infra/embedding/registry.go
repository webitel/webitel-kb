package embedding

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
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
	metrics  *clientMetrics
}

// RegistryOption configures the shared HTTP behavior of the registry providers.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	httpClient    *http.Client
	geminiBaseURL string
	meterProvider metric.MeterProvider
}

// WithHTTPClient sets the HTTP client used by all providers (e.g. in tests).
func WithHTTPClient(hc *http.Client) RegistryOption {
	return func(c *registryConfig) { c.httpClient = hc }
}

// WithGeminiBaseURLOption overrides the Gemini base URL (used in tests).
func WithGeminiBaseURLOption(url string) RegistryOption {
	return func(c *registryConfig) { c.geminiBaseURL = url }
}

// WithMeterProvider sets where the provider calls are recorded; the global
// provider by default.
func WithMeterProvider(mp metric.MeterProvider) RegistryOption {
	return func(c *registryConfig) { c.meterProvider = mp }
}

// NewRegistry builds the provider registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	cfg := registryConfig{meterProvider: otel.GetMeterProvider()}
	for _, opt := range opts {
		opt(&cfg)
	}

	geminiOpts := make([]GeminiOption, 0, 2)
	if cfg.httpClient != nil {
		geminiOpts = append(geminiOpts, WithGeminiHTTPClient(cfg.httpClient))
	}

	if cfg.geminiBaseURL != "" {
		geminiOpts = append(geminiOpts, WithGeminiBaseURL(cfg.geminiBaseURL))
	}

	endpointOpts := make([]EndpointOption, 0, 1)
	if cfg.httpClient != nil {
		endpointOpts = append(endpointOpts, WithEndpointHTTPClient(cfg.httpClient))
	}

	return &Registry{
		gemini:   NewGemini(geminiOpts...),
		endpoint: NewEndpoint(endpointOpts...),
		metrics:  newClientMetrics(cfg.meterProvider),
	}
}

// ForModel returns the Provider for a model's provider key. Every call to it
// is recorded under that key.
func (r *Registry) ForModel(provider string) (Provider, error) {
	switch provider {
	case ProviderGemini:
		return measured{Provider: r.gemini, key: provider, metrics: r.metrics}, nil
	case ProviderBGEM3, ProviderE5, ProviderBGEReranker, ProviderBYOM:
		return measured{Provider: r.endpoint, key: provider, metrics: r.metrics}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, provider)
	}
}
