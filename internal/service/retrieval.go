package service

import (
	"context"
	"strings"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// defaultMenuDepth is the level a menu returns when the caller asks for none.
const defaultMenuDepth int32 = 1

// RetrievalService owns the read side operators and bots consume.
type RetrievalService struct {
	uow store.UnitOfWork
}

func NewRetrievalService(uow store.UnitOfWork) *RetrievalService {
	return &RetrievalService{uow: uow}
}

// Search runs full-text search over subjects and published bodies.
func (s *RetrievalService) Search(
	ctx context.Context, opts options.Searcher, filter model.SearchFilter,
) ([]*model.ArticleSummary, bool, error) {
	// A blank query matches nothing by construction.
	if strings.TrimSpace(opts.GetSearch()) == "" {
		return nil, false, nil
	}

	return s.uow.RetrievalStore().Search(ctx, opts, filter)
}

// Resolve returns the summaries of the requested articles, in order.
func (s *RetrievalService) Resolve(
	ctx context.Context, opts options.Searcher, spaceIDs []int64,
) ([]*model.ArticleSummary, error) {
	ids := opts.GetIDs()
	if len(ids) == 0 {
		return nil, nil
	}

	if len(ids) > options.MaxSearchSize {
		return nil, errors.InvalidArgument(
			"too many ids requested at once",
			errors.WithID("kb.retrieval.ids_limit"),
		)
	}

	return s.uow.RetrievalStore().Resolve(ctx, opts, spaceIDs)
}

// Menu returns the articles below a parent.
func (s *RetrievalService) Menu(
	ctx context.Context, opts options.Searcher, spaceID, parentID int64, depthLimit int32,
) ([]*model.ArticleSummary, error) {
	if spaceID <= 0 {
		return nil, errors.InvalidArgument(
			"a space is required",
			errors.WithID("kb.retrieval.space_required"),
		)
	}

	return s.uow.RetrievalStore().Menu(ctx, opts, spaceID, parentID, menuDepth(depthLimit))
}

// menuDepth resolves the requested depth.
func menuDepth(depthLimit int32) int32 {
	switch {
	case depthLimit <= 0:
		return defaultMenuDepth
	case depthLimit > model.MaxArticleDepth:
		return model.MaxArticleDepth
	default:
		return depthLimit
	}
}
