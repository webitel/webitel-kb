package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/outbox"
)

type publishedMessage struct {
	exchange   string
	routingKey string
	body       []byte
	headers    amqp.Table
}

type fakeBroker struct {
	published []publishedMessage
	err       error
}

func (b *fakeBroker) Declare(context.Context) error { return nil }

func (b *fakeBroker) Publish(
	_ context.Context, exchange, routingKey string, body []byte, headers amqp.Table,
) error {
	b.published = append(b.published, publishedMessage{exchange, routingKey, body, headers})

	return b.err
}

func (b *fakeBroker) Close() error { return nil }

type fakeOutbox struct {
	failed []int64
	err    error

	backlog      int64
	oldest       time.Duration
	failedCount  int64
	observeError error
}

func (o *fakeOutbox) Database() (*pgxpool.Pool, error) { return nil, errors.New("not used") }

func (o *fakeOutbox) CleanupOutbox(context.Context, time.Duration, int) (int64, error) {
	return 0, nil
}

func (o *fakeOutbox) Backlog(context.Context) (int64, time.Duration, error) {
	return o.backlog, o.oldest, o.observeError
}

func (o *fakeOutbox) CountIndexFailed(context.Context) (int64, error) {
	return o.failedCount, o.observeError
}

func (o *fakeOutbox) MarkIndexFailed(_ context.Context, articleID int64) error {
	o.failed = append(o.failed, articleID)

	return o.err
}

type fakeMetrics struct {
	backlog   []int64
	oldest    []time.Duration
	failed    []int64
	published []error
	poisoned  int
}

func (m *fakeMetrics) Leading(bool) {}

func (m *fakeMetrics) Backlog(count int64, oldest time.Duration) {
	m.backlog = append(m.backlog, count)
	m.oldest = append(m.oldest, oldest)
}

func (m *fakeMetrics) IndexFailed(count int64) { m.failed = append(m.failed, count) }

func (m *fakeMetrics) Published(_ context.Context, err error) { m.published = append(m.published, err) }

func (m *fakeMetrics) Poisoned(context.Context) { m.poisoned++ }

func testForwarder(broker Broker, store Outbox) *Forwarder {
	return New(
		Config{PublishTimeout: time.Second},
		store,
		broker,
		nil,
		&fakeMetrics{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func metricsOf(f *Forwarder) *fakeMetrics {
	return f.metrics.(*fakeMetrics)
}

func reindexMessage(routingKey string) *message.Message {
	msg := message.NewMessage("uuid-1", []byte(`{"type":"article.reindex"}`))
	if routingKey != "" {
		msg.Metadata.Set(outbox.MetadataRoutingKey, routingKey)
	}

	return msg
}

// The stored routing key must reach the broker unchanged: it is what keeps a
// future partitioned consumer able to route by article.
func TestForwardPublishesUnderTheStoredRoutingKey(t *testing.T) {
	broker := &fakeBroker{}

	publish := testForwarder(broker, &fakeOutbox{})
	if err := publish.forward(publish.publisherFor(event.ReindexExchange))(reindexMessage("42")); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if len(broker.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(broker.published))
	}

	got := broker.published[0]
	if got.exchange != event.ReindexExchange {
		t.Errorf("exchange = %q, want %q", got.exchange, event.ReindexExchange)
	}

	if got.routingKey != "42" {
		t.Errorf("routing key = %q, want %q", got.routingKey, "42")
	}

	if got.headers[event.ReindexMessageIDHeader] != "uuid-1" {
		t.Errorf("message id header = %v, want the watermill uuid", got.headers[event.ReindexMessageIDHeader])
	}
}

func TestForwardRejectsAMessageWithoutRoutingKey(t *testing.T) {
	broker := &fakeBroker{}

	publish := testForwarder(broker, &fakeOutbox{})
	if err := publish.forward(publish.publisherFor(event.ReindexExchange))(reindexMessage("")); err == nil {
		t.Fatal("forward accepted a message with no routing key")
	}

	if len(broker.published) != 0 {
		t.Error("the message was published anyway")
	}
}

// The poison queue acknowledges the row right after this middleware, so the
// article has to be marked here or it stays pending with nothing to deliver it.
func TestMarkFailedRecordsTheArticle(t *testing.T) {
	store := &fakeOutbox{}
	handlerErr := errors.New("broker is gone")

	handler := testForwarder(&fakeBroker{}, store).markFailed(
		func(*message.Message) ([]*message.Message, error) { return nil, handlerErr },
	)

	_, err := handler(reindexMessage("42"))
	if !errors.Is(err, handlerErr) {
		t.Fatalf("error = %v, want the handler error to survive", err)
	}

	if len(store.failed) != 1 || store.failed[0] != 42 {
		t.Fatalf("marked %v, want [42]", store.failed)
	}
}

func TestMarkFailedLeavesASucceedingMessageAlone(t *testing.T) {
	store := &fakeOutbox{}

	handler := testForwarder(&fakeBroker{}, store).markFailed(
		func(*message.Message) ([]*message.Message, error) { return nil, nil },
	)

	if _, err := handler(reindexMessage("42")); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(store.failed) != 0 {
		t.Fatalf("marked %v on success", store.failed)
	}
}

func TestForwardRecordsThePublication(t *testing.T) {
	brokerErr := errors.New("broker is gone")

	tests := []struct {
		name string
		err  error
	}{
		{name: "delivered"},
		{name: "rejected by the broker", err: brokerErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publish := testForwarder(&fakeBroker{err: tt.err}, &fakeOutbox{})
			_ = publish.forward(publish.publisherFor(event.ReindexExchange))(reindexMessage("42"))

			got := metricsOf(publish).published
			if len(got) != 1 || !errors.Is(got[0], tt.err) {
				t.Fatalf("recorded %v, want one publication with error %v", got, tt.err)
			}
		})
	}
}

func TestPoisonQueueCountsWhatItSetsAside(t *testing.T) {
	tests := []struct {
		name      string
		exchange  string
		brokerErr error
		want      int
	}{
		{name: "set aside", exchange: event.ReindexDLX, want: 1},
		{name: "poison queue unreachable", exchange: event.ReindexDLX, brokerErr: errors.New("broker is gone"), want: 0},
		{name: "indexing exchange", exchange: event.ReindexExchange, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testForwarder(&fakeBroker{err: tt.brokerErr}, &fakeOutbox{})
			_ = f.publisherFor(tt.exchange).Publish("42", reindexMessage("42"))

			if got := metricsOf(f).poisoned; got != tt.want {
				t.Fatalf("poisoned = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestObserveReportsTheReadings(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeOutbox
		want  int
	}{
		{name: "read", store: &fakeOutbox{backlog: 3, oldest: time.Minute, failedCount: 2}, want: 1},
		{name: "unavailable", store: &fakeOutbox{observeError: errors.New("db")}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testForwarder(&fakeBroker{}, tt.store)
			f.observeBacklog(context.Background())
			f.observeIndexFailed(context.Background())

			m := metricsOf(f)
			if len(m.backlog) != tt.want || len(m.failed) != tt.want {
				t.Fatalf("readings: backlog %v, failed %v, want %d of each", m.backlog, m.failed, tt.want)
			}

			if tt.want == 1 && (m.backlog[0] != 3 || m.oldest[0] != time.Minute || m.failed[0] != 2) {
				t.Fatalf("readings: backlog %v oldest %v failed %v", m.backlog, m.oldest, m.failed)
			}
		})
	}
}
