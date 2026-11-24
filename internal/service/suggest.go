package service

import (
	"cmp"
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
)

// Suggest limits.
const (
	// rerankCandidates is how many fused chunks the reranker scores at least.
	rerankCandidates = 10
	// suggestSize is how many articles or chunks a suggest answers with.
	suggestSize = 3
	// rerankTimeout bounds the rerank call.
	rerankTimeout = 5 * time.Second
	// answerReserve is the time kept before the caller deadline to answer
	// without rerank.
	answerReserve = 500 * time.Millisecond
)

// Suggest picks the articles or chunks that answer a customer message.
func (s *RetrievalService) Suggest(
	ctx context.Context, opts options.Searcher, q model.SuggestQuery,
) (*model.Suggestion, error) {
	answer := &model.Suggestion{Articles: make([]*model.ArticleSummary, 0), Chunks: make([]*model.ChunkHit, 0)}

	if strings.TrimSpace(q.Message) == "" {
		return answer, nil
	}

	domainID := opts.GetAuthOpts().GetDomainID()

	spaceIDs, reach, err := teamSpaces(ctx, s.uow.SpaceStore(), opts, q.TeamID, q.SpaceIDs)
	if err != nil {
		return nil, err
	}

	if !reach {
		return answer, nil
	}

	spaces, err := s.uow.SpaceStore().ResolveEmbeddings(ctx, domainID, spaceIDs)
	if err != nil {
		return nil, err
	}

	if len(spaces) == 0 {
		return answer, nil
	}

	spaceIDs = spaceIDsOf(spaces)
	message := clipQuery(q.Message)

	hits, err := s.fuse(ctx, opts, message, spaces, model.SearchFilter{SpaceIDs: spaceIDs}, rerankCandidates)
	if err != nil {
		return nil, err
	}

	if len(hits) == 0 {
		return answer, nil
	}

	hits, reranker := s.rerankHits(ctx, domainID, spaceIDs, message, hits)
	answer.Reranked = reranker != nil

	hits = aboveMinScore(hits, reranker, spaces)
	if len(hits) == 0 {
		return answer, nil
	}

	if q.ReturnChunks {
		answer.Chunks = hits[:min(len(hits), suggestSize)]

		return answer, nil
	}

	best := leadingHits(hits, suggestSize)

	articles, err := s.uow.RetrievalStore().Resolve(ctx, opts, articleIDsOf(best), spaceIDs)
	if err != nil {
		return nil, err
	}

	answer.Articles = withMatchedSnippet(articles, best)

	return answer, nil
}

// rerankHits reorders and scores the hits with the reranker of the spaces and
// returns the reranker that did; nil when none did. Reranking is an optional
// step: whatever goes wrong, the fusion order stands.
func (s *RetrievalService) rerankHits(
	ctx context.Context, domainID int64, spaceIDs []int64, query string, hits []*model.ChunkHit,
) ([]*model.ChunkHit, *model.SpaceReranker) {
	ordered, reranker, err := s.rerank(ctx, domainID, spaceIDs, query, hits)
	if err != nil {
		s.log.WarnContext(ctx, "hits are not reranked", slog.Any("error", err))

		return hits, nil
	}

	return ordered, reranker
}

// rerank scores the whole pool with the reranker the spaces pick. The fusion
// order comes back untouched, with no reranker, when no space reranks.
func (s *RetrievalService) rerank(
	ctx context.Context, domainID int64, spaceIDs []int64, query string, hits []*model.ChunkHit,
) ([]*model.ChunkHit, *model.SpaceReranker, error) {
	rerankers, err := s.uow.SpaceStore().ResolveRerankers(ctx, domainID, spaceIDs)
	if err != nil {
		return nil, nil, err
	}

	reranker := pickReranker(rerankers)
	if reranker == nil {
		return hits, nil, nil
	}

	timeout := rerankTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline.Add(-answerReserve)))
	}

	if timeout <= 0 {
		return nil, nil, errRerankBudget
	}

	scores, err := s.rerankWith(ctx, timeout, query, reranker, hits)
	if err != nil {
		return nil, nil, fmt.Errorf("reranker %d: %w", reranker.ModelID, err)
	}

	return orderByScore(hits, scores), reranker, nil
}

// errRerankBudget reports a rerank skipped for lack of time to answer.
var errRerankBudget = stderrors.New("no time left to rerank")

