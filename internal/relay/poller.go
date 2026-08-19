// Package relay ships pending outbox envelopes to the message broker without
// inspecting them. The instance that holds the cluster leadership publishes in
// outbox id order; the others stand by. Publishing is confirmed by the broker
// before a row is marked, so delivery is at-least-once and duplicates are the
// consumer's normal case.
package relay

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/webitel/webitel-kb/infra/pubsub/reliable"
	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
)

const (
	// backlogLogEvery bounds how often the leader reports outbox depth while
	// a backlog exists.
	backlogLogEvery = 30 * time.Second

	// detachedOpTimeout bounds the work that must run even during shutdown:
	// stamping an already-confirmed publish prefix.
	detachedOpTimeout = 5 * time.Second
)

// Outbox is the relay's view of the queue.
type Outbox interface {
	FetchUnpublished(ctx context.Context, limit int) ([]model.OutboxEvent, error)
	MarkPublished(ctx context.Context, ids []int64) error
	Backlog(ctx context.Context) (count int64, oldest time.Duration, err error)
}

// Elector runs the callback while this instance leads the cluster.
type Elector interface {
	Run(ctx context.Context, lead func(ctx context.Context) error)
}

// Broker publishes envelopes with delivery confirmation.
type Broker interface {
	Declare(ctx context.Context, topology reliable.Topology) error
	Publish(ctx context.Context, exchange, routingKey string, msg reliable.Message) error
	Close() error
}

// Config is captured at construction; the poller never rereads the shared
// service configuration.
type Config struct {
	// Interval separates ticks while the outbox is drained or the instance
	// stands by.
	Interval time.Duration

	// Batch is the maximum number of envelopes one tick publishes. A full
	// batch triggers an immediate next tick.
	Batch int

	// PublishTimeout bounds one publish including the broker confirmation.
	PublishTimeout time.Duration
}

// Poller runs the relay loop.
type Poller struct {
	cfg     Config
	outbox  Outbox
	broker  Broker
	elector Elector
	log     *slog.Logger

	cancel         context.CancelFunc
	done           chan struct{}
	lastBacklogLog time.Time
}

// New assembles a poller; Start launches it.
func New(cfg Config, outbox Outbox, broker Broker, elector Elector, log *slog.Logger) *Poller {
	return &Poller{
		cfg:     cfg,
		outbox:  outbox,
		broker:  broker,
		elector: elector,
		log:     log,
	}
}

// Start launches the relay loop on its own context: the loop outlives the
// startup phase and stops only via Stop.
func (p *Poller) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go p.run(ctx)
}

// Stop halts the loop and waits for the in-flight tick bounded by ctx. The
// broker connection is closed either way: leaking it on a slow shutdown would
// be worse than closing under a still-running tick.
func (p *Poller) Stop(ctx context.Context) error {
	p.cancel()

	var err error

	select {
	case <-p.done:
	case <-ctx.Done():
		err = ctx.Err()
	}

	if closeErr := p.broker.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	return err
}

func (p *Poller) run(ctx context.Context) {
	defer close(p.done)

	p.elector.Run(ctx, p.lead)
}

// lead relays while this instance holds leadership. A database failure ends
// the term so a healthier instance can take over; publish failures do not,
// they only slow the ticks down.
func (p *Poller) lead(ctx context.Context) error {
	if err := p.broker.Declare(ctx, reindexTopology()); err != nil {
		return fmt.Errorf("relay: declare topology: %w", err)
	}

	b := newBackoff(time.Second, 30*time.Second)

	for ctx.Err() == nil {
		published, fetched, err := p.tick(ctx)

		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}

			return err
		case fetched > 0 && published == 0:
			// The head of the queue cannot be published (broker down or a
			// failing message): stay leader, retry with growing pauses.
			sleep(ctx, b.next())
		case fetched == p.cfg.Batch:
			// A full batch means more is probably waiting: drain now.
			b.reset()
		default:
			b.reset()
			sleep(ctx, p.cfg.Interval)
		}
	}

	return nil
}

