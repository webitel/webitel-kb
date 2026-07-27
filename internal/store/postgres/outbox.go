package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
)

// OutboxSession is the relay's view of the outbox, pinned to one dedicated
// pool connection. The pinning is what makes the session advisory lock work:
// the lock lives on the connection's backend, so leadership lasts exactly as
// long as the connection and releases itself if the process or link dies.
type OutboxSession struct {
	conn    *pgxpool.Conn
	querier Querier
	leader  bool
}

// AcquireOutboxSession pins a dedicated connection for relay work.
func (s *Store) AcquireOutboxSession(ctx context.Context) (*OutboxSession, error) {
	pool, err := s.Database()
	if err != nil {
		return nil, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire outbox session: %w", err)
	}

	return &OutboxSession{conn: conn, querier: conn}, nil
}

// TryLeaderLock attempts the relay's session advisory lock without waiting.
// False means another live session already leads.
func (o *OutboxSession) TryLeaderLock(ctx context.Context) (bool, error) {
	var acquired bool

	err := o.querier.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1, $2)`,
		event.AdvisoryLockClassID, event.AdvisoryLockRelayID,
	).Scan(&acquired)
	if err != nil {
		return false, fmt.Errorf("postgres: outbox leader lock: %w", err)
	}

	o.leader = acquired

	return acquired, nil
}

// FetchUnpublished returns up to limit pending envelopes in outbox id order,
// which is publish order.
func (o *OutboxSession) FetchUnpublished(ctx context.Context, limit int) ([]model.OutboxEvent, error) {
	rows, err := o.querier.Query(ctx, `
SELECT id, aggregate_id, payload
  FROM kb.outbox_events
 WHERE published_at IS NULL
 ORDER BY id
 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: fetch outbox: %w", err)
	}

	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.OutboxEvent, error) {
		var e model.OutboxEvent

		err := row.Scan(&e.ID, &e.ArticleID, &e.Payload)

		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: scan outbox: %w", err)
	}

	return events, nil
}

// MarkPublished stamps the given rows as handed to the broker.
func (o *OutboxSession) MarkPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	_, err := o.querier.Exec(ctx,
		`UPDATE kb.outbox_events SET published_at = now() WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("postgres: mark published: %w", err)
	}

	return nil
}

// Backlog reports how many envelopes still await publication and the age of
// the oldest one.
func (o *OutboxSession) Backlog(ctx context.Context) (count int64, oldest time.Duration, err error) {
	var seconds float64

	err = o.querier.QueryRow(ctx, `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)
  FROM kb.outbox_events
 WHERE published_at IS NULL`).Scan(&count, &seconds)
	if err != nil {
		return 0, 0, fmt.Errorf("postgres: outbox backlog: %w", err)
	}

	return count, time.Duration(seconds * float64(time.Second)), nil
}

// Close releases the leader lock, when held, and returns the connection to
// the pool. A connection whose lock cannot be released is destroyed instead of
// being pooled: a pooled connection with a live advisory lock would leak
// leadership to an unrelated borrower. A standby session skips the unlock
// entirely (unlocking a lock that was never taken makes the server log a
// warning on every poll).
func (o *OutboxSession) Close(ctx context.Context) {
	if o.leader {
		o.leader = false

		_, err := o.querier.Exec(ctx,
			`SELECT pg_advisory_unlock($1, $2)`,
			event.AdvisoryLockClassID, event.AdvisoryLockRelayID)
		if err != nil && o.conn != nil {
			_ = o.conn.Conn().Close(context.WithoutCancel(ctx))
		}
	}

	if o.conn != nil {
		o.conn.Release()
		o.conn = nil
	}
}
