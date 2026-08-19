package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/webitel/webitel-kb/infra/pubsub/reliable"
	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
)

var errNoMoreBatches = errors.New("script exhausted")

type fakeOutbox struct {
	batches  [][]model.OutboxEvent
	fetchErr error // returned once the scripted batches run out

	marked  [][]int64
	markErr error

	backlogCalls int
	fetchCalls   int
}

func (s *fakeOutbox) FetchUnpublished(context.Context, int) ([]model.OutboxEvent, error) {
	s.fetchCalls++

	if len(s.batches) == 0 {
		return nil, s.fetchErr
	}

	batch := s.batches[0]
	s.batches = s.batches[1:]

	return batch, nil
}

func (s *fakeOutbox) MarkPublished(_ context.Context, ids []int64) error {
	if s.markErr != nil {
		return s.markErr
	}

	s.marked = append(s.marked, ids)

	return nil
}

func (s *fakeOutbox) Backlog(context.Context) (int64, time.Duration, error) {
	s.backlogCalls++

	return 1, time.Second, nil
}

// directElector promotes the instance at once, the way Consul would.
type directElector struct {
	runs int
	err  error
}

func (e *directElector) Run(ctx context.Context, lead func(context.Context) error) {
	for ctx.Err() == nil {
		e.runs++

		if e.err = lead(ctx); e.err != nil {
			return
		}
	}
}

type pubCall struct {
	exchange string
	key      string
	msg      reliable.Message
}

type fakeBroker struct {
	calls      []pubCall
	failAt     int // 0-based call index to fail; -1 never fails
	declares   int
	declareErr error
	closed     bool
}

func (b *fakeBroker) Publish(_ context.Context, exchange, key string, msg reliable.Message) error {
	call := len(b.calls)
	b.calls = append(b.calls, pubCall{exchange: exchange, key: key, msg: msg})

	if b.failAt >= 0 && call == b.failAt {
		return errors.New("broker down")
	}

	return nil
}

func (b *fakeBroker) Declare(context.Context, reliable.Topology) error {
	b.declares++

	return b.declareErr
}

func (b *fakeBroker) Close() error {
	b.closed = true

	return nil
}

func outboxEvents(ids ...int64) []model.OutboxEvent {
	events := make([]model.OutboxEvent, 0, len(ids))
	for _, id := range ids {
		events = append(events, model.OutboxEvent{
			ID:        id,
			ArticleID: id * 10,
			// Whitespace and unsorted keys: any re-encode would change these
			// bytes, so byte-equality asserts true pass-through.
			Payload: []byte(fmt.Sprintf(`{ "version_id": %d, "article_id": %d }`, id+100, id*10)),
		})
	}

	return events
}

func newTestPoller(outbox Outbox, broker Broker) *Poller {
	return newTestPollerWith(outbox, broker, &directElector{})
}

func newTestPollerWith(outbox Outbox, broker Broker, elector Elector) *Poller {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	return New(Config{Interval: time.Millisecond, Batch: 2, PublishTimeout: time.Second},
		outbox, broker, elector, log)
}

func TestTickPublishesBatchInOrder(t *testing.T) {
	outbox := &fakeOutbox{batches: [][]model.OutboxEvent{outboxEvents(1, 2, 3)}}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	published, fetched, err := p.tick(t.Context())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	if published != 3 || fetched != 3 {
		t.Fatalf("published/fetched = %d/%d, want 3/3", published, fetched)
	}

	wantKeys := []string{"10", "20", "30"}
	wantIDs := []string{"1", "2", "3"}

	for i, call := range broker.calls {
		if call.exchange != event.ReindexExchange {
			t.Fatalf("call %d exchange = %q", i, call.exchange)
		}

		if call.key != wantKeys[i] {
			t.Fatalf("call %d routing key = %q, want %q", i, call.key, wantKeys[i])
		}

		if call.msg.MessageID != wantIDs[i] {
			t.Fatalf("call %d message id = %q, want %q", i, call.msg.MessageID, wantIDs[i])
		}

		if call.msg.ContentType != event.ReindexContentType {
			t.Fatalf("call %d content type = %q", i, call.msg.ContentType)
		}

		// The defining pass-through property: stored bytes reach the broker
		// untouched.
		if !bytes.Equal(call.msg.Body, outboxEvents(1, 2, 3)[i].Payload) {
			t.Fatalf("call %d body = %s, want the stored payload verbatim", i, call.msg.Body)
		}
	}

	if len(outbox.marked) != 1 || len(outbox.marked[0]) != 3 {
		t.Fatalf("marked = %v, want one batch of 3", outbox.marked)
	}

	for i, id := range outbox.marked[0] {
		if id != int64(i+1) {
			t.Fatalf("marked ids = %v, want [1 2 3]", outbox.marked[0])
		}
	}
}

