package relay

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/webitel/webitel-go-kit/infra/discovery"
	"github.com/webitel/webitel-go-kit/infra/discovery/consul"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/store/postgres"
)

const (
	// sessionTTL and lockDelay decide how long a dead instance keeps
	// leadership: Consul invalidates a TTL session at up to twice its TTL and
	// then holds the key for the lock delay. Ten seconds is the Consul
	// minimum and keeps a takeover inside the re-indexing lag budget; the
	// lock delay must be set explicitly, because Consul reads a missing one
	// as its own fifteen second default.
	sessionTTL = 10 * time.Second
	lockDelay  = 100 * time.Millisecond
)

// Module wires the relay into the application. The fx.Invoke makes the
// forwarder part of every server start: the relay is not optional.
var Module = fx.Module("relay",
	fx.Provide(provideBroker, provideElector, provideForwarder),
	fx.Invoke(registerForwarder),
)

func provideBroker(cfg *config.Config, log *slog.Logger) (Broker, error) {
	return NewBroker(cfg.Pubsub.URL, cfg.Relay.PublishTimeout, log)
}

func provideElector(cfg *config.Config, log *slog.Logger) (Elector, error) {
	return consul.NewLeaderElector(
		cfg.Consul.Addr,
		model.ServiceName,
		discovery.GenerateInstanceID(model.ServiceName),
		log,
		consul.WithSessionTTL(sessionTTL),
		consul.WithLockDelay(lockDelay),
	)
}

// provideForwarder wires the relay from the service config. The broker URL
// scheme is already validated at config load; the relay intentionally does not
// check Pubsub.Driver, whose default names the broker product, not the
// protocol.
func provideForwarder(
	cfg *config.Config, store *postgres.Store, broker Broker, elector Elector, log *slog.Logger,
) *Forwarder {
	return New(Config{
		PollInterval:    cfg.Relay.PollInterval,
		PublishTimeout:  cfg.Relay.PublishTimeout,
		Retention:       cfg.Relay.Retention,
		CleanupInterval: cfg.Relay.CleanupInterval,
		CleanupBatch:    cfg.Relay.CleanupBatch,
	}, store, broker, elector, log)
}

// registerForwarder ties the relay to the fx lifecycle. Depending on
// *Forwarder (which depends on the postgres store) guarantees it stops before
// the connection pool closes.
func registerForwarder(p *Forwarder, lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			p.Start()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			return p.Stop(ctx)
		},
	})
}
