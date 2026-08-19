package relay

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/infra/leader"
	"github.com/webitel/webitel-kb/infra/pubsub/reliable"
	"github.com/webitel/webitel-kb/internal/store/postgres"
)

// Module wires the relay into the application. The fx.Invoke makes the poller
// part of every server start: the relay is not optional.
var Module = fx.Module("relay",
	fx.Provide(providePoller),
	fx.Invoke(registerPoller),
)

// providePoller wires the poller from the service config. The broker URL
// scheme is already validated at config load; the poller intentionally does
// not check Pubsub.Driver, whose default names the broker product, not the
// protocol.
func providePoller(
	cfg *config.Config, store *postgres.Store, elector leader.Elector, log *slog.Logger,
) *Poller {
	return New(Config{
		Interval:       cfg.Relay.Interval,
		Batch:          cfg.Relay.Batch,
		PublishTimeout: cfg.Relay.PublishTimeout,
	}, store, reliable.NewPublisher(cfg.Pubsub.URL), elector, log)
}

// registerPoller ties the loop to the fx lifecycle. Depending on *Poller
// (which depends on the postgres store) guarantees the loop stops before the
// connection pool closes.
func registerPoller(p *Poller, lc fx.Lifecycle) {
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
