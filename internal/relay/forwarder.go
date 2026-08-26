// Package relay moves outbox rows to the broker. Exactly one instance does it
// at a time: the work runs under a Consul leader election, so a second
// instance stands by and takes over when the leader goes away.
package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	watermillsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/outbox"
)

const (
	handlerName = "webitel.kb.outbox_forwarder"

	// retryLimit and the intervals below have to stay well inside the
	// subscriber ack deadline, otherwise a message is redelivered while it is
	// still being retried.
	// backlogInterval is how often a leading instance reports what it has not
	// delivered yet; a stalled relay shows up here and nowhere else.
	backlogInterval = 30 * time.Second

	retryLimit           = 5
	retryInitialInterval = 200 * time.Millisecond
	retryMaxInterval     = 5 * time.Second
	retryMultiplier      = 2.0
)

// Outbox is the database side of the relay.
type Outbox interface {
	Database() (*pgxpool.Pool, error)
	CleanupOutbox(ctx context.Context, retention time.Duration, batch int) (int64, error)
	Backlog(ctx context.Context) (int64, time.Duration, error)
	MarkIndexFailed(ctx context.Context, articleID int64) error
}

// Elector runs the relay on exactly one instance at a time.
type Elector interface {
	Run(ctx context.Context, onStart func(ctx context.Context) error, onStop func())
}

// Config is captured at construction; the relay never rereads the shared
// service configuration.
type Config struct {
	// PollInterval separates outbox reads when the last one found nothing.
	PollInterval time.Duration

	// PublishTimeout bounds one broker publication.
	PublishTimeout time.Duration

	// Retention is how long an acknowledged row is kept, and CleanupBatch how
	// many rows one delete statement removes.
	Retention       time.Duration
	CleanupInterval time.Duration
	CleanupBatch    int
}

// Forwarder relays outbox rows to the broker while this instance leads.
type Forwarder struct {
	cfg     Config
	store   Outbox
	broker  Broker
	elector Elector
	log     *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func New(cfg Config, store Outbox, broker Broker, elector Elector, log *slog.Logger) *Forwarder {
	return &Forwarder{
		cfg:     cfg,
		store:   store,
		broker:  broker,
		elector: elector,
		log:     log.With(slog.String("component", "relay")),
		done:    make(chan struct{}),
	}
}

// Start runs the election in the background; leadership decides when the
// forwarder actually works.
func (f *Forwarder) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel

	go func() {
		defer close(f.done)

		f.elector.Run(ctx, f.lead, func() {
			f.log.Warn("relay stepped down")
		})
	}()
}

// Stop ends the term and waits for the forwarder to settle before the caller
// tears down the pool or the broker.
func (f *Forwarder) Stop(ctx context.Context) error {
	if f.cancel == nil {
		return f.broker.Close()
	}

	f.cancel()

	select {
	case <-f.done:
	case <-ctx.Done():
		return ctx.Err()
	}

	return f.broker.Close()
}

// lead is the leader-only work: declare the topology, then run the router until
// the term ends. The router is built per term because watermill cannot restart
// one; returning an error steps down so another instance can take over.
func (f *Forwarder) lead(ctx context.Context) error {
	if err := f.broker.Declare(ctx); err != nil {
		return fmt.Errorf("relay: declare topology: %w", err)
	}

	router, err := f.newRouter()
	if err != nil {
		return err
	}

	f.log.Info("relay leading", slog.String("consumer_group", outbox.ConsumerGroup))

	var background sync.WaitGroup

	background.Add(2)

	go func() { defer background.Done(); f.cleanupLoop(ctx) }()
	go func() { defer background.Done(); f.observeLoop(ctx) }()

	err = router.Run(ctx)

	background.Wait()

	return err
}

// newRouter wires the outbox subscriber to the broker publisher. Middleware is
// applied outside in: retries run closest to the handler, and only once they
// are spent does the message reach the marker and the poison queue, which sets
// a message aside only after the article carries the failure.
func (f *Forwarder) newRouter() (*message.Router, error) {
	logger := watermill.NewSlogLogger(f.log)

	pool, err := f.store.Database()
	if err != nil {
		return nil, fmt.Errorf("relay: database: %w", err)
	}

	subscriber, err := watermillsql.NewSubscriber(
		watermillsql.BeginnerFromPgx(pool),
		watermillsql.SubscriberConfig{
			ConsumerGroup:    outbox.ConsumerGroup,
			PollInterval:     f.cfg.PollInterval,
			SchemaAdapter:    outbox.SchemaAdapter(),
			OffsetsAdapter:   outbox.OffsetsAdapter(),
			InitializeSchema: false,
		},
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("relay: outbox subscriber: %w", err)
	}

	router, err := message.NewRouter(message.RouterConfig{}, logger)
	if err != nil {
		return nil, fmt.Errorf("relay: router: %w", err)
	}

	poison, err := middleware.PoisonQueueWithFilter(
		f.publisherFor(event.ReindexDLX), event.ReindexDLQ,
		func(err error) bool { return !errors.Is(err, errNotMarked) })
	if err != nil {
		return nil, fmt.Errorf("relay: poison queue: %w", err)
	}

	router.AddMiddleware(
		middleware.Recoverer,
		poison,
		f.markFailed,
		middleware.Retry{
			MaxRetries:      retryLimit,
			InitialInterval: retryInitialInterval,
			MaxInterval:     retryMaxInterval,
			Multiplier:      retryMultiplier,
			Logger:          logger,
		}.Middleware,
	)

	router.AddConsumerHandler(handlerName, outbox.Topic, subscriber,
		f.forward(f.publisherFor(event.ReindexExchange)))

	return router, nil
}

// forward hands one stored envelope to the broker under the routing key the
// writer recorded. The payload is never decoded here.
func (f *Forwarder) forward(publisher message.Publisher) message.NoPublishHandlerFunc {
	return func(msg *message.Message) error {
		key := msg.Metadata.Get(outbox.MetadataRoutingKey)
		if key == "" {
			return fmt.Errorf("relay: message %s carries no routing key", msg.UUID)
		}

		return publisher.Publish(key, msg)
	}
}

// observeLoop reports the undelivered backlog while this instance leads.
func (f *Forwarder) observeLoop(ctx context.Context) {
	ticker := time.NewTicker(backlogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		count, oldest, err := f.store.Backlog(ctx)
		if err != nil {
			if ctx.Err() == nil {
				f.log.Error("outbox backlog unavailable", slog.Any("error", err))
			}

			continue
		}

		if count > 0 {
			f.log.Info("outbox backlog",
				slog.Int64("undelivered", count),
				slog.Duration("oldest_age", oldest))
		}
	}
}

// cleanupLoop removes acknowledged rows for as long as this instance leads.
func (f *Forwarder) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(f.cfg.CleanupInterval)
	defer ticker.Stop()

	for {
		f.cleanup(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (f *Forwarder) cleanup(ctx context.Context) {
	deleted, err := f.store.CleanupOutbox(ctx, f.cfg.Retention, f.cfg.CleanupBatch)
	if err != nil {
		if ctx.Err() == nil {
			f.log.Error("outbox cleanup failed", slog.Any("error", err))
		}

		return
	}

	if deleted > 0 {
		f.log.Info("outbox rows removed", slog.Int64("deleted", deleted))
	}
}
