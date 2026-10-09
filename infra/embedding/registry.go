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
	cohere   Provider
	openai   Provider
	endpoint Provider
	e5       Provider
	metrics  *clientMetrics
}

// RegistryOption configures the shared HTTP behavior of the registry providers.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	httpClient    *http.Client
	geminiBaseURL string
	cohereBaseURL string
	openAIBaseURL string
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

// WithCohereBaseURLOption overrides the Cohere base URL (used in tests).
func WithCohereBaseURLOption(url string) RegistryOption {
	return func(c *registryConfig) { c.cohereBaseURL = url }
}

// WithOpenAIBaseURLOption overrides the OpenAI base URL (used in tests).
func WithOpenAIBaseURLOption(url string) RegistryOption {
	return func(c *registryConfig) { c.openAIBaseURL = url }
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

	cohereOpts := make([]CohereOption, 0, 2)
	if cfg.httpClient != nil {
		cohereOpts = append(cohereOpts, WithCohereHTTPClient(cfg.httpClient))
	}

	if cfg.cohereBaseURL != "" {
		cohereOpts = append(cohereOpts, WithCohereBaseURL(cfg.cohereBaseURL))
	}

	openAIOpts := make([]OpenAIOption, 0, 2)
	if cfg.httpClient != nil {
		openAIOpts = append(openAIOpts, WithOpenAIHTTPClient(cfg.httpClient))
	}

	if cfg.openAIBaseURL != "" {
		openAIOpts = append(openAIOpts, WithOpenAIBaseURL(cfg.openAIBaseURL))
	}

	endpointOpts := make([]EndpointOption, 0, 1)
	if cfg.httpClient != nil {
		endpointOpts = append(endpointOpts, WithEndpointHTTPClient(cfg.httpClient))
	}

	endpoint := NewEndpoint(endpointOpts...)

	return &Registry{
		gemini:   NewGemini(geminiOpts...),
		cohere:   NewCohere(cohereOpts...),
		openai:   NewOpenAI(openAIOpts...),
		endpoint: endpoint,
		e5:       prefixed{Provider: endpoint, query: "query: ", document: "passage: "},
		metrics:  newClientMetrics(cfg.meterProvider),
	}
}

// ForModel returns the Provider for a model's provider key. Every call to it
// is recorded under that key.
func (r *Registry) ForModel(provider string) (Provider, error) {
	switch provider {
	case ProviderGemini:
		return measured{Provider: r.gemini, key: provider, metrics: r.metrics}, nil
	case ProviderCohere:
		return measured{Provider: r.cohere, key: provider, metrics: r.metrics}, nil
	case ProviderOpenAI:
		return measured{Provider: r.openai, key: provider, metrics: r.metrics}, nil
	case ProviderE5:
		return measured{Provider: r.e5, key: provider, metrics: r.metrics}, nil
	case ProviderBGEM3, ProviderBGEReranker, ProviderBYOM:
		return measured{Provider: r.endpoint, key: provider, metrics: r.metrics}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, provider)
	}
}
