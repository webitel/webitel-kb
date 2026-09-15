package grpc

import (
	"context"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

// RetrievalServer handles the Retrieval gRPC service.
type RetrievalServer struct {
	kb.UnimplementedRetrievalServer

	service *service.RetrievalService
}

func NewRetrievalServer(service *service.RetrievalService) *RetrievalServer {
	return &RetrievalServer{service: service}
}

func (s *RetrievalServer) Search(ctx context.Context, req *kb.SearchRequest) (*kb.SearchResponse, error) {
	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithSearchTerm(req.GetQuery()),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.Search(ctx, opts, model.SearchFilter{
		SpaceIDs:     req.GetSpaceIds(),
		Tags:         req.GetTags(),
		TagsMatchAll: req.GetTagMatch() == kb.TagMatch_TAG_MATCH_ALL,
	})
	if err != nil {
		return nil, err
	}

	return &kb.SearchResponse{Items: summariesToProto(items), Next: next}, nil
}

func (s *RetrievalServer) Resolve(ctx context.Context, req *kb.ResolveRequest) (*kb.ResolveResponse, error) {
	// The batch is the id list itself, never a page.
	opts, err := options.NewSearchOptions(ctx,
		options.WithUnlimitedSize(),
		options.WithIDs(req.GetIds()),
	)
	if err != nil {
		return nil, err
	}

	items, err := s.service.Resolve(ctx, opts, req.GetSpaceIds())
	if err != nil {
		return nil, err
	}

	return &kb.ResolveResponse{Items: summariesToProto(items)}, nil
}

func (s *RetrievalServer) Menu(ctx context.Context, req *kb.MenuRequest) (*kb.MenuResponse, error) {
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	items, err := s.service.Menu(ctx, opts, req.GetSpaceId(), req.GetParentId(), req.GetDepthLimit())
	if err != nil {
		return nil, err
	}

	return &kb.MenuResponse{Items: summariesToProto(items)}, nil
}

func (s *RetrievalServer) SemanticSearch(ctx context.Context, req *kb.SemanticSearchRequest) (*kb.SemanticSearchResponse, error) {
	// The result size is the top k itself, never a page.
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	hits, citations, err := s.service.SemanticSearch(ctx, opts, model.SemanticQuery{
		Query:            req.GetQuery(),
		SpaceIDs:         req.GetSpaceIds(),
		Tags:             req.GetTags(),
		TagsMatchAll:     req.GetTagMatch() == kb.TagMatch_TAG_MATCH_ALL,
		TopK:             int(req.GetTopK()),
		IncludeCitations: req.GetIncludeCitations(),
	})
	if err != nil {
		return nil, err
	}

	return &kb.SemanticSearchResponse{Chunks: chunksToProto(hits), Citations: citationsToProto(citations)}, nil
}

func (s *RetrievalServer) Suggest(ctx context.Context, req *kb.SuggestRequest) (*kb.SuggestResponse, error) {
	// The result size is fixed, never a page.
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	answer, err := s.service.Suggest(ctx, opts, model.SuggestQuery{
		Message:      req.GetCustomerMessage(),
		SpaceIDs:     req.GetSpaceIds(),
		TeamID:       req.GetTeamId(),
		ReturnChunks: req.GetReturnChunks(),
	})
	if err != nil {
		return nil, err
	}

	return &kb.SuggestResponse{Articles: summariesToProto(answer.Articles), Chunks: chunksToProto(answer.Chunks)}, nil
}

func chunksToProto(items []*model.ChunkHit) []*kb.Chunk {
	out := make([]*kb.Chunk, 0, len(items))
	for _, item := range items {
		out = append(out, &kb.Chunk{
			ArticleId:  item.ArticleID,
			VersionId:  item.VersionID,
			ChunkIndex: item.ChunkIndex,
			Content:    item.Content,
			Score:      item.Score,
		})
	}

	return out
}

func citationsToProto(items []*model.Citation) []*kb.Citation {
	out := make([]*kb.Citation, 0, len(items))
	for _, item := range items {
		out = append(out, &kb.Citation{
			ArticleId: item.ArticleID,
			Title:     item.Title,
			Snippet:   item.Snippet,
			Url:       item.URL,
		})
	}

	return out
}

func summaryToProto(in *model.ArticleSummary) *kb.ArticleSummary {
	return &kb.ArticleSummary{
		Id:        in.ID,
		SpaceId:   in.SpaceID,
		ParentId:  in.ParentID,
		Depth:     in.Depth,
		Type:      kb.ArticleType(in.Type),
		Subject:   in.Subject,
		Snippet:   in.Snippet,
		Tags:      in.Tags,
		State:     kb.ArticleState(in.State),
		Body:      in.Body,
		CreatedAt: unixMilli(in.CreatedAt),
		UpdatedAt: unixMilli(in.UpdatedAt),
	}
}

func summariesToProto(items []*model.ArticleSummary) []*kb.ArticleSummary {
	out := make([]*kb.ArticleSummary, 0, len(items))
	for _, item := range items {
		out = append(out, summaryToProto(item))
	}

	return out
}
