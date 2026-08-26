package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/outbox"
)

// brokerPublisher adapts the AMQP publisher to the watermill interface. The
// watermill topic is the broker routing key; the exchange is fixed per
// publisher, so the poison queue cannot end up on the indexing exchange.
type brokerPublisher struct {
	broker   Broker
	exchange string
	timeout  time.Duration
}

func (f *Forwarder) publisherFor(exchange string) message.Publisher {
	return &brokerPublisher{broker: f.broker, exchange: exchange, timeout: f.cfg.PublishTimeout}
}

func (p *brokerPublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, msg := range msgs {
		ctx, cancel := context.WithTimeout(msg.Context(), p.timeout)

		err := p.broker.Publish(ctx, p.exchange, topic, msg.Payload,
			amqp.Table{event.ReindexMessageIDHeader: msg.UUID})

		cancel()

		if err != nil {
			return err
		}
	}

	return nil
}

// Close is a no-op: the broker connection outlives any single term and is
// closed by the forwarder.
func (p *brokerPublisher) Close() error { return nil }

// errNotMarked keeps a message out of the poison queue: it must not be
// acknowledged while the article still looks pending.
var errNotMarked = errors.New("relay: article was not marked as failed")

// markFailed records the article as unindexable once the retries are spent.
// It sits inside the poison queue middleware, which acknowledges the row right
// after: without this the article would stay pending with nothing left to
// deliver it.
func (f *Forwarder) markFailed(next message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		out, err := next(msg)
		if err == nil {
			return out, nil
		}

		articleID, parseErr := strconv.ParseInt(msg.Metadata.Get(outbox.MetadataRoutingKey), 10, 64)
		if parseErr != nil {
			f.log.Error("undeliverable message carries no article id",
				slog.String("uuid", msg.UUID), slog.Any("error", err))

			return out, err
		}

		if markErr := f.store.MarkIndexFailed(msg.Context(), articleID); markErr != nil {
			return out, fmt.Errorf("%w: %w (marking failed: %w)", errNotMarked, err, markErr)
		}

		f.log.Error("article will not be indexed from this event",
			slog.Int64("article_id", articleID),
			slog.String("uuid", msg.UUID),
			slog.Any("error", err))

		return out, err
	}
}
