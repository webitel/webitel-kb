package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
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

type fakeOutbox struct{}

func (o *fakeOutbox) Database() (*pgxpool.Pool, error) { return nil, errors.New("not used") }

func (o *fakeOutbox) CleanupOutbox(context.Context, time.Duration, int) (int64, error) {
	return 0, nil
}

func (o *fakeOutbox) Backlog(context.Context) (int64, time.Duration, error) { return 0, 0, nil }

func testForwarder(broker Broker, store Outbox) *Forwarder {
	return New(
		Config{PublishTimeout: time.Second},
		store,
		broker,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
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
	if err := publish.forward(publish.publisherFor(event.ReindexExchange))(reindexMessage("")); !errors.Is(err, errUndeliverable) {
		t.Fatalf("error = %v, want an undeliverable message", err)
	}

	if len(broker.published) != 0 {
		t.Error("the message was published anyway")
	}
}

func TestPoisonAndRetryFilters(t *testing.T) {
	undeliverable := fmt.Errorf("%w: bad envelope", errUndeliverable)

	tests := []struct {
		name       string
		err        error
		wantPoison bool
		wantRetry  bool
	}{
		{name: "broker failure", err: errors.New("rabbitmq connection not available"), wantRetry: true},
		{name: "undeliverable", err: undeliverable, wantPoison: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := poisoned(tt.err); got != tt.wantPoison {
				t.Errorf("poisoned = %v, want %v", got, tt.wantPoison)
			}

			if got := retryable(middleware.RetryParams{Err: tt.err}); got != tt.wantRetry {
				t.Errorf("retryable = %v, want %v", got, tt.wantRetry)
			}
		})
	}
}
