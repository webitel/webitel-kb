package postgres

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-kb/internal/store"
)

// fakeTx records transaction outcomes. It embeds the pgx.Tx interface so only
// the methods the unit of work actually calls need implementing; any other
// call panics, which is exactly what a test should surface.
type fakeTx struct {
	pgx.Tx

	commits   int
	rollbacks int

	commitErr   error
	rollbackErr error

	// rollbackCtxErr records ctx.Err() as seen by Rollback: the unit of work
	// must roll back on a live context even when the request one is canceled.
	rollbackCtxErr error
}

func (t *fakeTx) Commit(context.Context) error {
	t.commits++

	return t.commitErr
}

func (t *fakeTx) Rollback(ctx context.Context) error {
	t.rollbacks++
	t.rollbackCtxErr = ctx.Err()

	return t.rollbackErr
}

// fakeBeginner hands out the prepared transaction.
type fakeBeginner struct {
	tx       *fakeTx
	beginErr error
	begins   int
}

func (b *fakeBeginner) Begin(context.Context) (pgx.Tx, error) {
	b.begins++
	if b.beginErr != nil {
		return nil, b.beginErr
	}

	return b.tx, nil
}

func TestWithinTransactionCommitsOnSuccess(t *testing.T) {
	tx := &fakeTx{}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	var ran bool

	err := uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		ran = true

		return nil
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	if !ran {
		t.Error("fn did not run")
	}

	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Errorf("commits=%d rollbacks=%d, want 1/0", tx.commits, tx.rollbacks)
	}
}

func TestWithinTransactionRollsBackOnError(t *testing.T) {
	tx := &fakeTx{}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	fnErr := stderrors.New("business rule failed")

	err := uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		return fnErr
	})

	if !stderrors.Is(err, fnErr) {
		t.Fatalf("err = %v, want the fn error", err)
	}

	if tx.rollbacks != 1 || tx.commits != 0 {
		t.Errorf("commits=%d rollbacks=%d, want 0/1", tx.commits, tx.rollbacks)
	}
}

func TestWithinTransactionReportsRollbackFailure(t *testing.T) {
	tx := &fakeTx{rollbackErr: stderrors.New("connection lost")}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	fnErr := stderrors.New("business rule failed")

	err := uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		return fnErr
	})
	if err == nil {
		t.Fatal("want an error")
	}

	// Both failures must be visible.
	for _, want := range []string{"business rule failed", "connection lost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWithinTransactionRollsBackAndRepanics(t *testing.T) {
	tx := &fakeTx{}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	defer func() {
		p := recover()

		if p == nil {
			t.Fatal("panic was swallowed; must re-raise")
		}

		if p != "boom" {
			t.Fatalf("panic = %v, want boom", p)
		}

		if tx.rollbacks != 1 || tx.commits != 0 {
			t.Errorf("commits=%d rollbacks=%d, want 0/1", tx.commits, tx.rollbacks)
		}
	}()

	_ = uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		panic("boom")
	})
}

func TestWithinTransactionJoinsOpenTransaction(t *testing.T) {
	beginner := &fakeBeginner{tx: &fakeTx{}}
	uow := NewUnitOfWork(beginner)

	var inner, outer int

	err := uow.WithinTransaction(context.Background(), func(ctx context.Context, txUow store.UnitOfWork) error {
		outer++

		// A nested call on the transactional uow must join, not re-begin.
		return txUow.WithinTransaction(ctx, func(_ context.Context, _ store.UnitOfWork) error {
			inner++

			return nil
		})
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	if outer != 1 || inner != 1 {
		t.Errorf("outer=%d inner=%d, want 1/1", outer, inner)
	}

	if beginner.begins != 1 {
		t.Errorf("begins = %d, want exactly 1 (nested call must not re-begin)", beginner.begins)
	}
	// One commit for the outer transaction only.
	if beginner.tx.commits != 1 {
		t.Errorf("commits = %d, want 1", beginner.tx.commits)
	}
}

func TestWithinTransactionRollsBackOnCanceledContext(t *testing.T) {
	// When fn fails because the request context was canceled, the rollback must
	// still go out on a live context — otherwise pgx cannot send ROLLBACK and
	// destroys the pooled connection.
	tx := &fakeTx{}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	ctx, cancel := context.WithCancel(context.Background())

	err := uow.WithinTransaction(ctx, func(ctx context.Context, _ store.UnitOfWork) error {
		cancel()

		return ctx.Err()
	})
	if err == nil {
		t.Fatal("want the ctx error")
	}

	if tx.rollbacks != 1 {
		t.Fatalf("rollbacks = %d, want 1", tx.rollbacks)
	}

	if tx.rollbackCtxErr != nil {
		t.Fatalf("rollback saw a dead context (%v); must use a detached one", tx.rollbackCtxErr)
	}
}

func TestWithinTransactionBeginFailure(t *testing.T) {
	beginErr := stderrors.New("pool exhausted")

	uow := NewUnitOfWork(&fakeBeginner{beginErr: beginErr})

	err := uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		t.Fatal("fn must not run when Begin fails")

		return nil
	})

	if !stderrors.Is(err, beginErr) {
		t.Fatalf("err = %v, want wrapped begin error", err)
	}
}

func TestWithinTransactionCommitFailure(t *testing.T) {
	tx := &fakeTx{commitErr: stderrors.New("serialization failure")}
	uow := NewUnitOfWork(&fakeBeginner{tx: tx})

	err := uow.WithinTransaction(context.Background(), func(_ context.Context, _ store.UnitOfWork) error {
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "serialization failure") {
		t.Fatalf("err = %v, want commit failure surfaced", err)
	}
}