// tick publishes one batch in outbox id order, stopping at the first failure,
// and marks the contiguous successful prefix. A publish failure is not an
// error: the unpublished tail is retried next tick. Only database failures
// propagate.
func (p *Poller) tick(ctx context.Context) (published, fetched int, err error) {
	events, err := p.outbox.FetchUnpublished(ctx, p.cfg.Batch)
	if err != nil {
		return 0, 0, err
	}

	if len(events) == 0 {
		return 0, 0, nil
	}

	ids := make([]int64, 0, len(events))

	for i := range events {
		e := &events[i]

		if perr := p.publish(ctx, e); perr != nil {
			p.log.Error("kb.relay.publish_failed",
				slog.Int64("outbox_id", e.ID),
				slog.Int64("article_id", e.ArticleID),
				slog.Any("error", perr))

			break
		}

		ids = append(ids, e.ID)
	}

	if len(ids) > 0 {
		// The prefix is already confirmed by the broker; the stamp must
		// survive a shutdown-canceled ctx, or every graceful restart would
		// republish it.
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachedOpTimeout)
		err := p.outbox.MarkPublished(markCtx, ids)

		cancel()

		if err != nil {
			return len(ids), len(events), err
		}
	}

	p.log.Debug("kb.relay.tick",
		slog.Int("fetched", len(events)),
		slog.Int("published", len(ids)))

	p.observeBacklog(ctx, len(ids) < len(events))

	return len(ids), len(events), nil
}

// publish sends one envelope, bounding the broker confirmation wait.
func (p *Poller) publish(ctx context.Context, e *model.OutboxEvent) error {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.PublishTimeout)
	defer cancel()

	return p.broker.Publish(ctx, event.ReindexExchange, event.ReindexRoutingKey(e.ArticleID), reliable.Message{
		Body:        e.Payload,
		MessageID:   strconv.FormatInt(e.ID, 10),
		ContentType: event.ReindexContentType,
	})
}

// observeBacklog reports outbox depth: always after a publish failure, and at
// most once per backlogLogEvery otherwise. The worker-side metrics only see
// messages that already reached the broker, so this is the sole signal of a
// stuck relay.
func (p *Poller) observeBacklog(ctx context.Context, failed bool) {
	if !failed && time.Since(p.lastBacklogLog) < backlogLogEvery {
		return
	}

	p.lastBacklogLog = time.Now()

	count, oldest, err := p.outbox.Backlog(ctx)
	if err != nil {
		p.log.Debug("kb.relay.backlog_failed", slog.Any("error", err))

		return
	}

	if count == 0 {
		return
	}

	p.log.Info("kb.relay.backlog",
		slog.Int64("unpublished", count),
		slog.Duration("oldest_age", oldest))
}

// reindexTopology renders the broker objects of the contract package for
// declaration.
func reindexTopology() reliable.Topology {
	return reliable.Topology{
		Exchanges: []reliable.Exchange{
			{Name: event.ReindexExchange, Kind: event.ReindexExchangeType},
			{Name: event.ReindexDLX, Kind: event.ReindexDLXType},
		},
		Queues: []reliable.Queue{
			{Name: event.ReindexQueue, Args: event.ReindexQueueArgs()},
			{Name: event.ReindexDLQ},
		},
		Bindings: []reliable.Binding{
			{Queue: event.ReindexQueue, Exchange: event.ReindexExchange, Key: event.ReindexQueueBinding},
			{Queue: event.ReindexDLQ, Exchange: event.ReindexDLX, Key: ""},
		},
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// backoff yields exponentially growing delays between an initial value and a
// cap; reset starts the progression over after a success.
type backoff struct {
	initial time.Duration
	limit   time.Duration
	current time.Duration
}

func newBackoff(initial, limit time.Duration) *backoff {
	return &backoff{initial: initial, limit: limit}
}

func (b *backoff) next() time.Duration {
	if b.current == 0 {
		b.current = b.initial
	} else {
		b.current *= 2
	}

	if b.current > b.limit {
		b.current = b.limit
	}

	return b.current
}

func (b *backoff) reset() {
	b.current = 0
}
