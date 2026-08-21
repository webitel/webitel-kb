package leader

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// fakeAPI stands in for Consul and records the call order, which is what the
// step-down contract is about.
type fakeAPI struct {
	mu sync.Mutex

	entries []*api.SessionEntry
	pairs   []*api.KVPair
	calls   []string

	holder     string
	sessionID  string
	createErr  error
	acquired   bool
	acquireErr error
	renewErr   error
	watchErr   error

	watch chan string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{sessionID: "s1", acquired: true, watch: make(chan string, 1)}
}

func (f *fakeAPI) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, call)
}

func (f *fakeAPI) history() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func (f *fakeAPI) Create(_ context.Context, entry *api.SessionEntry) (string, error) {
	f.mu.Lock()
	f.entries = append(f.entries, entry)
	f.calls = append(f.calls, "create")
	f.mu.Unlock()

	return f.sessionID, f.createErr
}

func (f *fakeAPI) Acquire(_ context.Context, pair *api.KVPair) (bool, error) {
	f.mu.Lock()
	f.pairs = append(f.pairs, pair)
	f.calls = append(f.calls, "acquire")
	f.mu.Unlock()

	return f.acquired, f.acquireErr
}

func (f *fakeAPI) Renew(_, _ string, done <-chan struct{}) error {
	f.record("renew")

	if f.renewErr != nil {
		return f.renewErr
	}

	<-done
	// A slow unwind: the elector must wait for it, not race it.
	time.Sleep(50 * time.Millisecond)
	f.record("renew_returned")

	return nil
}

func (f *fakeAPI) Watch(ctx context.Context, _ string, index uint64, wait time.Duration) (string, uint64, error) {
	if f.watchErr != nil {
		return "", 0, f.watchErr
	}

	if wait == 0 {
		f.mu.Lock()
		defer f.mu.Unlock()

		return f.holder, index + 1, nil
	}

	select {
	case holder := <-f.watch:
		return holder, index + 1, nil
	case <-ctx.Done():
		f.record("watch_returned")

		return "", 0, ctx.Err()
	}
}

func (f *fakeAPI) Destroy(_ context.Context, _ string) error {
	f.record("destroy")

	return nil
}

func testElector(t *testing.T, fake *fakeAPI) *ConsulElector {
	t.Helper()

	return newElector(fake, "node-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// runElector campaigns in the background and returns a stop function.
func runElector(t *testing.T, e *ConsulElector, lead func(context.Context) error) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		e.Run(ctx, lead)
	}()

	return func() {
		cancel()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("the elector did not stop")
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatal("condition was not met in time")
}

func TestSessionParametersArePinned(t *testing.T) {
	fake := newFakeAPI()
	fake.acquired = false
	fake.holder = "someone-else"
	e := testElector(t, fake)

	stop := runElector(t, e, func(context.Context) error { return nil })
	waitFor(t, func() bool { return len(fake.history()) >= 2 })
	stop()

	entry := fake.entries[0]
	if entry.TTL != "10s" || entry.Behavior != api.SessionBehaviorRelease {
		t.Fatalf("session entry = %+v", entry)
	}

	if entry.LockDelay <= 0 || entry.LockDelay > time.Millisecond {
		t.Fatalf("lock delay = %v, want a small non-zero value", entry.LockDelay)
	}

	pair := fake.pairs[0]
	if pair.Key != "service/webitel-kb/leader" || string(pair.Value) != "node-1" || pair.Session != "s1" {
		t.Fatalf("kv pair = %+v", pair)
	}
}

func TestStandbyDoesNotRunTheWork(t *testing.T) {
	fake := newFakeAPI()
	fake.acquired = false
	fake.holder = "someone-else"
	e := testElector(t, fake)

	var leads int

	stop := runElector(t, e, func(context.Context) error {
		leads++

		return nil
	})
	waitFor(t, func() bool { return len(fake.history()) >= 2 })
	stop()

	if leads != 0 {
		t.Fatalf("the work ran %d times without the key", leads)
	}

	if !slices.Contains(fake.history(), "destroy") {
		t.Fatal("a standby attempt leaked its session")
	}
}

func TestStandbyWakesOnRelease(t *testing.T) {
	// The standby blocks on the key, so a handover does not wait out the
	// retry interval.
	fake := newFakeAPI()
	fake.acquired = false
	fake.holder = "someone-else"
	e := testElector(t, fake)

	stop := runElector(t, e, func(context.Context) error { return nil })
	waitFor(t, func() bool { return len(fake.history()) >= 2 })

	before := len(fake.history())
	fake.watch <- ""

	waitFor(t, func() bool { return len(fake.history()) > before })
	stop()
}

func TestLostKeyStopsTheWorkBeforeTheSessionGoes(t *testing.T) {
	fake := newFakeAPI()
	e := testElector(t, fake)

	started := make(chan struct{})

	var once sync.Once

	stop := runElector(t, e, func(ctx context.Context) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		fake.record("work_stopped")

		return nil
	})

	<-started

	fake.watch <- "someone-else"

	waitFor(t, func() bool { return slices.Contains(fake.history(), "destroy") })
	stop()

	history := fake.history()
	destroyed := slices.Index(history, "destroy")

	// The session may only go once the work and both helpers are done;
	// otherwise the key frees while this instance still publishes.
	for _, before := range []string{"work_stopped", "renew_returned"} {
		at := slices.Index(history, before)
		if at < 0 || at > destroyed {
			t.Fatalf("call order = %v, want %q before the session is destroyed", history, before)
		}
	}
}

