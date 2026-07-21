// Package store declares the storage-layer contracts the rest of the service
// depends on;
package store

import (
	"context"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
)

// UnitOfWork runs storage work, optionally grouping it into one transaction.
// Entity store accessors are added here as the stores appear.
type UnitOfWork interface {
	// WithinTransaction executes fn within a single database transaction. The
	// uow passed to fn runs every operation on that transaction. A nil return
	// commits; an error or panic rolls back (the panic is re-raised). Calling
	// WithinTransaction on a uow already inside a transaction joins it rather
	// than nesting.
	WithinTransaction(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error

	// EmbeddingModelStore accesses the embedding model registry.
	EmbeddingModelStore() EmbeddingModelStore
}

// EmbeddingModelStore persists the embedding/reranker model registry. Reads see
// the caller's domain and global models; writes are restricted to the caller's
// domain, so global models stay read-only. The provider credential (config) is
// write-only: no read model ever carries it.
type EmbeddingModelStore interface {
	// List returns a page of models and whether a next page exists.
	List(ctx context.Context, opts options.Searcher, filter model.EmbeddingModelFilter) ([]*model.EmbeddingModel, bool, error)

	// Locate returns the single model the options identify by id.
	Locate(ctx context.Context, opts options.Searcher) (*model.EmbeddingModel, error)

	// Create registers a model owned by the caller's domain. config is the
	// encrypted provider credential; nil stores NULL.
	Create(ctx context.Context, opts options.Creator, in *model.EmbeddingModel, config []byte) (*model.EmbeddingModel, error)

	// Update rewrites the writable fields of the model opts identify and resets
	// validated_at: a changed registration must pass validation again. With
	// keepConfig the stored credential is left untouched; otherwise config
	// replaces it.
	Update(ctx context.Context, opts options.Updator, in *model.EmbeddingModel, config []byte, keepConfig bool) (*model.EmbeddingModel, error)

	// Delete removes the model opts identify and returns its last state.
	Delete(ctx context.Context, opts options.Deleter) (*model.EmbeddingModel, error)

	// MarkValidated stamps a successful validation on the model opts identify.
	MarkValidated(ctx context.Context, opts options.Updator) (*model.EmbeddingModel, error)

	// GetConfig returns the stored encrypted credential of a model readable by
	// the domain; nil when the model has none.
	GetConfig(ctx context.Context, id, domainID int64) ([]byte, error)
}
