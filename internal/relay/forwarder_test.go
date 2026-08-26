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
}

func (o *fakeOutbox) Database() (*pgxpool.Pool, error) { return nil, errors.New("not used") }

func (o *fakeOutbox) CleanupOutbox(context.Context, time.Duration, int) (int64, error) {
	return 0, nil
}

func (o *fakeOutbox) Backlog(context.Context) (int64, time.Duration, error) { return 0, 0, nil }

func (o *fakeOutbox) MarkIndexFailed(_ context.Context, articleID int64) error {
	o.failed = append(o.failed, articleID)

	return o.err
}

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
