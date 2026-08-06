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

	// SpaceStore accesses knowledge-base spaces.
	SpaceStore() SpaceStore
}

// SpaceStore persists knowledge-base spaces and their team binding. Every read
// and write is scoped to the caller's domain.
type SpaceStore interface {
	// List returns a page of spaces and whether a next page exists.
	List(ctx context.Context, opts options.Searcher) ([]*model.Space, bool, error)

	// Locate returns the single space the options identify by id.
	Locate(ctx context.Context, opts options.Searcher) (*model.Space, error)

	// LocateForUpdate is Locate with the row locked until the transaction
	// ends: the read a read-then-write flow bases its decisions on.
	LocateForUpdate(ctx context.Context, opts options.Searcher) (*model.Space, error)

	// Create inserts a space owned by the caller's domain. The team binding is
	// written separately via ReplaceTeams.
	Create(ctx context.Context, opts options.Creator, in *model.Space) (*model.Space, error)

	// Update rewrites the writable fields of the space opts identify. The
	// immutable columns (language, migration target) are not part of the
	// statement at all.
	Update(ctx context.Context, opts options.Updator, in *model.Space) (*model.Space, error)

	// Delete removes the space opts identify and returns its last state. The
	// team binding goes with it; any remaining article blocks the delete.
	Delete(ctx context.Context, opts options.Deleter) (*model.Space, error)

	// ReplaceTeams rewrites the team binding of a domain's space to exactly the
	// given set; an empty set removes the binding.
	ReplaceTeams(ctx context.Context, spaceID, domainID, userID int64, teamIDs []int64) error

	// HasArticles reports whether any article still references a domain's
	// space — the same condition the schema enforces on delete, checked here
	// so the caller can fail with a domain error instead of a raw constraint.
	HasArticles(ctx context.Context, spaceID, domainID int64) (bool, error)
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
