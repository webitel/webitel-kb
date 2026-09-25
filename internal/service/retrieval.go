package service

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

// defaultMenuDepth is the level a menu returns when the caller asks for none.
const defaultMenuDepth int32 = 1

// Semantic search limits.
const (
	// defaultTopK is how many chunks a caller gets without asking.
	defaultTopK = 10
	// maxTopK is the branch depth: nothing deeper exists to fuse.
	maxTopK = queryobject.BranchDepth
	// embedTimeout bounds the query embedding call, retries included.
	embedTimeout = 5 * time.Second
)

// RetrievalService owns the read side operators and bots consume.
type RetrievalService struct {
	uow       store.UnitOfWork
	enc       crypto.Encryptor
	providers ProviderResolver
	log       *slog.Logger
}

func NewRetrievalService(
	uow store.UnitOfWork, encryptor crypto.Encryptor, providers ProviderResolver, log *slog.Logger,
) *RetrievalService {
	return &RetrievalService{uow: uow, enc: encryptor, providers: providers, log: log}
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

	return s.uow.RetrievalStore().Resolve(ctx, opts, ids, spaceIDs)
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

// SemanticSearch fuses the vector and the full-text ranking of chunks.
func (s *RetrievalService) SemanticSearch(
	ctx context.Context, opts options.Searcher, q model.SemanticQuery,
) ([]*model.ChunkHit, []*model.Citation, error) {
	if strings.TrimSpace(q.Query) == "" {
		return make([]*model.ChunkHit, 0), make([]*model.Citation, 0), nil
	}

	if len(q.SpaceIDs) == 0 {
		return nil, nil, errors.InvalidArgument(
			"at least one space is required",
			errors.WithID("kb.retrieval.space_required"),
		)
	}

	spaces, err := s.uow.SpaceStore().ResolveEmbeddings(ctx, opts.GetAuthOpts().GetDomainID(), q.SpaceIDs)
	if err != nil {
		return nil, nil, err
	}

	filter := model.SearchFilter{SpaceIDs: q.SpaceIDs, Tags: q.Tags, TagsMatchAll: q.TagsMatchAll}

	hits, err := s.fuse(ctx, opts, q.Query, spaces, filter, topK(q.TopK))
	if err != nil {
		return nil, nil, err
	}

	citations := make([]*model.Citation, 0)
	if q.IncludeCitations {
		citations = citationsOf(hits)
	}

	return hits, citations, nil
}

// fuse embeds the query per model and runs the fused ranking of chunks.
func (s *RetrievalService) fuse(
	ctx context.Context, opts options.Searcher, query string, spaces []*model.SpaceEmbedding, filter model.SearchFilter, topK int,
) ([]*model.ChunkHit, error) {
	vectors, err := s.embedQuery(ctx, query, spaces)
	if err != nil {
		return nil, err
	}

	hybrid := model.HybridQuery{Term: query, Filter: filter, Vectors: vectors, TopK: topK}
	hits := make([]*model.ChunkHit, 0)

	// The rescore setting is local to the transaction the query runs in.
	err = s.uow.WithinTransaction(ctx, func(ctx context.Context, uow store.UnitOfWork) error {
		found, err := uow.RetrievalStore().SemanticSearch(ctx, opts, hybrid)
		if err != nil {
			return err
		}

		hits = append(hits, found...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return hits, nil
}

// embedQuery embeds the text once per model the spaces are indexed with.
func (s *RetrievalService) embedQuery(
	ctx context.Context, query string, spaces []*model.SpaceEmbedding,
) ([]model.ModelVector, error) {
	vectors := make([]model.ModelVector, 0)
	byModel := make(map[int64]int)

	for _, space := range spaces {
		if !space.VectorSearchEnabled {
			continue
		}

		if space.ModelID == 0 {
			return nil, errors.New(
				"space has no embedding model",
				errors.WithCode(codes.FailedPrecondition),
				errors.WithID("kb.space.model_unset"),
			)
		}

		if i, seen := byModel[space.ModelID]; seen {
			vectors[i].SpaceIDs = append(vectors[i].SpaceIDs, space.SpaceID)

			continue
		}

		vector, err := s.embedWith(ctx, query, space)
		if err != nil {
			return nil, err
		}

		byModel[space.ModelID] = len(vectors)
		vectors = append(vectors, model.ModelVector{ModelID: space.ModelID, SpaceIDs: []int64{space.SpaceID}, Vector: vector})
	}

	return vectors, nil
}

// embedWith embeds the text under the model of a space.
func (s *RetrievalService) embedWith(ctx context.Context, query string, space *model.SpaceEmbedding) ([]float32, error) {
	key, err := openModelCredential(ctx, s.enc, space.Provider, space.Config)
	if err != nil {
		return nil, err
	}

	provider, err := s.providerFor(space.Provider)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()

	result, err := provider.Embed(ctx, embedding.EmbedRequest{
		ModelRef:   space.ModelRef,
		APIKey:     key,
		Endpoint:   space.Endpoint,
		Dimensions: int(space.Dimensions),
		Task:       embedding.TaskQuery,
		Texts:      []string{query},
	})
	if err != nil {
		return nil, embedError(err)
	}

	if len(result.Vectors) != 1 {
		return nil, errors.Unavailable(
			"embedding provider returned no vector",
			errors.WithID("kb.retrieval.embedding_unavailable"),
		)
	}

	return result.Vectors[0], nil
}

// providerFor resolves the client of a provider key.
func (s *RetrievalService) providerFor(key string) (embedding.Provider, error) {
	provider, err := s.providers.ForModel(key)
	if err != nil {
		return nil, errors.Internal(
			"embedding provider is not supported",
			errors.WithID("kb.model.provider_unsupported"),
			errors.WithCause(err),
		)
	}

	return provider, nil
}

// embedError tells a refused credential from an unavailable provider.
func embedError(err error) error {
	var apiErr *embedding.APIError
	if stderrors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
		return errors.New(
			"embedding provider rejected the credential",
			errors.WithCode(codes.FailedPrecondition),
			errors.WithID("kb.model.credential_rejected"),
			errors.WithCause(err),
		)
	}

	return errors.Unavailable(
		"embedding provider is unavailable",
		errors.WithID("kb.retrieval.embedding_unavailable"),
		errors.WithCause(err),
	)
}

// topK resolves the requested result size.
func topK(asked int) int {
	switch {
	case asked <= 0:
		return defaultTopK
	case asked > maxTopK:
		return maxTopK
	default:
		return asked
	}
}

// citationsOf keeps the first hit of every article, in fusion order.
func citationsOf(hits []*model.ChunkHit) []*model.Citation {
	leading := leadingHits(hits, len(hits))
	citations := make([]*model.Citation, 0, len(leading))

	for _, hit := range leading {
		citations = append(citations, &model.Citation{
			ArticleID: hit.ArticleID,
			Title:     hit.Subject,
			Snippet:   excerpt(hit.Content),
		})
	}

	return citations
}

// excerpt cuts a text to the excerpt length on a rune boundary.
func excerpt(text string) string {
	runes := []rune(text)
	if len(runes) <= queryobject.ExcerptLength {
		return text
	}

	return string(runes[:queryobject.ExcerptLength])
}
