package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-kb/internal/model"
)

// FetchUnpublished returns up to limit pending envelopes in outbox id order,
// which is publish order.
func (s *Store) FetchUnpublished(ctx context.Context, limit int) ([]model.OutboxEvent, error) {
	rows, err := s.Query(ctx, `
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
func (s *Store) MarkPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	_, err := s.Exec(ctx,
		`UPDATE kb.outbox_events SET published_at = now() WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("postgres: mark published: %w", err)
	}

	return nil
}

// Backlog reports how many envelopes still await publication and the age of
// the oldest one.
func (s *Store) Backlog(ctx context.Context) (count int64, oldest time.Duration, err error) {
	var seconds float64

	err = s.QueryRow(ctx, `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)
  FROM kb.outbox_events
 WHERE published_at IS NULL`).Scan(&count, &seconds)
	if err != nil {
		return 0, 0, fmt.Errorf("postgres: outbox backlog: %w", err)
	}

	return count, time.Duration(seconds * float64(time.Second)), nil
}
