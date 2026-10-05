package relay

import (
	"context"
	"errors"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/webitel/webitel-kb/internal/event"
)

// brokerPublisher adapts the AMQP publisher to the watermill interface. The
// watermill topic is the broker routing key; the exchange is fixed per
// publisher, so the poison queue cannot end up on the indexing exchange.
type brokerPublisher struct {
	broker   Broker
	exchange string
	timeout  time.Duration
	metrics  *relayMetrics
}

func (f *Forwarder) publisherFor(exchange string) message.Publisher {
	return &brokerPublisher{broker: f.broker, exchange: exchange, timeout: f.cfg.PublishTimeout, metrics: f.metrics}
}

func (p *brokerPublisher) Publish(topic string, msgs ...*message.Message) error {
	for _, msg := range msgs {
		ctx, cancel := context.WithTimeout(msg.Context(), p.timeout)

		err := p.broker.Publish(ctx, p.exchange, topic, msg.Payload,
			amqp.Table{event.ReindexMessageIDHeader: msg.UUID})

		cancel()

		p.metrics.published(msg.Context(), p.exchange, err)

		if err != nil {
			return err
		}

		if p.exchange == event.ReindexDLX {
			p.metrics.poison(msg.Context())
		}
	}

	return nil
}

// Close is a no-op: the broker connection outlives any single term and is
// closed by the forwarder.
func (p *brokerPublisher) Close() error { return nil }

// errUndeliverable marks a message no retry can publish. Any other error is a
// broker failure: the row stays in the outbox and is delivered again.
var errUndeliverable = errors.New("relay: message cannot be delivered")
