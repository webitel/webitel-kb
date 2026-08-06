package service

import (
	"context"
	"slices"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// SpaceService owns the space business rules.
type SpaceService struct {
	uow store.UnitOfWork
}

func NewSpaceService(uow store.UnitOfWork) *SpaceService {
	return &SpaceService{uow: uow}
}

func (s *SpaceService) List(ctx context.Context, opts options.Searcher) ([]*model.Space, bool, error) {
	return s.uow.SpaceStore().List(ctx, opts)
}

func (s *SpaceService) Locate(ctx context.Context, opts options.Searcher) (*model.Space, error) {
	return s.uow.SpaceStore().Locate(ctx, opts)
}

// Create validates the retrieval configuration and writes the space together
// with its team binding in one transaction.
func (s *SpaceService) Create(
	ctx context.Context, opts options.Creator, in *model.Space, teamIDs []int64,
) (*model.Space, error) {
	if in.Language == "" {
		return nil, errors.InvalidArgument(
			"language is required",
			errors.WithID("kb.space.language_required"),
		)
	}

	if err := requireName(in); err != nil {
		return nil, err
	}

	if err := validateRetrievalConfig(in); err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()

	var created *model.Space

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		if err := s.requireValidatedModels(ctx, tx, session, in.EmbeddingModelID, in.RerankerModelID); err != nil {
			return err
		}

		space, err := tx.SpaceStore().Create(ctx, opts, in)
		if err != nil {
			return err
		}

		if err := tx.SpaceStore().ReplaceTeams(
			ctx, space.ID, session.GetDomainID(), session.GetUserID(), dedupeIDs(teamIDs),
		); err != nil {
			return err
		}

		created, err = tx.SpaceStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{space.ID}, fields: opts.GetFields(),
		})

		return err
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// Update enforces the immutable fields against the stored space, validates a
// newly configured model, and rewrites the space with its team binding in one
// transaction.
func (s *SpaceService) Update(
	ctx context.Context, opts options.Updator, in *model.Space, teamIDs []int64,
) (*model.Space, error) {
	if err := requireName(in); err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()

	var updated *model.Space

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.SpaceStore().LocateForUpdate(ctx, readOptions{
			auth: session,
			ids:  []int64{opts.GetID()},
			fields: []string{
				"id", "language", "embedding_model_id", "reranker_model_id",
			},
		})
		if err != nil {
			return err
		}

		if in.Language != "" && in.Language != current.Language {
			return errors.InvalidArgument(
				"language is immutable",
				errors.WithID("kb.space.language_immutable"),
			)
		}

		// The embedding model fixes the vector dimension of everything already
		// indexed: it may be configured once, never changed or cleared. This
		// check runs before the consistency rules, so clearing the model of a
		// vector-enabled space reports the true cause.
		switch {
		case in.EmbeddingModelID == current.EmbeddingModelID:
			// Unchanged.
		case current.EmbeddingModelID == 0:
			// One-way upgrade from a lexical-only space; validated below.
		default:
			return errors.InvalidArgument(
				"embedding model is immutable",
				errors.WithID("kb.space.embedding_model_immutable"),
			)
		}

		if err := validateRetrievalConfig(in); err != nil {
			return err
		}

		embeddingToValidate := in.EmbeddingModelID
		if in.EmbeddingModelID == current.EmbeddingModelID {
			embeddingToValidate = 0 // no change, no gate
		}

		rerankerToValidate := in.RerankerModelID
		if in.RerankerModelID == current.RerankerModelID {
			rerankerToValidate = 0
		}

		if err := s.requireValidatedModels(ctx, tx, session, embeddingToValidate, rerankerToValidate); err != nil {
			return err
		}

		space, err := tx.SpaceStore().Update(ctx, opts, in)
		if err != nil {
			return err
		}

		if err := tx.SpaceStore().ReplaceTeams(
			ctx, space.ID, session.GetDomainID(), session.GetUserID(), dedupeIDs(teamIDs),
		); err != nil {
			return err
		}

		updated, err = tx.SpaceStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{space.ID}, fields: opts.GetFields(),
		})

		return err
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// Delete removes a space that no article references anymore: the check
// mirrors the schema constraint, so the caller gets a domain error instead of
// a raw FK violation. The team binding goes with the space.
func (s *SpaceService) Delete(ctx context.Context, opts options.Deleter) (*model.Space, error) {
	session := opts.GetAuthOpts()

	var deleted *model.Space

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		hasArticles, err := tx.SpaceStore().HasArticles(ctx, opts.GetID(), session.GetDomainID())
		if err != nil {
			return err
		}

		if hasArticles {
			return errors.Aborted(
				"space still holds articles; archive or move them first",
				errors.WithID("kb.space.articles_exist"),
			)
		}

		deleted, err = tx.SpaceStore().Delete(ctx, opts)

		return err
	})
	if err != nil {
		return nil, err
	}

	return deleted, nil
}

