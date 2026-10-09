package embedding

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
)

// meterName is the instrumentation scope of the provider clients.
const meterName = "github.com/webitel/webitel-kb/infra/embedding"

// durationBuckets are the GenAI boundaries: from 10 ms, doubling up to 81.92 s.
var durationBuckets = func() []float64 {
	b := make([]float64, 14)
	for i := range b {
		b[i] = 0.01 * float64(int(1)<<i)
	}

	return b
}()

// genAIProviders renames the providers the GenAI conventions know.
var genAIProviders = map[string]genaiconv.ProviderNameAttr{
	ProviderGemini: genaiconv.ProviderNameGCPGemini,
	ProviderOpenAI: genaiconv.ProviderNameOpenAI,
	ProviderCohere: genaiconv.ProviderNameCohere,
	ProviderAzure:  genaiconv.ProviderNameAzureAIOpenAI,
}

// clientMetrics records the calls to model providers.
type clientMetrics struct {
	embedding genaiconv.ClientOperationDuration
	rerank    kbconv.RerankDuration
}

func newClientMetrics(provider metric.MeterProvider) *clientMetrics {
	meter := provider.Meter(meterName)
	m := &clientMetrics{}

	var err error

	if m.embedding, err = genaiconv.NewClientOperationDuration(meter,
		metric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		otel.Handle(err)
	}

	if m.rerank, err = kbconv.NewRerankDuration(meter,
		metric.WithExplicitBucketBoundaries(durationBuckets...)); err != nil {
		otel.Handle(err)
	}

	return m
}

// measured is a Provider that records the duration of every call under the
// provider key it was resolved for.
type measured struct {
	Provider

	key     string
	metrics *clientMetrics
}

func (p measured) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	started := time.Now()
	res, err := p.Provider.Embed(ctx, req)

	name, ok := genAIProviders[p.key]
	if !ok {
		name = genaiconv.ProviderNameAttr(p.key)
	}

	p.metrics.embedding.Record(ctx, time.Since(started).Seconds(), genaiconv.OperationNameEmbeddings, name,
		withError(err, p.metrics.embedding.AttrRequestModel(req.ModelRef))...)

	return res, err
}

func (p measured) Rerank(ctx context.Context, req RerankRequest) (RerankResult, error) {
	started := time.Now()
	res, err := p.Provider.Rerank(ctx, req)

	p.metrics.rerank.Record(ctx, time.Since(started).Seconds(), req.ModelRef, p.key, withError(err)...)

	return res, err
}

// withError adds error.type to attrs when the call failed.
func withError(err error, attrs ...attribute.KeyValue) []attribute.KeyValue {
	if err != nil {
		attrs = append(attrs, semconv.ErrorType(err))
	}

	return attrs
}
