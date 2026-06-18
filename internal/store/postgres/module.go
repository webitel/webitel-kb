package postgres

import (
	"context"

	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/config"
)

var Module = fx.Module("store",
	fx.Provide(ProvideStore),
	fx.Invoke(func(*Store) {}),
)

func ProvideStore(cfg *config.Config, lc fx.Lifecycle) (*Store, error) {
	s := New(cfg.Postgres.DSN)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return s.Open(ctx)
		},
		OnStop: func(_ context.Context) error {
			return s.Close()
		},
	})

	return s, nil
}