func TestRenewFailureEndsTheTerm(t *testing.T) {
	fake := newFakeAPI()
	fake.renewErr = errors.New("consul unreachable")
	e := testElector(t, fake)

	ended := make(chan struct{})

	stop := runElector(t, e, func(ctx context.Context) error {
		<-ctx.Done()

		select {
		case <-ended:
		default:
			close(ended)
		}

		return nil
	})

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("a failed renewal did not end the term")
	}

	stop()
}

func TestWatchFailureEndsTheTerm(t *testing.T) {
	// An instance that cannot read the key cannot prove it still leads.
	fake := newFakeAPI()
	fake.watchErr = errors.New("consul unreachable")
	e := testElector(t, fake)

	ended := make(chan struct{})

	stop := runElector(t, e, func(ctx context.Context) error {
		<-ctx.Done()

		select {
		case <-ended:
		default:
			close(ended)
		}

		return nil
	})

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("an unreadable key did not end the term")
	}

	stop()
}

func TestWorkFailureReleasesTheKeyAndBacksOff(t *testing.T) {
	// A failing term must free the key for a healthier instance without
	// spinning on Consul: the next campaign waits out the cooldown.
	fake := newFakeAPI()
	e := testElector(t, fake)

	stop := runElector(t, e, func(context.Context) error {
		return errors.New("database down")
	})

	waitFor(t, func() bool { return slices.Contains(fake.history(), "destroy") })
	time.Sleep(300 * time.Millisecond)
	stop()

	campaigns := 0

	for _, call := range fake.history() {
		if call == "create" {
			campaigns++
		}
	}

	if campaigns > 1 {
		t.Fatalf("campaigned %d times inside the cooldown: %v", campaigns, fake.history())
	}
}

func TestCreateFailureSkipsTheWork(t *testing.T) {
	fake := newFakeAPI()
	fake.createErr = errors.New("consul unreachable")
	e := testElector(t, fake)

	var leads int

	stop := runElector(t, e, func(context.Context) error {
		leads++

		return nil
	})
	waitFor(t, func() bool { return len(fake.history()) >= 1 })
	stop()

	if leads != 0 {
		t.Fatalf("the work ran %d times without a session", leads)
	}
}
