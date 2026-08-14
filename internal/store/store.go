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

	// ArticleStore accesses knowledge-base articles.
	ArticleStore() ArticleStore

	// ArticleVersionStore accesses the version history of articles.
	ArticleVersionStore() ArticleVersionStore
}

// ArticleStore persists knowledge-base articles. Every read and write is
// scoped to the caller's domain through the owning space; reads never see
// soft-deleted rows. Multi-statement flows are grouped by the service in one
// transaction.
type ArticleStore interface {
	// List returns a page of articles and whether a next page exists.
	List(ctx context.Context, opts options.Searcher, filter model.ArticleFilter) ([]*model.Article, bool, error)

	// Locate returns the single article the options identify by id.
	Locate(ctx context.Context, opts options.Searcher) (*model.Article, error)

	// Create inserts an article into its space, deriving depth from the parent.
	// Unset type and state take their default code.
	Create(ctx context.Context, opts options.Creator, in *model.Article) (*model.Article, error)

	// Update rewrites the writable fields of the article opts identify and
	// bumps its version. The write is guarded by the optimistic-lock version:
	// a mismatch fails with a version conflict.
	Update(ctx context.Context, opts options.Updator, in *model.Article, expectedVer int32) (*model.Article, error)

	// Delete soft-deletes the article opts identify together with its subtree
	// and returns the root's last state.
	Delete(ctx context.Context, opts options.Deleter, expectedVer int32) (*model.Article, error)

	// LocateForUpdate is Locate with the row locked until the transaction
	// ends: the read a read-then-write flow bases its decisions on.
	LocateForUpdate(ctx context.Context, opts options.Searcher) (*model.Article, error)

	// Move reparents the article opts identify, shifting the depth of its
	// whole subtree. A zero newParentID moves it to the top level. The write
	// rejects a cycle, a foreign parent and a subtree that would outgrow the
	// maximum depth, and is guarded by the optimistic-lock version.
	Move(ctx context.Context, opts options.Updator, newParentID int64, expectedVer int32) (*model.Article, error)

	// Ancestors returns the ancestor chain of an article, root first; the
	// article itself is not part of it.
	Ancestors(ctx context.Context, opts options.Searcher, articleID int64) ([]*model.Article, error)

	// Tree returns the hierarchy of a space as its root nodes.
	Tree(ctx context.Context, opts options.Searcher, spaceID int64) ([]*model.TreeNode, error)

	// Subtree returns an article and everything below it with their depth, so
	// a caller can validate a move before attempting it.
	Subtree(ctx context.Context, opts options.Searcher, articleID int64) ([]model.SubtreeNode, error)

	// SuggestTags returns the distinct tags of a space matching a prefix.
	SuggestTags(ctx context.Context, opts options.Searcher, spaceID int64, prefix string, size int) ([]string, error)
}

// ArticleVersionStore persists the immutable version history of articles.
// Reads and writes are scoped to the caller's domain through the owning
// article and its space.
type ArticleVersionStore interface {
	// List returns a page of versions of an article, newest first, and
	// whether a next page exists.
	List(ctx context.Context, opts options.Searcher, articleID int64) ([]*model.ArticleVersion, bool, error)

	// Locate returns a single version of an article by its number.
	Locate(ctx context.Context, opts options.Searcher, articleID int64, number int32) (*model.ArticleVersion, error)

	// Create appends a version to an article, numbering it after the current
	// last one and building the search vector with the given text search
	// configuration.
	Create(ctx context.Context, opts options.Creator, in *model.ArticleVersion, textSearchConfig string) (*model.ArticleVersion, error)
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
