package postgres

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	otelpgx "github.com/webitel/webitel-go-kit/infra/otel/instrumentation/pgx"
)

type Store struct {
	dsn  string
	pool *pgxpool.Pool
}

func New(dsn string) *Store {
	return &Store{dsn: dsn}
}

// Open parses the DSN, attaches the OpenTelemetry pgx tracer, creates the pool
// and verifies connectivity with a Ping (fail-fast on a bad/unreachable DB).
func (s *Store) Open(ctx context.Context) error {
	cfg, err := pgxpool.ParseConfig(s.dsn)
	if err != nil {
		return err
	}

	// Trace every query through OpenTelemetry.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return err
	}

	s.pool = pool

	slog.Debug("kb.store.connection_opened", slog.String("message", "postgres: connection opened"))

	return nil
}

// Close releases the connection pool.
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil

		slog.Debug("kb.store.connection_closed", slog.String("message", "postgres: connection closed"))
	}

	return nil
}
