package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	watermillsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/outbox"
	"github.com/webitel/webitel-kb/internal/store"
)

// outboxStore writes the transactional outbox on the caller's querier.
type outboxStore struct {
	db Querier
}

var _ store.OutboxStore = (*outboxStore)(nil)

// PublishReindex stores one envelope in the caller's transaction. The row
// carries the broker routing key; the relay never decodes the payload.
func (o *outboxStore) PublishReindex(ctx context.Context, e event.ArticleReindex) error {
	payload, err := e.Marshal()
	if err != nil {
		return fmt.Errorf("postgres: outbox event: %w", err)
	}

	tx, ok := o.db.(pgx.Tx)
	if !ok {
		return errors.Internal(
			"an outbox event needs the transaction of the change it describes",
			errors.WithID("kb.outbox.no_transaction"),
		)
	}

	publisher, err := watermillsql.NewPublisher(
		watermillsql.TxFromPgx(tx),
		watermillsql.PublisherConfig{SchemaAdapter: outbox.SchemaAdapter()},
		watermill.NopLogger{},
	)
	if err != nil {
		return fmt.Errorf("postgres: outbox publisher: %w", err)
	}

	msg := message.NewMessage(watermill.NewUUID(), payload)
	msg.SetContext(ctx)
	msg.Metadata.Set(outbox.MetadataRoutingKey, event.ReindexRoutingKey(e.ArticleID))
	msg.Metadata.Set(outbox.MetadataType, e.Type)
	msg.Metadata.Set(outbox.MetadataSchema, strconv.Itoa(e.Schema))

	if err := publisher.Publish(outbox.Topic, msg); err != nil {
		return fmt.Errorf("postgres: write outbox: %w", err)
	}

	return nil
}

// CleanupOutbox removes rows the relay has already acknowledged and that are
// older than retention, in batches so one call cannot hold locks for long.
// Rows above the acknowledged offset are never touched.
func (s *Store) CleanupOutbox(ctx context.Context, retention time.Duration, batch int) (int64, error) {
	const query = `
WITH acked AS (
    SELECT last_processed_transaction_id, offset_acked
      FROM ` + outbox.OffsetsTable + `
     WHERE consumer_group = $1
), doomed AS (
    SELECT m.transaction_id, m."offset"
      FROM ` + outbox.MessagesTable + ` m, acked a
     WHERE (m.transaction_id, m."offset") <= (a.last_processed_transaction_id, a.offset_acked)
       AND m.created_at < now() - $2::interval
     LIMIT $3
)
DELETE FROM ` + outbox.MessagesTable + ` m
 USING doomed d
 WHERE m.transaction_id = d.transaction_id
   AND m."offset" = d."offset"`

	var total int64

	for {
		tag, err := s.Exec(ctx, query, outbox.ConsumerGroup, retention.String(), batch)
		if err != nil {
			return total, fmt.Errorf("postgres: cleanup outbox: %w", err)
		}

		total += tag.RowsAffected()

		if tag.RowsAffected() < int64(batch) {
			return total, nil
		}

		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// Backlog reports how many rows the relay has not acknowledged yet and the age
// of the oldest of them.
func (s *Store) Backlog(ctx context.Context) (count int64, oldest time.Duration, err error) {
	const query = `
SELECT count(*),
       COALESCE(EXTRACT(EPOCH FROM now() - min(m.created_at)), 0)
  FROM ` + outbox.MessagesTable + ` m
  LEFT JOIN ` + outbox.OffsetsTable + ` o ON o.consumer_group = $1
 WHERE o.consumer_group IS NULL
    OR (m.transaction_id, m."offset") > (o.last_processed_transaction_id, o.offset_acked)`

	var seconds float64

	err = s.QueryRow(ctx, query, outbox.ConsumerGroup).Scan(&count, &seconds)
	if err != nil {
		return 0, 0, fmt.Errorf("postgres: outbox backlog: %w", err)
	}

	return count, time.Duration(seconds * float64(time.Second)), nil
}

// MarkIndexFailed records that an envelope could not be delivered and the
// article will not be indexed from it. Without this the article would sit in
// the pending state forever while the row itself is already acknowledged.
func (s *Store) MarkIndexFailed(ctx context.Context, articleID int64) error {
	_, err := s.Exec(ctx,
		`UPDATE kb.article SET index_state = $2 WHERE id = $1`,
		articleID, model.IndexStateFailed)
	if err != nil {
		return fmt.Errorf("postgres: mark index failed: %w", err)
	}

	return nil
}
