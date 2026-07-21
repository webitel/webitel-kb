package postgres

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	otelpgx "github.com/webitel/webitel-go-kit/infra/otel/instrumentation/pgx"
)

type Store struct {
	dsn string

	// mu guards pool: Open/Close run on the service lifecycle while Database
	// may be called from request goroutines.
	mu   sync.RWMutex
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

	s.mu.Lock()
	s.pool = pool
	s.mu.Unlock()

	slog.Debug("kb.store.connection_opened", slog.String("message", "postgres: connection opened"))

	return nil
}

// Database returns the connection pool, or an error until Open has run.
func (s *Store) Database() (*pgxpool.Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.pool == nil {
		return nil, errors.New("postgres: connection is not opened")
	}

	return s.pool, nil
}

// The Store itself acts as the pool-backed Querier and transaction beginner,
// resolving the pool on every call: providers wire the unit of work before the
// service starts, but the pool exists only after Open has run.

// Begin starts a transaction on the pool.
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	pool, err := s.Database()
	if err != nil {
		return nil, err
	}

	return pool.Begin(ctx)
}

// Exec runs a statement on the pool.
func (s *Store) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	pool, err := s.Database()
	if err != nil {
		return pgconn.CommandTag{}, err
	}

	return pool.Exec(ctx, sql, args...)
}

// Query runs a query on the pool.
func (s *Store) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	pool, err := s.Database()
	if err != nil {
		return nil, err
	}

	return pool.Query(ctx, sql, args...)
}

// QueryRow runs a single-row query on the pool. QueryRow cannot fail by
// contract, so a pool error surfaces on Scan.
func (s *Store) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	pool, err := s.Database()
	if err != nil {
		return errRow{err: err}
	}

	return pool.QueryRow(ctx, sql, args...)
}

// errRow delivers a row-acquisition error through the pgx.Row contract.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// Close releases the connection pool.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pool != nil {
		s.pool.Close()
		s.pool = nil

		slog.Debug("kb.store.connection_closed", slog.String("message", "postgres: connection closed"))
	}

	return nil
}