func TestTickStopsAtFirstFailure(t *testing.T) {
	outbox := &fakeOutbox{batches: [][]model.OutboxEvent{outboxEvents(1, 2, 3, 4, 5)}}
	broker := &fakeBroker{failAt: 2}
	p := newTestPoller(outbox, broker)

	published, fetched, err := p.tick(t.Context())
	if err != nil {
		t.Fatalf("tick must not fail on a publish error, got %v", err)
	}

	if got := len(broker.calls); got != 3 {
		t.Fatalf("Publish called %d times, want 3 (2 ok + 1 failed, tail skipped)", got)
	}

	if published != 2 || fetched != 5 {
		t.Fatalf("published/fetched = %d/%d, want 2/5", published, fetched)
	}

	if len(outbox.marked) != 1 || len(outbox.marked[0]) != 2 ||
		outbox.marked[0][0] != 1 || outbox.marked[0][1] != 2 {
		t.Fatalf("marked = %v, want the [1 2] prefix only", outbox.marked)
	}

	if outbox.backlogCalls == 0 {
		t.Fatal("a failed tick must report the backlog")
	}
}

func TestTickHeadFailure(t *testing.T) {
	outbox := &fakeOutbox{batches: [][]model.OutboxEvent{outboxEvents(1, 2)}}
	broker := &fakeBroker{failAt: 0}
	p := newTestPoller(outbox, broker)

	published, fetched, err := p.tick(t.Context())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	if published != 0 || fetched != 2 {
		t.Fatalf("published/fetched = %d/%d, want 0/2", published, fetched)
	}

	if len(outbox.marked) != 0 {
		t.Fatalf("marked = %v, want nothing", outbox.marked)
	}
}

func TestTickEmptyBatch(t *testing.T) {
	outbox := &fakeOutbox{}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	published, fetched, err := p.tick(t.Context())
	if err != nil || published != 0 || fetched != 0 {
		t.Fatalf("tick = %d/%d/%v, want 0/0/nil", published, fetched, err)
	}

	if len(broker.calls) != 0 {
		t.Fatalf("Publish called on an empty batch: %v", broker.calls)
	}
}

func TestTickMarkFailurePropagates(t *testing.T) {
	outbox := &fakeOutbox{
		batches: [][]model.OutboxEvent{outboxEvents(1)},
		markErr: errors.New("db down"),
	}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	if _, _, err := p.tick(t.Context()); err == nil {
		t.Fatal("a database failure must propagate out of the tick")
	}
}

func TestLeadDeclareFailureEndsTerm(t *testing.T) {
	outbox := &fakeOutbox{}
	broker := &fakeBroker{failAt: -1, declareErr: errors.New("amqp down")}
	p := newTestPoller(outbox, broker)

	if err := p.lead(t.Context()); err == nil {
		t.Fatal("declare failure did not end the term")
	}

	if outbox.fetchCalls != 0 {
		t.Fatal("outbox was read before topology was ready")
	}
}

