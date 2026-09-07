package service

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/store"
)

// IndexingService serves the indexer worker: the embedding model of a space
// and its opened credential.
type IndexingService struct {
	uow store.UnitOfWork
	enc crypto.Encryptor
}

// NewIndexingService builds the service.
func NewIndexingService(uow store.UnitOfWork, encryptor crypto.Encryptor) *IndexingService {
	return &IndexingService{uow: uow, enc: encryptor}
}

// ResolveSpaceEmbedding returns what the worker needs to embed a space. A space
// without vector search is answered, not refused: its articles are still indexed.
func (s *IndexingService) ResolveSpaceEmbedding(ctx context.Context, spaceID int64) (*model.SpaceEmbedding, error) {
	if spaceID <= 0 {
		return nil, errors.InvalidArgument(
			"space id is required",
			errors.WithID("kb.space.id_required"),
		)
	}

	found, err := s.uow.SpaceStore().ResolveEmbedding(ctx, spaceID)
	if err != nil {
		return nil, err
	}

	if !found.VectorSearchEnabled {
		return &model.SpaceEmbedding{}, nil
	}

	if found.ModelID == 0 {
		return nil, errors.Aborted(
			"space has no embedding model",
			errors.WithID("kb.space.model_unset"),
		)
	}

	if _, cloud := cloudProviders[found.Provider]; cloud {
		// Say so here, rather than let the worker retry an authentication failure.
		if len(found.Config) == 0 {
			return nil, errors.Aborted(
				"model has no stored credential",
				errors.WithID("kb.model.credential_missing"),
			)
		}
	} else {
		// Self-hosted providers take no key.
		found.Config = nil
	}

	key, err := s.enc.Decrypt(ctx, found.Config)
	if err != nil {
		return nil, errors.Internal(
			"unable to open the model credential",
			errors.WithID("kb.model.credential"),
			errors.WithCause(err),
		)
	}

	found.Config = nil
	found.APIKey = string(key)

	return found, nil
}
