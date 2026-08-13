package grpc

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

// EmbeddingModelsServer handles the EmbeddingModels gRPC service: it builds
// request options, delegates to the model service and maps models to the
// contract. The API key exists only in the input message; no response carries
// it back.
type EmbeddingModelsServer struct {
	kb.UnimplementedEmbeddingModelsServer

	service *service.EmbeddingModelService
}

func NewEmbeddingModelsServer(service *service.EmbeddingModelService) *EmbeddingModelsServer {
	return &EmbeddingModelsServer{service: service}
}

func (s *EmbeddingModelsServer) ListModels(ctx context.Context, req *kb.ListModelsRequest) (*kb.EmbeddingModelList, error) {
	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithFields(req),
		options.WithSort(req),
		options.WithSearch(req),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.List(ctx, opts, model.EmbeddingModelFilter{Type: req.GetType()})
	if err != nil {
		return nil, err
	}

	models := make([]*kb.EmbeddingModel, 0, len(items))
	for _, item := range items {
		models = append(models, modelToProto(item))
	}

	return &kb.EmbeddingModelList{Items: models, Next: next}, nil
}

func (s *EmbeddingModelsServer) LocateModel(ctx context.Context, req *kb.LocateModelRequest) (*kb.EmbeddingModel, error) {
	opts, err := options.NewLocateOptions(ctx, options.WithID(req.GetId()))
	if err != nil {
		return nil, err
	}

	item, err := s.service.Locate(ctx, opts)
	if err != nil {
		return nil, err
	}

	return modelToProto(item), nil
}

func (s *EmbeddingModelsServer) CreateModel(ctx context.Context, req *kb.CreateModelRequest) (*kb.EmbeddingModel, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errModelInputRequired
	}

	opts, err := options.NewCreateOptions(ctx)
	if err != nil {
		return nil, err
	}

	created, err := s.service.Create(ctx, opts, modelFromInput(in), in.GetApiKey())
	if err != nil {
		return nil, err
	}

	return modelToProto(created), nil
}

func (s *EmbeddingModelsServer) UpdateModel(ctx context.Context, req *kb.UpdateModelRequest) (*kb.EmbeddingModel, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errModelInputRequired
	}

	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(req.GetId()))
	if err != nil {
		return nil, err
	}

	updated, err := s.service.Update(ctx, opts, modelFromInput(in), in.GetApiKey())
	if err != nil {
		return nil, err
	}

	return modelToProto(updated), nil
}

func (s *EmbeddingModelsServer) DeleteModel(ctx context.Context, req *kb.DeleteModelRequest) (*kb.EmbeddingModel, error) {
	opts, err := options.NewDeleteOptions(ctx, options.WithDeleteID(req.GetId()))
	if err != nil {
		return nil, err
	}

	deleted, err := s.service.Delete(ctx, opts)
	if err != nil {
		return nil, err
	}

	return modelToProto(deleted), nil
}

func (s *EmbeddingModelsServer) ValidateModel(ctx context.Context, req *kb.ValidateModelRequest) (*kb.EmbeddingModel, error) {
	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(req.GetId()))
	if err != nil {
		return nil, err
	}

	validated, err := s.service.Validate(ctx, opts)
	if err != nil {
		return nil, err
	}

	return modelToProto(validated), nil
}

func modelFromInput(in *kb.InputEmbeddingModel) *model.EmbeddingModel {
	return &model.EmbeddingModel{
		Type:         in.GetType(),
		Name:         in.GetName(),
		Provider:     in.GetProvider(),
		IsSelfHosted: in.GetIsSelfHosted(),
		ModelRef:     in.GetModelRef(),
		Dimensions:   in.GetDimensions(),
		Endpoint:     in.GetEndpoint(),
	}
}

func modelToProto(m *model.EmbeddingModel) *kb.EmbeddingModel {
	return &kb.EmbeddingModel{
		Id:           m.ID,
		DomainId:     m.DomainID,
		Type:         m.Type,
		Name:         m.Name,
		Provider:     m.Provider,
		IsSelfHosted: m.IsSelfHosted,
		ModelRef:     m.ModelRef,
		Dimensions:   m.Dimensions,
		Endpoint:     m.Endpoint,
		ValidatedAt:  unixMilli(m.ValidatedAt),
		CreatedAt:    unixMilli(m.CreatedAt),
		CreatedBy:    lookupToProto(m.CreatedBy),
	}
}

var errModelInputRequired = errors.InvalidArgument(
	"input is required",
	errors.WithID("kb.model.input_required"),
)
