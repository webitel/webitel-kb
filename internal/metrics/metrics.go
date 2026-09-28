// Package metrics holds the instruments of the service.
package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/semconv/v1.40.0/genaiconv"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/messagingconv"

	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/outboxconv"

	"github.com/webitel/webitel-kb/internal/model"
)

// providerBuckets are the GenAI boundaries: from 10 ms, doubling up to 81.92 s.
var providerBuckets = func() []float64 {
	b := make([]float64, 14)
	for i := range b {
		b[i] = 0.01 * float64(int(1)<<i)
	}

	return b
}()

// operationSend names a publish in messaging.operation.name.
const operationSend = "send"

// genAIProviders renames the providers the GenAI conventions know.
var genAIProviders = map[string]genaiconv.ProviderNameAttr{
	"gemini": genaiconv.ProviderNameGCPGemini,
}

var relayStates = []outboxconv.RelayStateAttr{outboxconv.RelayStateLeader, outboxconv.RelayStateFollower}

var indexStates = map[int32]kbconv.ArticleIndexStateAttr{
	model.IndexStatePending:  kbconv.ArticleIndexStatePending,
	model.IndexStateIndexing: kbconv.ArticleIndexStateIndexing,
	model.IndexStateIndexed:  kbconv.ArticleIndexStateIndexed,
	model.IndexStateFailed:   kbconv.ArticleIndexStateFailed,
}

// Metrics records what the service does. Readings of the outbox and of the
// indexes are reported only while this instance leads the relay.
type Metrics struct {
	embedding genaiconv.ClientOperationDuration
	rerank    kbconv.RerankDuration
	sent      messagingconv.ClientSentMessages
	poisoned  outboxconv.RelayPoisoned

	mu      sync.Mutex
	leading bool
	backlog *backlogReading
	indexes map[int32]int64
}

type backlogReading struct {
	count  int64
	oldest time.Duration
}

// Provide builds the instruments over the global meter provider.
func Provide() (*Metrics, error) {
	return New(otel.Meter(model.ServiceName, metric.WithInstrumentationVersion(model.Version)))
}

// Noop discards every measurement.
func Noop() *Metrics {
	m, _ := New(noop.NewMeterProvider().Meter(model.ServiceName))

	return m
}

// New builds the instruments over the meter.
func New(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}

	var err error

	if m.embedding, err = genaiconv.NewClientOperationDuration(meter,
		metric.WithExplicitBucketBoundaries(providerBuckets...)); err != nil {
		return nil, fmt.Errorf("metrics: embedding: %w", err)
	}

	if m.rerank, err = kbconv.NewRerankDuration(meter,
		metric.WithExplicitBucketBoundaries(providerBuckets...)); err != nil {
		return nil, fmt.Errorf("metrics: rerank: %w", err)
	}

	if m.sent, err = messagingconv.NewClientSentMessages(meter); err != nil {
		return nil, fmt.Errorf("metrics: sent messages: %w", err)
	}

	if m.poisoned, err = outboxconv.NewRelayPoisoned(meter); err != nil {
		return nil, fmt.Errorf("metrics: poisoned: %w", err)
	}

	if err = m.observe(meter); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Metrics) observe(meter metric.Meter) error {
	count, err := outboxconv.NewEventCountObservable(meter)
	if err != nil {
		return fmt.Errorf("metrics: event count: %w", err)
	}

	age, err := outboxconv.NewEventAgeObservable(meter)
	if err != nil {
		return fmt.Errorf("metrics: event age: %w", err)
	}

	status, err := outboxconv.NewRelayStatusObservable(meter)
	if err != nil {
		return fmt.Errorf("metrics: relay status: %w", err)
	}

	indexes, err := kbconv.NewArticleIndexCountObservable(meter)
	if err != nil {
		return fmt.Errorf("metrics: index count: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		m.mu.Lock()
		defer m.mu.Unlock()

		current := outboxconv.RelayStateFollower
		if m.leading {
			current = outboxconv.RelayStateLeader
		}

		for _, s := range relayStates {
			o.ObserveInt64(status.Inst(), boolValue(s == current), metric.WithAttributes(status.AttrRelayState(s)))
		}

		if m.backlog != nil {
			o.ObserveInt64(count.Inst(), m.backlog.count)
			o.ObserveFloat64(age.Inst(), m.backlog.oldest.Seconds())
		}

		if m.indexes != nil {
			for state, attr := range indexStates {
				o.ObserveInt64(indexes.Inst(), m.indexes[state],
					metric.WithAttributes(indexes.AttrArticleIndexState(attr)))
			}
		}

		return nil
	}, count.Inst(), age.Inst(), status.Inst(), indexes.Inst())
	if err != nil {
		return fmt.Errorf("metrics: callback: %w", err)
	}

	return nil
}

// Embedding records one embedding call to a provider.
func (m *Metrics) Embedding(ctx context.Context, provider, modelRef string, took time.Duration, err error) {
	name, ok := genAIProviders[provider]
	if !ok {
		name = genaiconv.ProviderNameAttr(provider)
	}

	m.embedding.Record(ctx, took.Seconds(), genaiconv.OperationNameEmbeddings, name,
		withError(err, m.embedding.AttrRequestModel(modelRef))...)
}

// Rerank records one rerank call to a provider.
func (m *Metrics) Rerank(ctx context.Context, provider, modelRef string, took time.Duration, err error) {
	m.rerank.Record(ctx, took.Seconds(), modelRef, provider, withError(err)...)
}

// Published counts one attempt to publish an outbox event to the exchange.
func (m *Metrics) Published(ctx context.Context, exchange string, err error) {
	m.sent.Add(ctx, 1, operationSend, messagingconv.SystemRabbitMQ,
		withError(err, m.sent.AttrDestinationTemplate(exchange))...)
}

// Poisoned counts an outbox event moved to the poison queue.
func (m *Metrics) Poisoned(ctx context.Context) {
	m.poisoned.Add(ctx, 1)
}

// Leading marks the start or the end of a relay term. The end drops the
// readings: another instance reports them from now on.
func (m *Metrics) Leading(leading bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leading = leading

	if !leading {
		m.backlog = nil
		m.indexes = nil
	}
}

// Backlog keeps the latest reading of the undelivered outbox.
func (m *Metrics) Backlog(count int64, oldest time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backlog = &backlogReading{count: count, oldest: oldest}
}

// IndexStates keeps the latest count of live articles by index state.
func (m *Metrics) IndexStates(counts map[int32]int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.indexes = counts
}

// withError adds error.type to attrs when the call failed.
func withError(err error, attrs ...attribute.KeyValue) []attribute.KeyValue {
	if err != nil {
		attrs = append(attrs, semconv.ErrorType(err))
	}

	return attrs
}

func boolValue(b bool) int64 {
	if b {
		return 1
	}

	return 0
}
