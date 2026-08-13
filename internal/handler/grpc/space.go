package grpc

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

// SpacesServer handles the Spaces gRPC service: it builds request options,
// delegates to the space service and maps models to the contract.
type SpacesServer struct {
	kb.UnimplementedSpacesServer

	service *service.SpaceService
}

func NewSpacesServer(service *service.SpaceService) *SpacesServer {
	return &SpacesServer{service: service}
}

func (s *SpacesServer) ListSpaces(ctx context.Context, req *kb.ListSpacesRequest) (*kb.SpaceList, error) {
	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithFields(req),
		options.WithSort(req),
		options.WithSearch(req),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.List(ctx, opts)
	if err != nil {
		return nil, err
	}

	spaces := make([]*kb.Space, 0, len(items))
	for _, item := range items {
		spaces = append(spaces, spaceToProto(item))
	}

	return &kb.SpaceList{Items: spaces, Next: next}, nil
}

func (s *SpacesServer) LocateSpace(ctx context.Context, req *kb.LocateSpaceRequest) (*kb.Space, error) {
	opts, err := options.NewLocateOptions(ctx, options.WithID(req.GetId()))
	if err != nil {
		return nil, err
	}

	item, err := s.service.Locate(ctx, opts)
	if err != nil {
		return nil, err
	}

	return spaceToProto(item), nil
}

func (s *SpacesServer) CreateSpace(ctx context.Context, req *kb.CreateSpaceRequest) (*kb.Space, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errSpaceInputRequired
	}

	opts, err := options.NewCreateOptions(ctx)
	if err != nil {
		return nil, err
	}

	created, err := s.service.Create(ctx, opts, spaceFromInput(in), in.GetTeamIds())
	if err != nil {
		return nil, err
	}

	return spaceToProto(created), nil
}

func (s *SpacesServer) UpdateSpace(ctx context.Context, req *kb.UpdateSpaceRequest) (*kb.Space, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errSpaceInputRequired
	}

	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(req.GetId()))
	if err != nil {
		return nil, err
	}

	updated, err := s.service.Update(ctx, opts, spaceFromInput(in), in.GetTeamIds())
	if err != nil {
		return nil, err
	}

	return spaceToProto(updated), nil
}

func (s *SpacesServer) DeleteSpace(ctx context.Context, req *kb.DeleteSpaceRequest) (*kb.Space, error) {
	opts, err := options.NewDeleteOptions(ctx, options.WithDeleteID(req.GetId()))
	if err != nil {
		return nil, err
	}

	deleted, err := s.service.Delete(ctx, opts)
	if err != nil {
		return nil, err
	}

	return spaceToProto(deleted), nil
}

var errSpaceInputRequired = errors.InvalidArgument(
	"input is required",
	errors.WithID("kb.space.input_required"),
)

func spaceFromInput(in *kb.InputSpace) *model.Space {
	return &model.Space{
		Name:                in.GetName(),
		Description:         in.GetDescription(),
		Language:            in.GetLanguage(),
		EmbeddingModelID:    in.GetEmbeddingModelId(),
		RerankerModelID:     in.GetRerankerModelId(),
		VectorSearchEnabled: in.GetVectorSearchEnabled(),
		RerankEnabled:       in.GetRerankEnabled(),
		ChunkingStrategy:    in.GetChunkingStrategy(),
		HomeArticleID:       in.GetHomeArticleId(),
	}
}

func spaceToProto(m *model.Space) *kb.Space {
	return &kb.Space{
		Id:                     m.ID,
		DomainId:               m.DomainID,
		Name:                   m.Name,
		Description:            m.Description,
		Language:               m.Language,
		EmbeddingModelId:       m.EmbeddingModelID,
		TargetEmbeddingModelId: m.TargetEmbeddingModelID,
		RerankerModelId:        m.RerankerModelID,
		VectorSearchEnabled:    m.VectorSearchEnabled,
		RerankEnabled:          m.RerankEnabled,
		ChunkingStrategy:       m.ChunkingStrategy,
		HomeArticleId:          m.HomeArticleID,
		Teams:                  lookupsToProto(m.Teams),
		CreatedAt:              unixMilli(m.CreatedAt),
		UpdatedAt:              unixMilli(m.UpdatedAt),
		CreatedBy:              lookupToProto(m.CreatedBy),
		UpdatedBy:              lookupToProto(m.UpdatedBy),
	}
}
