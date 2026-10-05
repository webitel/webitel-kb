package postgres

import (
	"context"

	"go.uber.org/fx"

	"github.com/webitel/webitel-go-kit/infra/health"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/internal/store"
)

var Module = fx.Module("store",
	fx.Provide(ProvideStore, ProvideUnitOfWork),
	fx.Invoke(registerHealth),
)

// registerHealth adds the database as an informational check.
func registerHealth(s *Store, h *health.Registry) {
	h.Informational("postgres", s.Ping)
}

// ProvideUnitOfWork exposes the unit of work over the store. The store resolves
// its pool per call, so providing before the pool exists is safe.
func ProvideUnitOfWork(s *Store) store.UnitOfWork {
	return NewUnitOfWork(s)
}

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
