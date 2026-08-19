package grpc

import (
	"context"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/service"
)

// TagsServer handles the Tags gRPC service: tag completion within a space.
type TagsServer struct {
	kb.UnimplementedTagsServer

	service *service.ArticleService
}

func NewTagsServer(service *service.ArticleService) *TagsServer {
	return &TagsServer{service: service}
}

func (s *TagsServer) SuggestTags(ctx context.Context, req *kb.SuggestTagsRequest) (*kb.SuggestTagsResponse, error) {
	opts, err := options.NewSearchOptions(ctx)
	if err != nil {
		return nil, err
	}

	tags, err := s.service.SuggestTags(ctx, opts, req.GetSpaceId(), req.GetQ(), int(req.GetSize()))
	if err != nil {
		return nil, err
	}

	return &kb.SuggestTagsResponse{Tags: tags}, nil
}
