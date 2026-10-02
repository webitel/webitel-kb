package relay

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/messagingconv"

	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/kbconv"
	"github.com/webitel/webitel-go-kit/infra/otel/semconv/v0.2.0/outboxconv"

	"github.com/webitel/webitel-kb/internal/model"
)

// meterName is the instrumentation scope of the relay.
const meterName = "github.com/webitel/webitel-kb/internal/relay"

var relayStates = []outboxconv.RelayStateAttr{outboxconv.RelayStateLeader, outboxconv.RelayStateFollower}

var indexStates = map[int32]kbconv.ArticleIndexStateAttr{
	model.IndexStatePending:  kbconv.ArticleIndexStatePending,
	model.IndexStateIndexing: kbconv.ArticleIndexStateIndexing,
	model.IndexStateIndexed:  kbconv.ArticleIndexStateIndexed,
	model.IndexStateFailed:   kbconv.ArticleIndexStateFailed,
}

// relayMetrics records the work of the relay. Readings of the outbox and of
// the article indexes are reported only while this instance leads.
type relayMetrics struct {
	sent     messagingconv.ClientSentMessages
	poisoned outboxconv.RelayPoisoned

	mu      sync.Mutex
	leading bool
	backlog *backlogReading
	indexes map[int32]int64
}

type backlogReading struct {
	count  int64
	oldest time.Duration
}

func newRelayMetrics(provider metric.MeterProvider) *relayMetrics {
	meter := provider.Meter(meterName)
	m := &relayMetrics{}

	var err error

	if m.sent, err = messagingconv.NewClientSentMessages(meter); err != nil {
		otel.Handle(err)
	}

	if m.poisoned, err = outboxconv.NewRelayPoisoned(meter); err != nil {
		otel.Handle(err)
	}

	if err = m.observe(meter); err != nil {
		otel.Handle(err)
	}

	return m
}

func (m *relayMetrics) observe(meter metric.Meter) error {
	count, err := outboxconv.NewEventCountObservable(meter)
	if err != nil {
		return err
	}

	age, err := outboxconv.NewEventAgeObservable(meter)
	if err != nil {
		return err
	}

	status, err := outboxconv.NewRelayStatusObservable(meter)
	if err != nil {
		return err
	}

	indexes, err := kbconv.NewArticleIndexCountObservable(meter)
	if err != nil {
		return err
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

	return err
}

// published counts one attempt to publish an outbox event to the exchange.
func (m *relayMetrics) published(ctx context.Context, exchange string, err error) {
	attrs := []attribute.KeyValue{m.sent.AttrDestinationName(exchange)}
	if err != nil {
		attrs = append(attrs, semconv.ErrorType(err))
	}

	m.sent.Add(ctx, 1, string(messagingconv.OperationTypeSend), messagingconv.SystemRabbitMQ, attrs...)
}

// poison counts an outbox event moved to the poison queue.
func (m *relayMetrics) poison(ctx context.Context) {
	m.poisoned.Add(ctx, 1)
}

// lead marks the start or the end of a relay term. The end drops the
// readings: another instance reports them from now on.
func (m *relayMetrics) lead(leading bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leading = leading

	if !leading {
		m.backlog = nil
		m.indexes = nil
	}
}

// readBacklog keeps the latest reading of the undelivered outbox.
func (m *relayMetrics) readBacklog(count int64, oldest time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backlog = &backlogReading{count: count, oldest: oldest}
}

// readIndexStates keeps the latest count of live articles by index state.
func (m *relayMetrics) readIndexStates(counts map[int32]int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.indexes = counts
}

func boolValue(b bool) int64 {
	if b {
		return 1
	}

	return 0
}
