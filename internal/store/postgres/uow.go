package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-kb/internal/store"
)

// txBeginner starts database transactions. *pgxpool.Pool satisfies it; the seam
// exists so the unit of work is testable without a live database, and because
// the pool itself is only created on service start, after providers run.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// unitOfWork implements store.UnitOfWork over a pgx pool. The zero querier runs
// operations directly on the pool; inside WithinTransaction it is the open
// transaction.
type unitOfWork struct {
	db      txBeginner
	querier Querier
}

var _ store.UnitOfWork = (*unitOfWork)(nil)

// NewUnitOfWork returns a unit of work running on db.
func NewUnitOfWork(db txBeginner) *unitOfWork {
	uow := &unitOfWork{db: db}

	// A pool is also a Querier; a bare beginner (tests) simply has no direct
	// querier until a transaction opens.
	if querier, ok := db.(Querier); ok {
		uow.querier = querier
	}

	return uow
}

// EmbeddingModelStore returns the registry store bound to the current querier.
// Constructed on every call: the store is a bare struct over the querier, and
// building it fresh keeps the transactional value-copy of the unit of work
// trivially correct — there is no cached store bound to an older querier.
func (u *unitOfWork) EmbeddingModelStore() store.EmbeddingModelStore {
	return &embeddingModelStore{db: u.querier}
}

// WithinTransaction executes fn within one transaction. If this unit of work is
// already transactional, fn joins the open transaction instead of nesting. A
// panic inside fn rolls the transaction back and is re-raised.
func (u *unitOfWork) WithinTransaction(ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error) error {
	// Already inside a transaction: join it.
	if _, ok := u.querier.(pgx.Tx); ok {
		return fn(ctx, u)
	}

	tx, err := u.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// Value-copy carries every field — including any added later — over to the
	// transactional instance; only the querier switches to the transaction.
	txUow := *u
	txUow.querier = tx

	defer func() {
		if p := recover(); p != nil {
			// Roll back even when ctx is already canceled: with the canceled ctx
			// pgx cannot send ROLLBACK and destroys the pooled connection.
			_ = tx.Rollback(context.WithoutCancel(ctx))

			panic(p)
		}
	}()

	if err := fn(ctx, &txUow); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil {
			return fmt.Errorf("tx: %w (rollback: %v)", err, rbErr)
		}

		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