func TestLeadDrainsThenReturnsDBError(t *testing.T) {
	// Batch size is 2: a full first batch must trigger an immediate drain
	// tick, the short second batch a paced one; the scripted fetch error then
	// ends the term so another instance can take over.
	outbox := &fakeOutbox{
		batches: [][]model.OutboxEvent{
			outboxEvents(1, 2),
			outboxEvents(3),
		},
		fetchErr: errNoMoreBatches,
	}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	err := p.lead(t.Context())
	if !errors.Is(err, errNoMoreBatches) {
		t.Fatalf("lead error = %v, want the database failure", err)
	}

	if len(broker.calls) != 3 {
		t.Fatalf("published %d messages, want 3", len(broker.calls))
	}

	if outbox.fetchCalls != 3 {
		t.Fatalf("fetch called %d times, want 3", outbox.fetchCalls)
	}
}

func TestLeadStopsWithTheTerm(t *testing.T) {
	outbox := &fakeOutbox{}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := p.lead(ctx); err != nil {
		t.Fatalf("a canceled term reported an error: %v", err)
	}
}

func TestStartStop(t *testing.T) {
	outbox := &fakeOutbox{}
	broker := &fakeBroker{failAt: -1}
	elector := &directElector{}
	p := newTestPollerWith(outbox, broker, elector)

	p.Start()

	// Let it spin through a few empty ticks.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if !broker.closed {
		t.Fatal("Stop did not close the broker")
	}

	if elector.runs == 0 {
		t.Fatal("the loop never campaigned for leadership")
	}
}

func TestObserveBacklogCadence(t *testing.T) {
	outbox := &fakeOutbox{batches: [][]model.OutboxEvent{
		outboxEvents(1),
		outboxEvents(2),
		outboxEvents(3),
	}}
	broker := &fakeBroker{failAt: -1}
	p := newTestPoller(outbox, broker)

	// First successful tick reports (initial observation)...
	if _, _, err := p.tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if outbox.backlogCalls != 1 {
		t.Fatalf("backlog calls after first tick = %d, want 1", outbox.backlogCalls)
	}

	// ...the next successful tick inside the throttle window must not.
	if _, _, err := p.tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if outbox.backlogCalls != 1 {
		t.Fatalf("backlog calls inside throttle window = %d, want still 1", outbox.backlogCalls)
	}

	// A publish failure reports regardless of the window.
	broker.failAt = len(broker.calls)

	if _, _, err := p.tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if outbox.backlogCalls != 2 {
		t.Fatalf("backlog calls after failure = %d, want 2", outbox.backlogCalls)
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		name    string
		initial time.Duration
		max     time.Duration
		steps   []time.Duration
	}{
		{
			name:    "doubles to cap",
			initial: time.Second,
			max:     10 * time.Second,
			steps:   []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second},
		},
		{
			name:    "initial above half cap",
			initial: 8 * time.Second,
			max:     10 * time.Second,
			steps:   []time.Duration{8 * time.Second, 10 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBackoff(tt.initial, tt.max)

			for i, want := range tt.steps {
				if got := b.next(); got != want {
					t.Fatalf("step %d = %v, want %v", i, got, want)
				}
			}

			b.reset()

			if got := b.next(); got != tt.initial {
				t.Fatalf("after reset = %v, want %v", got, tt.initial)
			}
		})
	}
}

func TestReindexTopologyMatchesContract(t *testing.T) {
	topo := reindexTopology()

	if len(topo.Exchanges) != 2 || len(topo.Queues) != 2 || len(topo.Bindings) != 2 {
		t.Fatalf("unexpected topology shape: %+v", topo)
	}

	if topo.Queues[0].Name != event.ReindexQueue ||
		topo.Queues[0].Args["x-dead-letter-exchange"] != event.ReindexDLX {
		t.Fatalf("indexing queue misses the dead-letter argument: %+v", topo.Queues[0])
	}

	if topo.Bindings[0].Key != event.ReindexQueueBinding {
		t.Fatalf("indexing binding key = %q", topo.Bindings[0].Key)
	}
}
