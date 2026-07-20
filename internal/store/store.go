// Package store declares the storage-layer contracts the rest of the service
// depends on;
package store

import "context"

// UnitOfWork runs storage work, optionally grouping it into one transaction.
// Entity store accessors are added here as the stores appear.
type UnitOfWork interface {
	// WithinTransaction executes fn within a single database transaction. The
	// uow passed to fn runs every operation on that transaction. A nil return
	// commits; an error or panic rolls back (the panic is re-raised). Calling
	// WithinTransaction on a uow already inside a transaction joins it rather
	// than nesting.
	WithinTransaction(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error
}
