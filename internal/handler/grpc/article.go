package grpc

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/etag"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

var errArticleInputRequired = errors.InvalidArgument(
	"an article input is required",
	errors.WithID("kb.article.input_required"),
)

var errArticleIDRequired = errors.InvalidArgument(
	"an article id is required",
	errors.WithID("kb.article.id_required"),
)

// ArticlesServer handles the Articles gRPC service: it decodes etags, builds
// request options, delegates to the article service and maps models to the
// contract.
type ArticlesServer struct {
	kb.UnimplementedArticlesServer

	service *service.ArticleService
}

func NewArticlesServer(service *service.ArticleService) *ArticlesServer {
	return &ArticlesServer{service: service}
}

func (s *ArticlesServer) ListArticles(ctx context.Context, req *kb.ListArticlesRequest) (*kb.ArticleList, error) {
	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithFields(req),
		options.WithSort(req),
		options.WithSearch(req),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.List(ctx, opts, model.ArticleFilter{
		SpaceID: req.GetSpaceId(),
		Type:    int32(req.GetType()),
		State:   int32(req.GetState()),
		Tags:    req.GetTags(),
	})
	if err != nil {
		return nil, err
	}

	return articleListToProto(items, next)
}

func (s *ArticlesServer) LocateArticle(ctx context.Context, req *kb.LocateArticleRequest) (*kb.Article, error) {
	// Reads accept a bare id as well, which keeps manual REST calls usable.
	id, err := etag.ParseLocator(etag.TypeArticle, req.GetEtag())
	if err != nil {
		return nil, err
	}

	opts, err := options.NewLocateOptions(ctx, options.WithID(id))
	if err != nil {
		return nil, err
	}

	item, err := s.service.Locate(ctx, opts)
	if err != nil {
		return nil, err
	}

	return articleToProto(item)
}

func (s *ArticlesServer) CreateArticle(ctx context.Context, req *kb.CreateArticleRequest) (*kb.Article, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errArticleInputRequired
	}

	body, err := bodyFromInput(in)
	if err != nil {
		return nil, err
	}

	opts, err := options.NewCreateOptions(ctx)
	if err != nil {
		return nil, err
	}

	created, err := s.service.Create(ctx, opts, articleFromInput(in), body)
	if err != nil {
		return nil, err
	}

	return articleToProto(created)
}

func (s *ArticlesServer) UpdateArticle(ctx context.Context, req *kb.UpdateArticleRequest) (*kb.Article, error) {
	in := req.GetInput()
	if in == nil {
		return nil, errArticleInputRequired
	}

	// Mutations require a full etag: the version it carries is the guard.
	id, ver, err := etag.Parse(etag.TypeArticle, req.GetEtag())
	if err != nil {
		return nil, err
	}

	body, err := bodyFromInput(in)
	if err != nil {
		return nil, err
	}

	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(id))
	if err != nil {
		return nil, err
	}

	updated, err := s.service.Update(ctx, opts, articleFromInput(in), body, ver)
	if err != nil {
		return nil, err
	}

	return articleToProto(updated)
}

func (s *ArticlesServer) DeleteArticle(ctx context.Context, req *kb.DeleteArticleRequest) (*kb.Article, error) {
	id, ver, err := etag.Parse(etag.TypeArticle, req.GetEtag())
	if err != nil {
		return nil, err
	}

	opts, err := options.NewDeleteOptions(ctx, options.WithDeleteID(id))
	if err != nil {
		return nil, err
	}

	deleted, err := s.service.Delete(ctx, opts, ver)
	if err != nil {
		return nil, err
	}

	return articleToProto(deleted)
}

func (s *ArticlesServer) MoveArticle(ctx context.Context, req *kb.MoveArticleRequest) (*kb.Article, error) {
	id, ver, err := etag.Parse(etag.TypeArticle, req.GetEtag())
	if err != nil {
		return nil, err
	}

	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(id))
	if err != nil {
		return nil, err
	}

	moved, err := s.service.Move(ctx, opts, req.GetNewParentId(), ver)
	if err != nil {
		return nil, err
	}

	return articleToProto(moved)
}

func (s *ArticlesServer) ListChildren(ctx context.Context, req *kb.ListChildrenRequest) (*kb.ArticleList, error) {
	// Without a parent the unpaged listing would fan out to every root of the
	// domain, so a missing id is a caller mistake.
	if req.GetId() <= 0 {
		return nil, errArticleIDRequired
	}

	// The contract carries no paging here, and a parent may hold more children
	// than a default page.
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	parentID := req.GetId()

	items, next, err := s.service.List(ctx, opts, model.ArticleFilter{ParentID: &parentID})
	if err != nil {
		return nil, err
	}

	return articleListToProto(items, next)
}

func (s *ArticlesServer) ListAncestors(ctx context.Context, req *kb.ListAncestorsRequest) (*kb.ArticleList, error) {
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	items, err := s.service.Ancestors(ctx, opts, req.GetId())
	if err != nil {
		return nil, err
	}

	return articleListToProto(items, false)
}

func (s *ArticlesServer) GetTree(ctx context.Context, req *kb.GetTreeRequest) (*kb.GetTreeResponse, error) {
	opts, err := options.NewSearchOptions(ctx, options.WithUnlimitedSize())
	if err != nil {
		return nil, err
	}

	nodes, err := s.service.Tree(ctx, opts, req.GetSpaceId())
	if err != nil {
		return nil, err
	}

	return &kb.GetTreeResponse{Nodes: treeToProto(nodes)}, nil
}
