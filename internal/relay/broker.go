package relay

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/webitel/webitel-go-kit/infra/pubsub/rabbitmq"
	rabbitslog "github.com/webitel/webitel-go-kit/infra/pubsub/rabbitmq/pkg/adapter/slog"

	"github.com/webitel/webitel-kb/internal/event"
)

// writeTimeout bounds every socket write, so a broker applying TCP pushback
// cannot hold a publication past its context.
const writeTimeout = 30 * time.Second

// Broker is the message broker side of the relay.
type Broker interface {
	Declare(ctx context.Context) error
	Publish(ctx context.Context, exchange, routingKey string, body []byte, headers amqp.Table) error
	Close() error
}

// broker publishes envelopes over AMQP. The connection is rebuilt in the
// background and replays the declared topology, so a wiped or replaced broker
// gets the exchanges and queues back without restarting the service.
type broker struct {
	conn      *rabbitmq.Connection
	publisher *rabbitmq.MessagePublisher
}

// NewBroker dials lazily, so an unavailable broker cannot keep the service from
// starting. Publications are confirmed and mandatory: a successful publish
// means the message reached a queue, and one that matched none is an error.
func NewBroker(url string, confirmTimeout time.Duration, log *slog.Logger) (Broker, error) {
	logger := rabbitslog.NewSlogLogger(log.With(slog.String("component", "broker")))

	cfg, err := rabbitmq.NewConfig(url,
		rabbitmq.WithWriteTimeout(writeTimeout),
		rabbitmq.WithLazyConnect(true),
	)
	if err != nil {
		return nil, fmt.Errorf("relay: broker config: %w", err)
	}

	conn, err := rabbitmq.NewConnection(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("relay: broker connection: %w", err)
	}

	pubCfg, err := rabbitmq.NewPublisherConfig(
		rabbitmq.WithConfirmation(true),
		rabbitmq.WithPublisherConfirmationTimeout(confirmTimeout),
		rabbitmq.WithPublisherMaxRetries(1),
		rabbitmq.WithPersistentDelivery(true),
		rabbitmq.WithMandatory(true),
	)
	if err != nil {
		return nil, fmt.Errorf("relay: publisher config: %w", err)
	}

	publisher, err := rabbitmq.NewPublisher(conn, pubCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("relay: publisher: %w", err)
	}

	return &broker{conn: conn, publisher: publisher}, nil
}

// Declare creates every object of the contract. Redeclaring with different
// properties fails the channel, so the parameters here and in the worker must
// match exactly. The dead letter queue is bound separately because a fanout
// binding carries no routing key, which DeclareQueue skips.
func (b *broker) Declare(ctx context.Context) error {
	reindex, err := rabbitmq.NewExchangeConfig(event.ReindexExchange, rabbitmq.ExchangeTypeTopic)
	if err != nil {
		return fmt.Errorf("relay: exchange config: %w", err)
	}

	dlx, err := rabbitmq.NewExchangeConfig(event.ReindexDLX, rabbitmq.ExchangeTypeFanout)
	if err != nil {
		return fmt.Errorf("relay: dlx config: %w", err)
	}

	args := event.ReindexQueueArgs()

	queueOpts := make([]rabbitmq.QueueOption, 0, len(args))
	for name, value := range args {
		queueOpts = append(queueOpts, rabbitmq.WithQueueArgument(name, value))
	}

	queue, err := rabbitmq.NewQueueConfig(event.ReindexQueue, queueOpts...)
	if err != nil {
		return fmt.Errorf("relay: queue config: %w", err)
	}

	dlq, err := rabbitmq.NewQueueConfig(event.ReindexDLQ)
	if err != nil {
		return fmt.Errorf("relay: dlq config: %w", err)
	}

	for _, exchange := range []*rabbitmq.ExchangeConfig{reindex, dlx} {
		if err := b.conn.DeclareExchange(ctx, exchange); err != nil {
			return fmt.Errorf("relay: declare %s: %w", exchange.Name, err)
		}
	}

	if err := b.conn.DeclareQueue(ctx, queue, reindex, event.ReindexQueueBinding); err != nil {
		return fmt.Errorf("relay: declare %s: %w", queue.Name, err)
	}

	if err := b.conn.DeclareQueue(ctx, dlq, nil, ""); err != nil {
		return fmt.Errorf("relay: declare %s: %w", dlq.Name, err)
	}

	if err := b.conn.BindQueue(dlq.Name, "", dlx.Name, false, nil); err != nil {
		return fmt.Errorf("relay: bind %s: %w", dlq.Name, err)
	}

	return nil
}

func (b *broker) Publish(
	ctx context.Context, exchange, routingKey string, body []byte, headers amqp.Table,
) error {
	return b.publisher.Publish(ctx, exchange, routingKey, body, headers)
}

func (b *broker) Close() error {
	_ = b.publisher.Close()

	return b.conn.Close()
}
