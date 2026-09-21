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

	"github.com/webitel/webitel-kb/internal/model"
)

// Outcome label values.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// providerBuckets bound one provider call, up to its timeout.
var providerBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30}

// Metrics records what the service does. Gauges report only readings taken
// while this instance leads the relay, so a follower exports none of them.
type Metrics struct {
	embedding metric.Float64Histogram
	rerank    metric.Float64Histogram
	published metric.Int64Counter
	poisoned  metric.Int64Counter

	mu      sync.Mutex
	leading bool
	backlog *backlogReading
	failed  *int64
}

type backlogReading struct {
	count  int64
	oldest time.Duration
}

// Provide builds the instruments over the global meter provider.
func Provide() (*Metrics, error) {
	return New(otel.Meter(model.ServiceName))
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

	if m.embedding, err = meter.Float64Histogram("kb_embedding_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of one embedding call to a provider"),
		metric.WithExplicitBucketBoundaries(providerBuckets...),
	); err != nil {
		return nil, fmt.Errorf("metrics: embedding: %w", err)
	}

	if m.rerank, err = meter.Float64Histogram("kb_rerank_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of one rerank call to a provider"),
		metric.WithExplicitBucketBoundaries(providerBuckets...),
	); err != nil {
		return nil, fmt.Errorf("metrics: rerank: %w", err)
	}

	if m.published, err = meter.Int64Counter("kb_outbox_published_total",
		metric.WithUnit("{event}"),
		metric.WithDescription("Attempts to publish an outbox event to the broker"),
	); err != nil {
		return nil, fmt.Errorf("metrics: published: %w", err)
	}

	if m.poisoned, err = meter.Int64Counter("kb_outbox_poisoned_total",
		metric.WithUnit("{event}"),
		metric.WithDescription("Outbox events set aside after the retries were spent"),
	); err != nil {
		return nil, fmt.Errorf("metrics: poisoned: %w", err)
	}

	if err = m.observe(meter); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Metrics) observe(meter metric.Meter) error {
	backlog, err := meter.Int64ObservableGauge("kb_outbox_backlog",
		metric.WithUnit("{event}"),
		metric.WithDescription("Outbox events not delivered to the broker yet"))
	if err != nil {
		return fmt.Errorf("metrics: backlog: %w", err)
	}

	oldest, err := meter.Float64ObservableGauge("kb_outbox_oldest_age_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Age of the oldest outbox event not delivered yet"))
	if err != nil {
		return fmt.Errorf("metrics: oldest age: %w", err)
	}

	failed, err := meter.Int64ObservableGauge("kb_articles_index_failed",
		metric.WithUnit("{article}"),
		metric.WithDescription("Articles whose last version could not be indexed"))
	if err != nil {
		return fmt.Errorf("metrics: index failed: %w", err)
	}

	leader, err := meter.Int64ObservableGauge("kb_relay_leader",
		metric.WithDescription("Whether this instance runs the outbox relay"))
	if err != nil {
		return fmt.Errorf("metrics: leader: %w", err)
	}

	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		m.mu.Lock()
		defer m.mu.Unlock()

		var lead int64
		if m.leading {
			lead = 1
		}

		o.ObserveInt64(leader, lead)

		if m.backlog != nil {
			o.ObserveInt64(backlog, m.backlog.count)
			o.ObserveFloat64(oldest, m.backlog.oldest.Seconds())
		}

		if m.failed != nil {
			o.ObserveInt64(failed, *m.failed)
		}

		return nil
	}, backlog, oldest, failed, leader)
	if err != nil {
		return fmt.Errorf("metrics: callback: %w", err)
	}

	return nil
}

// Embedding records one embedding call to a provider.
func (m *Metrics) Embedding(ctx context.Context, provider, modelRef string, took time.Duration, err error) {
	m.embedding.Record(ctx, took.Seconds(), metric.WithAttributes(providerAttrs(provider, modelRef, err)...))
}

// Rerank records one rerank call to a provider.
func (m *Metrics) Rerank(ctx context.Context, provider, modelRef string, took time.Duration, err error) {
	m.rerank.Record(ctx, took.Seconds(), metric.WithAttributes(providerAttrs(provider, modelRef, err)...))
}

// Published counts one attempt to publish an outbox event.
func (m *Metrics) Published(ctx context.Context, err error) {
	m.published.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome(err))))
}

// Poisoned counts an outbox event set aside for good.
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
		m.failed = nil
	}
}

// Backlog keeps the latest reading of the undelivered outbox.
func (m *Metrics) Backlog(count int64, oldest time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backlog = &backlogReading{count: count, oldest: oldest}
}

// IndexFailed keeps the latest count of articles that failed indexing.
func (m *Metrics) IndexFailed(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failed = &count
}

func providerAttrs(provider, modelRef string, err error) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("model", modelRef),
		attribute.String("outcome", outcome(err)),
	}
}

func outcome(err error) string {
	if err != nil {
		return OutcomeError
	}

	return OutcomeOK
}
