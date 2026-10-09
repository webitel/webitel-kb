package embedding

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	genaisemconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	webitelconv "github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
)

// stubProvider answers every call with err.
type stubProvider struct{ err error }

func (p stubProvider) Embed(context.Context, EmbedRequest) (EmbedResult, error) {
	return EmbedResult{}, p.err
}

func (p stubProvider) Rerank(context.Context, RerankRequest) (RerankResult, error) {
	return RerankResult{}, p.err
}

// recorded returns the histogram points of the metric.
func recorded(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var points []metricdata.HistogramDataPoint[float64]

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if h, ok := m.Data.(metricdata.Histogram[float64]); ok && m.Name == name {
				points = append(points, h.DataPoints...)
			}
		}
	}

	return points
}

func TestProviderCallsAreRecorded(t *testing.T) {
	providerErr := errors.New("provider is down")
	errorType := semconv.ErrorType(providerErr).Value.AsString()

	tests := []struct {
		name   string
		key    string
		err    error
		call   func(p Provider)
		metric string
		want   map[attribute.Key]string
	}{
		{
			name: "embedding answered",
			key:  ProviderBGEM3,
			call: func(p Provider) {
				_, _ = p.Embed(context.Background(), EmbedRequest{ModelRef: "BAAI/bge-m3"})
			},
			metric: genaiconv.ClientOperationDuration{}.Name(),
			want: map[attribute.Key]string{
				genaisemconv.GenAIOperationNameKey: "embeddings",
				genaisemconv.GenAIProviderNameKey:  "bge-m3",
				genaisemconv.GenAIRequestModelKey:  "BAAI/bge-m3",
			},
		},
		{
			name: "gemini named as the conventions know it",
			key:  ProviderGemini,
			call: func(p Provider) {
				_, _ = p.Embed(context.Background(), EmbedRequest{ModelRef: "gemini-embedding-001"})
			},
			metric: genaiconv.ClientOperationDuration{}.Name(),
			want: map[attribute.Key]string{
				genaisemconv.GenAIOperationNameKey: "embeddings",
				genaisemconv.GenAIProviderNameKey:  "gcp.gemini",
				genaisemconv.GenAIRequestModelKey:  "gemini-embedding-001",
			},
		},
		{
			name: "openai named as the conventions know it",
			key:  ProviderOpenAI,
			call: func(p Provider) {
				_, _ = p.Embed(context.Background(), EmbedRequest{ModelRef: "text-embedding-3-small"})
			},
			metric: genaiconv.ClientOperationDuration{}.Name(),
			want: map[attribute.Key]string{
				genaisemconv.GenAIOperationNameKey: "embeddings",
				genaisemconv.GenAIProviderNameKey:  "openai",
				genaisemconv.GenAIRequestModelKey:  "text-embedding-3-small",
			},
		},
		{
			name: "embedding failed",
			key:  ProviderE5,
			err:  providerErr,
			call: func(p Provider) {
				_, _ = p.Embed(context.Background(), EmbedRequest{ModelRef: "multilingual-e5-large"})
			},
			metric: genaiconv.ClientOperationDuration{}.Name(),
			want: map[attribute.Key]string{
				genaisemconv.GenAIOperationNameKey: "embeddings",
				genaisemconv.GenAIProviderNameKey:  "e5",
				genaisemconv.GenAIRequestModelKey:  "multilingual-e5-large",
				semconv.ErrorTypeKey:               errorType,
			},
		},
		{
			name: "rerank answered",
			key:  ProviderBGEReranker,
			call: func(p Provider) {
				_, _ = p.Rerank(context.Background(), RerankRequest{ModelRef: "BAAI/bge-reranker-v2-m3"})
			},
			metric: kbconv.RerankDuration{}.Name(),
			want: map[attribute.Key]string{
				webitelconv.WebitelKBRerankProviderKey: "bge-reranker",
				webitelconv.WebitelKBRerankModelKey:    "BAAI/bge-reranker-v2-m3",
			},
		},
		{
			name: "rerank failed",
			key:  ProviderBGEReranker,
			err:  providerErr,
			call: func(p Provider) {
				_, _ = p.Rerank(context.Background(), RerankRequest{ModelRef: "BAAI/bge-reranker-v2-m3"})
			},
			metric: kbconv.RerankDuration{}.Name(),
			want: map[attribute.Key]string{
				webitelconv.WebitelKBRerankProviderKey: "bge-reranker",
				webitelconv.WebitelKBRerankModelKey:    "BAAI/bge-reranker-v2-m3",
				semconv.ErrorTypeKey:                   errorType,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			r := NewRegistry(WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
			r.gemini, r.endpoint = stubProvider{tt.err}, stubProvider{tt.err}

			p, err := r.ForModel(tt.key)
			if err != nil {
				t.Fatalf("for model: %v", err)
			}

			tt.call(p)

			points := recorded(t, reader, tt.metric)
			if len(points) != 1 || points[0].Count != 1 {
				t.Fatalf("%s: %v, want one recorded call", tt.metric, points)
			}

			if len(points[0].Bounds) != len(durationBuckets) {
				t.Fatalf("bounds = %v, want %v", points[0].Bounds, durationBuckets)
			}

			got := make(map[attribute.Key]string)
			for _, kv := range points[0].Attributes.ToSlice() {
				got[kv.Key] = kv.Value.String()
			}

			if len(got) != len(tt.want) {
				t.Fatalf("attributes = %v, want %v", got, tt.want)
			}

			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("attributes = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
