package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

// menuFrom reads the summaries of a walked menu.
const menuFrom = "menu JOIN kb.article m ON m.id = menu.id"

// menuWalkCTE walks the children of a parent down to the requested depth.
const menuWalkCTE = `WITH RECURSIVE menu AS (
	SELECT c.id, 1 AS level
	FROM kb.article c JOIN kb.space s ON s.id = c.space_id
	WHERE s.domain_id = ? AND c.space_id = ? AND %s
	  AND c.deleted_at IS NULL AND c.state = ?
	UNION ALL
	SELECT c.id, parent.level + 1
	FROM kb.article c JOIN menu parent ON c.parent_id = parent.id
	WHERE parent.level < ? AND c.deleted_at IS NULL AND c.state = ?
)`

// maxMenuItems bounds a menu the contract cannot paginate.
const maxMenuItems = 100

// queryRescore is how many vector candidates diskann re-checks exactly.
const queryRescore = 200

type retrievalStore struct {
	db Querier
}

var _ store.RetrievalStore = (*retrievalStore)(nil)

// summaryRecord is the scan target of the summary projection.
type summaryRecord struct {
	ID        int64     `db:"id"`
	SpaceID   int64     `db:"space_id"`
	ParentID  int64     `db:"parent_id"`
	Depth     int32     `db:"depth"`
	Type      int32     `db:"type"`
	Subject   string    `db:"subject"`
	Tags      []string  `db:"tags"`
	State     int32     `db:"state"`
	Snippet   string    `db:"snippet"`
	Body      string    `db:"body"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func mapSummary(record *summaryRecord) *model.ArticleSummary {
	return &model.ArticleSummary{
		ID:        record.ID,
		SpaceID:   record.SpaceID,
		ParentID:  record.ParentID,
		Depth:     record.Depth,
		Type:      record.Type,
		Subject:   record.Subject,
		Snippet:   record.Snippet,
		Tags:      record.Tags,
		State:     record.State,
		Body:      record.Body,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}
}

func (s *retrievalStore) Search(
	ctx context.Context, opts options.Searcher, filter model.SearchFilter,
) ([]*model.ArticleSummary, bool, error) {
	term := opts.GetSearch()

	hits, hitArgs, err := queryobject.NewSearchHits(term).
		WithScope(opts.GetAuthOpts().GetDomainID(), filter).
		WithPaging(opts.GetSize(), opts.GetPage()).
		ToSQL()
	if err != nil {
		return nil, false, ParseError(err)
	}

	sql, args, err := queryobject.NewSummaryQuery(queryobject.HitsFrom).
		WithPrefix(hits, hitArgs...).
		WithHeadline(term).
		WithOrder("h.rank DESC", "m.id").
		ToSQL()
	if err != nil {
		return nil, false, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, err := collectRows(rows, mapSummary)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

// chunkRecord is the scan target of a fused hit.
type chunkRecord struct {
	ID         int64   `db:"id"`
	ArticleID  int64   `db:"article_id"`
	VersionID  int64   `db:"version_id"`
	ChunkIndex int32   `db:"chunk_index"`
	Subject    string  `db:"subject"`
	Content    string  `db:"content"`
	Score      float64 `db:"score"`
}

func mapChunk(record *chunkRecord) *model.ChunkHit {
	return &model.ChunkHit{
		ID:         record.ID,
		ArticleID:  record.ArticleID,
		VersionID:  record.VersionID,
		ChunkIndex: record.ChunkIndex,
		Subject:    record.Subject,
		Content:    record.Content,
		Score:      record.Score,
	}
}

func (s *retrievalStore) SemanticSearch(
	ctx context.Context, opts options.Searcher, q model.HybridQuery,
) ([]*model.ChunkHit, error) {
	if _, err := s.db.Exec(ctx, fmt.Sprintf("SET LOCAL diskann.query_rescore = %d", queryRescore)); err != nil {
		return nil, ParseError(err)
	}

	sql, args, err := queryobject.NewHybridHits(q).
		WithScope(opts.GetAuthOpts().GetDomainID()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	items, err := collectRows(rows, mapChunk)
	if err != nil {
		return nil, ParseError(err)
	}

	return items, nil
}

func (s *retrievalStore) Resolve(
	ctx context.Context, opts options.Searcher, spaceIDs []int64,
) ([]*model.ArticleSummary, error) {
	sql, args, err := queryobject.NewSummaryQuery(queryobject.SummaryFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithRetrievable().
		WithIDs(opts.GetIDs()).
		WithSpaces(spaceIDs).
		WithExcerpt().
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	items, err := collectRows(rows, mapSummary)
	if err != nil {
		return nil, ParseError(err)
	}

	return items, nil
}

func (s *retrievalStore) Menu(
	ctx context.Context, opts options.Searcher, spaceID, parentID int64, depthLimit int32,
) ([]*model.ArticleSummary, error) {
	walk, args := menuWalk(opts.GetAuthOpts().GetDomainID(), spaceID, parentID, depthLimit)

	sql, args, err := queryobject.NewSummaryQuery(menuFrom).
		WithPrefix(walk, args...).
		WithExcerpt().
		WithFAQBody().
		WithOrder("menu.level", "m.subject", "m.id").
		WithLimit(maxMenuItems).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	items, err := collectRows(rows, mapSummary)
	if err != nil {
		return nil, ParseError(err)
	}

	return items, nil
}

// menuWalk renders the walk of a menu.
func menuWalk(domainID, spaceID, parentID int64, depthLimit int32) (string, []any) {
	args := []any{domainID, spaceID}

	parent := "c.parent_id IS NULL"
	if parentID > 0 {
		parent = "c.parent_id = ?"

		args = append(args, parentID)
	}

	args = append(args, model.ArticleStateActive, depthLimit, model.ArticleStateActive)

	return fmt.Sprintf(menuWalkCTE, parent), args
}
