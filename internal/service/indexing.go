package service

import (
	"context"

	"google.golang.org/grpc/codes"

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
		return nil, errors.New(
			"space has no embedding model",
			errors.WithCode(codes.FailedPrecondition),
			errors.WithID("kb.space.model_unset"),
		)
	}

	key, err := openModelCredential(ctx, s.enc, found.Provider, found.Config)
	if err != nil {
		return nil, err
	}

	found.Config = nil
	found.APIKey = key

	return found, nil
}