func requireName(in *model.Space) error {
	if in.Name == "" {
		return errors.InvalidArgument(
			"name is required",
			errors.WithID("kb.space.name_required"),
		)
	}

	return nil
}

// validateRetrievalConfig checks that the enabled retrieval features have
// their models configured.
func validateRetrievalConfig(in *model.Space) error {
	if in.VectorSearchEnabled && in.EmbeddingModelID == 0 {
		return errors.InvalidArgument(
			"vector search requires an embedding model",
			errors.WithID("kb.space.embedding_model_required"),
		)
	}

	if in.RerankEnabled && in.RerankerModelID == 0 {
		return errors.InvalidArgument(
			"reranking requires a reranker model",
			errors.WithID("kb.space.reranker_model_required"),
		)
	}

	return nil
}

// requireValidatedModels gates the model references a space may take: each
// non-zero id must be visible to the domain, of the right type, and validated.
func (s *SpaceService) requireValidatedModels(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, embeddingID, rerankerID int64,
) error {
	if embeddingID != 0 {
		if err := requireValidatedModel(ctx, tx, session, embeddingID, model.ModelTypeEmbedding); err != nil {
			return err
		}
	}

	if rerankerID != 0 {
		if err := requireValidatedModel(ctx, tx, session, rerankerID, model.ModelTypeReranker); err != nil {
			return err
		}
	}

	return nil
}

func requireValidatedModel(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, id int64, wantType string,
) error {
	found, err := tx.EmbeddingModelStore().Locate(ctx, readOptions{
		auth:   session,
		ids:    []int64{id},
		fields: []string{"id", "type", "validated_at"},
	})
	if err != nil {
		return err
	}

	if found.Type != wantType {
		return errors.InvalidArgument(
			"model type does not match its role",
			errors.WithID("kb.space.model_type"),
			errors.WithValue("expected", wantType),
		)
	}

	if found.ValidatedAt.IsZero() {
		return errors.InvalidArgument(
			"model is not validated",
			errors.WithID("kb.space.model_not_validated"),
		)
	}

	return nil
}

func dedupeIDs(ids []int64) []int64 {
	if len(ids) < 2 {
		return ids
	}

	out := slices.Clone(ids)
	slices.Sort(out)

	return slices.Compact(out)
}

// readOptions is the service-internal options.Searcher carrier for in-flow
// reads (the validated-model gate, transactional read-backs).
type readOptions struct {
	auth   auth.Auther
	ids    []int64
	fields []string
}

var _ options.Searcher = readOptions{}

func (o readOptions) GetAuthOpts() auth.Auther { return o.auth }
func (o readOptions) GetFields() []string      { return o.fields }
func (o readOptions) GetSearch() string        { return "" }
func (o readOptions) GetPage() int             { return 1 }
func (o readOptions) GetSize() int             { return 1 }
func (o readOptions) GetSort() string          { return "" }
func (o readOptions) GetIDs() []int64          { return o.ids }