// pickReranker picks the reranker a request scores with among those the spaces
// enable: one with a threshold first, then the earliest registered; nil when
// none.
func pickReranker(spaces []*model.SpaceReranker) *model.SpaceReranker {
	var picked *model.SpaceReranker

	for _, space := range spaces {
		if space.Enabled && (picked == nil || rerankerBefore(space, picked)) {
			picked = space
		}
	}

	return picked
}

func rerankerBefore(a, b *model.SpaceReranker) bool {
	if (a.MinScore == nil) != (b.MinScore == nil) {
		return a.MinScore != nil
	}

	return a.ModelID < b.ModelID
}

// rerankWith scores the hits against the query under the reranker of a space
// within the timeout.
func (s *RetrievalService) rerankWith(
	ctx context.Context, timeout time.Duration, query string, space *model.SpaceReranker, hits []*model.ChunkHit,
) ([]float64, error) {
	key, err := openModelCredential(ctx, s.enc, space.Provider, space.Config)
	if err != nil {
		return nil, err
	}

	provider, err := s.providerFor(space.Provider)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := provider.Rerank(ctx, embedding.RerankRequest{
		ModelRef:  space.ModelRef,
		APIKey:    key,
		Endpoint:  space.Endpoint,
		Query:     query,
		Documents: documentsOf(hits),
	})
	if err != nil {
		return nil, fmt.Errorf("reranker is unavailable: %w", err)
	}

	if len(result.Scores) != len(hits) {
		return nil, fmt.Errorf("reranker scored %d of %d documents", len(result.Scores), len(hits))
	}

	if !inUnitRange(result.Scores) {
		return nil, fmt.Errorf("reranker scores are outside [0, 1]: %v", result.Scores)
	}

	return result.Scores, nil
}

// inUnitRange reports whether every score is in [0, 1], the scale a reranker
// must answer on.
func inUnitRange(scores []float64) bool {
	for _, score := range scores {
		if !(score >= 0 && score <= 1) {
			return false
		}
	}

	return true
}

// documentsOf renders the hits the way a cross-encoder reads them.
func documentsOf(hits []*model.ChunkHit) []string {
	docs := make([]string, 0, len(hits))
	for _, hit := range hits {
		docs = append(docs, hit.Subject+"\n"+hit.Content)
	}

	return docs
}

// orderByScore returns the hits carrying the scores, best first; ties keep the fusion order.
func orderByScore(hits []*model.ChunkHit, scores []float64) []*model.ChunkHit {
	ordered := make([]*model.ChunkHit, 0, len(hits))

	for i, hit := range hits {
		scored := *hit
		scored.Score, scored.Measured = scores[i], true
		ordered = append(ordered, &scored)
	}

	slices.SortStableFunc(ordered, func(a, b *model.ChunkHit) int { return cmp.Compare(b.Score, a.Score) })

	return ordered
}

// leadingHits keeps the first hit of every article, in order, up to n articles.
func leadingHits(hits []*model.ChunkHit, n int) []*model.ChunkHit {
	best := make([]*model.ChunkHit, 0, n)
	seen := make(map[int64]struct{}, n)

	for _, hit := range hits {
		if len(best) == n {
			break
		}

		if _, dup := seen[hit.ArticleID]; dup {
			continue
		}

		seen[hit.ArticleID] = struct{}{}
		best = append(best, hit)
	}

	return best
}

// withMatchedSnippet gives every article the text of the chunk that matched.
func withMatchedSnippet(articles []*model.ArticleSummary, hits []*model.ChunkHit) []*model.ArticleSummary {
	matched := make(map[int64]*model.ChunkHit, len(hits))
	for _, hit := range hits {
		matched[hit.ArticleID] = hit
	}

	for _, article := range articles {
		if hit, ok := matched[article.ID]; ok {
			article.Snippet = excerpt(hit.Content)
		}
	}

	return articles
}

// articleIDsOf lists the articles of the hits, in order.
func articleIDsOf(hits []*model.ChunkHit) []int64 {
	ids := make([]int64, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ArticleID)
	}

	return ids
}

// spaceIDsOf lists the spaces that answered.
func spaceIDsOf(spaces []*model.SpaceEmbedding) []int64 {
	ids := make([]int64, 0, len(spaces))
	for _, space := range spaces {
		ids = append(ids, space.SpaceID)
	}

	return ids
}
