// Package leader elects a single active instance through the Consul KV lock.
package leader

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hashicorp/consul/api"
)

const (
	// leaderKey holds the node id of the current relay leader.
	leaderKey = "service/webitel-kb/leader"

	// sessionName labels the session in the Consul UI.
	sessionName = "webitel-kb-leader-lock"

	// sessionTTL bounds how long leadership survives a dead instance. Consul
	// invalidates a TTL session at up to twice its TTL, so the ten second
	// minimum keeps a takeover inside the re-index lag budget.
	sessionTTL    = "10s"
	renewInterval = "5s"

	// lockDelay must be small but NOT zero: Consul reads a zero delay as
	// "unset" and applies its own default of 15s, which would block every
	// handover for a whole extra TTL. The server clamps this to 1ms.
	lockDelay = time.Nanosecond

	// retryInterval separates attempts while another instance leads;
	// errCooldown separates attempts after a Consul failure.
	retryInterval = 10 * time.Second
	errCooldown   = 5 * time.Second

	// watchTimeout bounds one blocking read of the leader key.
	watchTimeout = 10 * time.Second
)

// Elector runs a callback while this instance holds leadership.
type Elector interface {
	Run(ctx context.Context, lead func(ctx context.Context) error)
}

// sessionAPI is the Consul surface the elector needs, kept narrow so the
// election can be tested without a live agent.
type sessionAPI interface {
	Create(ctx context.Context, entry *api.SessionEntry) (string, error)
	Renew(ttl, sessionID string, done <-chan struct{}) error
	Acquire(ctx context.Context, pair *api.KVPair) (bool, error)
	Watch(ctx context.Context, key string, index uint64, wait time.Duration) (holder string, next uint64, err error)
	Destroy(ctx context.Context, sessionID string) error
}

// ConsulElector holds the relay leadership in Consul KV.
type ConsulElector struct {
	api    sessionAPI
	log    *slog.Logger
	nodeID string
}

var _ Elector = (*ConsulElector)(nil)

// New dials the Consul agent and returns an elector for this instance.
func New(consulAddr, nodeID string, log *slog.Logger) (*ConsulElector, error) {
	cfg := api.DefaultConfig()
	cfg.Address = consulAddr

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("leader: consul client: %w", err)
	}

	return newElector(consulClient{client}, nodeID, log), nil
}

func newElector(api sessionAPI, nodeID string, log *slog.Logger) *ConsulElector {
	return &ConsulElector{
		api:    api,
		nodeID: nodeID,
		log:    log.With(slog.String("component", "leader"), slog.String("key", leaderKey)),
	}
}

// Run campaigns until ctx ends, running lead while this instance holds the
// key. An error from lead gives up leadership so another instance can try,
// and pauses this one so a broken instance cannot spin on Consul.
func (e *ConsulElector) Run(ctx context.Context, lead func(ctx context.Context) error) {
	for ctx.Err() == nil {
		if err := e.attempt(ctx, lead); err != nil {
			wait(ctx, errCooldown)
		}
	}
}

func (e *ConsulElector) attempt(ctx context.Context, lead func(ctx context.Context) error) error {
	sessionID, err := e.api.Create(ctx, &api.SessionEntry{
		Name:      sessionName,
		TTL:       sessionTTL,
		LockDelay: lockDelay,
		Behavior:  api.SessionBehaviorRelease,
	})
	if err != nil {
		if ctx.Err() == nil {
			e.log.Warn("kb.leader.session_failed", slog.Any("error", err))
		}

		wait(ctx, errCooldown)

		return nil
	}

	// The session outlives the leader context on purpose: it is destroyed
	// only after the worker has stopped, so no one takes over too early.
	defer e.destroy(ctx, sessionID)

	acquired, err := e.api.Acquire(ctx, &api.KVPair{
		Key: leaderKey, Value: []byte(e.nodeID), Session: sessionID,
	})
	if err != nil {
		if ctx.Err() == nil {
			e.log.Warn("kb.leader.acquire_failed", slog.Any("error", err))
		}

		wait(ctx, errCooldown)

		return nil
	}

	if !acquired {
		e.log.Debug("kb.leader.standby")
		e.awaitRelease(ctx)

		return nil
	}

	e.log.Info("kb.leader.promoted",
		slog.String("node_id", e.nodeID), slog.String("session", sessionID))

	return e.serve(ctx, sessionID, lead)
}

// serve runs the callback under a leader context and tears it down in order:
// cancel, wait for the worker, then let the deferred destroy release the key.
func (e *ConsulElector) serve(
	ctx context.Context, sessionID string, lead func(ctx context.Context) error,
) error {
	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	renewed := make(chan struct{})

	go func() {
		defer close(renewed)

		if err := e.api.Renew(renewInterval, sessionID, leaderCtx.Done()); err != nil {
			e.log.Warn("kb.leader.renew_failed", slog.Any("error", err))
			cancel()
		}
	}()

	watched := make(chan struct{})

	go func() {
		defer close(watched)

		e.watch(leaderCtx, sessionID)
		cancel()
	}()

	err := lead(leaderCtx)
	if err != nil {
		e.log.Error("kb.leader.work_failed", slog.Any("error", err))
	}

	cancel()
	<-watched
	<-renewed

	// A shutdown is not an alarm; losing the key while running is.
	if ctx.Err() != nil {
		e.log.Info("kb.leader.released", slog.String("node_id", e.nodeID))
	} else {
		e.log.Warn("kb.leader.demoted", slog.String("node_id", e.nodeID))
	}

	return err
}

// awaitRelease blocks until the key changes or retryInterval passes, so a
// standby takes over right after a handover instead of on the next poll.
func (e *ConsulElector) awaitRelease(ctx context.Context) {
	holder, index, err := e.api.Watch(ctx, leaderKey, 0, 0)
	if err != nil {
		if ctx.Err() == nil {
			e.log.Warn("kb.leader.watch_failed", slog.Any("error", err))
		}

		wait(ctx, errCooldown)

		return
	}

	if holder == "" {
		// Nobody holds it: the acquire lost a race that is already over.
		return
	}

	if _, _, err := e.api.Watch(ctx, leaderKey, index, retryInterval); err != nil && ctx.Err() == nil {
		wait(ctx, errCooldown)
	}
}

// watch blocks on the leader key and returns as soon as it no longer belongs
// to this session.
func (e *ConsulElector) watch(ctx context.Context, sessionID string) {
	var index uint64

	for ctx.Err() == nil {
		holder, next, err := e.api.Watch(ctx, leaderKey, index, watchTimeout)
		if err != nil {
			if ctx.Err() == nil {
				e.log.Warn("kb.leader.watch_failed", slog.Any("error", err))
			}

			return
		}

		if holder != sessionID {
			return
		}

		index = next
	}
}

func (e *ConsulElector) destroy(ctx context.Context, sessionID string) {
	// Shutdown cancels ctx, yet the session still has to go: leaving it
	// alive would hold the key for a whole TTL.
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), errCooldown)
	defer cancel()

	if err := e.api.Destroy(destroyCtx, sessionID); err != nil {
		e.log.Warn("kb.leader.destroy_failed", slog.Any("error", err))
	}
}

func wait(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
