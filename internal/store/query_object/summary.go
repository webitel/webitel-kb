package queryobject

import (
	"fmt"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/model"
)

// SummaryFrom is the base relation of the summary query object.
const SummaryFrom = "kb.article m"

// HitsFrom reads the summaries of ranked search hits.
const HitsFrom = "h JOIN kb.article m ON m.id = h.id"

// ExcerptLength bounds the leading excerpt of a body.
const ExcerptLength = 240

// headlineOptions shape the fragment a search hit is shown with.
const headlineOptions = `MaxWords=32, MinWords=12, MaxFragments=1, StartSel="", StopSel=""`

// Join bits of the summary query object.
const (
	summaryJoinSpace = 1 << iota
	summaryJoinVersion
)

// tsQuery renders the parsed form of a search term.
func tsQuery() string {
	return fmt.Sprintf("websearch_to_tsquery('%s', ?)", model.TextSearchDefault)
}

// The predicates below are shared by the summary projection and a search.

func whereDomainScope(builder squirrel.SelectBuilder, domainID int64) squirrel.SelectBuilder {
	return builder.Where("s.domain_id = ?", domainID)
}

// whereRetrievable keeps live articles in the active state.
func whereRetrievable(builder squirrel.SelectBuilder) squirrel.SelectBuilder {
	return builder.Where("m.deleted_at IS NULL").Where("m.state = ?", model.ArticleStateActive)
}

func whereSpaces(builder squirrel.SelectBuilder, spaceIDs []int64) squirrel.SelectBuilder {
	if len(spaceIDs) == 0 {
		return builder
	}

	return builder.Where("m.space_id = ANY(?)", spaceIDs)
}

// whereTags keeps articles carrying the given tags.
func whereTags(builder squirrel.SelectBuilder, tags []string, matchAll bool) squirrel.SelectBuilder {
	switch {
	case len(tags) == 0:
		return builder
	case matchAll:
		return builder.Where("m.tags @> ?", tags)
	default:
		return builder.Where("m.tags && ?", tags)
	}
}

// SearchHits ranks the articles a query matches, one branch per lexical side.
type SearchHits struct {
	subject squirrel.SelectBuilder
	body    squirrel.SelectBuilder
	size    int
	page    int
}

// NewSearchHits starts the ranked query of a term.
func NewSearchHits(term string) *SearchHits {
	query := tsQuery()

	return &SearchHits{
		subject: squirrel.Select("m.id").
			Column(fmt.Sprintf("ts_rank_cd(m.search_tsv, %s) AS rank", query), term).
			From(SummaryFrom).
			Join("kb.space s ON s.id = m.space_id").
			Where(fmt.Sprintf("m.search_tsv @@ %s", query), term),
		body: squirrel.Select("m.id").
			Column(fmt.Sprintf("ts_rank_cd(v.tsv, %s) AS rank", query), term).
			From(SummaryFrom).
			Join("kb.space s ON s.id = m.space_id").
			Join("kb.article_version v ON v.id = m.published_version_id").
			Where(fmt.Sprintf("v.tsv @@ %s", query), term),
	}
}

// WithScope applies the criteria both branches share.
func (h *SearchHits) WithScope(domainID int64, filter model.SearchFilter) *SearchHits {
	for _, branch := range []*squirrel.SelectBuilder{&h.subject, &h.body} {
		scoped := whereDomainScope(*branch, domainID)
		scoped = whereRetrievable(scoped)
		scoped = whereSpaces(scoped, filter.SpaceIDs)
		*branch = whereTags(scoped, filter.Tags, filter.TagsMatchAll)
	}

	return h
}

// WithPaging cuts the ranked hits down to one page, one row past it.
func (h *SearchHits) WithPaging(size, page int) *SearchHits {
	h.size, h.page = size, page

	return h
}

// ToSQL renders the ranked query as a common table expression named h.
func (h *SearchHits) ToSQL() (string, []any, error) {
	subject, subjectArgs, err := h.subject.ToSql()
	if err != nil {
		return "", nil, err
	}

	body, bodyArgs, err := h.body.ToSql()
	if err != nil {
		return "", nil, err
	}

	hits := fmt.Sprintf(
		"WITH h AS (SELECT id, sum(rank) AS rank FROM (%s UNION ALL %s) branches"+
			" GROUP BY id ORDER BY rank DESC, id%s)",
		subject, body, pagingClause(h.size, h.page),
	)

	return hits, append(subjectArgs, bodyArgs...), nil
}

// pagingClause renders the page as literals.
func pagingClause(size, page int) string {
	if size <= 0 {
		return ""
	}

	// One row past the page answers whether a next page exists.
	clause := fmt.Sprintf(" LIMIT %d", size+1)
	if page > 1 {
		clause += fmt.Sprintf(" OFFSET %d", (page-1)*size)
	}

	return clause
}

// SummaryQuery builds the retrieval projection of an article.
type SummaryQuery struct {
	builder squirrel.SelectBuilder
	joins   int
	// compact reports whether the rendered statement may be squeezed.
	compact bool
}

// NewSummaryQuery starts a query over from, normally SummaryFrom.
func NewSummaryQuery(from string) *SummaryQuery {
	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Select(
			"m.id AS id",
			"m.space_id AS space_id",
			"COALESCE(m.parent_id, 0) AS parent_id",
			"m.depth AS depth",
			"m.type AS type",
			"m.subject AS subject",
			"m.tags AS tags",
			"m.state AS state",
			"m.created_at AS created_at",
			"m.updated_at AS updated_at",
		).
		From(from)

	return &SummaryQuery{builder: builder, compact: true}
}

// ensureJoins appends the join clauses of newly required bits.
func (q *SummaryQuery) ensureJoins(required int) {
	missing := required &^ q.joins

	if missing&summaryJoinSpace != 0 {
		q.builder = q.builder.Join("kb.space s ON s.id = m.space_id")
	}

	if missing&summaryJoinVersion != 0 {
		// Left: an article without a published version stays findable.
		q.builder = q.builder.LeftJoin("kb.article_version v ON v.id = m.published_version_id")
	}

	q.joins |= required
}

// WithDomainScope keeps the articles of spaces owned by the domain.
func (q *SummaryQuery) WithDomainScope(domainID int64) *SummaryQuery {
	q.ensureJoins(summaryJoinSpace)
	q.builder = whereDomainScope(q.builder, domainID)

	return q
}

// WithRetrievable keeps the articles a reader may retrieve.
func (q *SummaryQuery) WithRetrievable() *SummaryQuery {
	q.builder = whereRetrievable(q.builder)

	return q
}

// WithSpaces keeps the articles of the given spaces; empty means any.
func (q *SummaryQuery) WithSpaces(spaceIDs []int64) *SummaryQuery {
	q.builder = whereSpaces(q.builder, spaceIDs)

	return q
}

// WithIDs keeps the given ids and answers in the order they were asked for.
func (q *SummaryQuery) WithIDs(ids []int64) *SummaryQuery {
	if len(ids) == 0 {
		return q
	}

	q.builder = q.builder.
		Where("m.id = ANY(?)", ids).
		OrderByClause("array_position(?::bigint[], m.id)", ids)

	return q
}

// WithHeadline returns the fragment the term matched as the snippet.
func (q *SummaryQuery) WithHeadline(term string) *SummaryQuery {
	q.ensureJoins(summaryJoinVersion)
	q.compact = false
	q.builder = q.builder.Column(
		fmt.Sprintf("COALESCE(ts_headline('%s', v.body_plain, %s, '%s'), '') AS snippet",
			model.TextSearchDefault, tsQuery(), headlineOptions),
		term,
	)

	return q
}

// WithExcerpt returns the leading text of the published body as the snippet.
func (q *SummaryQuery) WithExcerpt() *SummaryQuery {
	q.ensureJoins(summaryJoinVersion)
	q.builder = q.builder.Column("COALESCE(left(v.body_plain, ?), '') AS snippet", ExcerptLength)

	return q
}

// WithFAQBody inlines the whole text of an FAQ.
func (q *SummaryQuery) WithFAQBody() *SummaryQuery {
	q.ensureJoins(summaryJoinVersion)
	q.builder = q.builder.Column(
		"CASE WHEN m.type = ? THEN COALESCE(v.body_plain, '') ELSE '' END AS body",
		model.ArticleTypeFAQ,
	)

	return q
}

// WithPrefix puts a clause in front of the SELECT.
func (q *SummaryQuery) WithPrefix(clause string, args ...any) *SummaryQuery {
	q.builder = q.builder.PrefixExpr(squirrel.Expr(clause, args...))

	return q
}

// WithOrder appends ordering criteria as written.
func (q *SummaryQuery) WithOrder(clauses ...string) *SummaryQuery {
	q.builder = q.builder.OrderBy(clauses...)

	return q
}

// WithLimit caps a listing the contract cannot paginate.
func (q *SummaryQuery) WithLimit(limit uint64) *SummaryQuery {
	q.builder = q.builder.Limit(limit)

	return q
}

// ToSQL renders the query.
func (q *SummaryQuery) ToSQL() (string, []any, error) {
	sql, args, err := q.builder.ToSql()
	if !q.compact {
		return sql, args, err
	}

	return CompactSQL(sql), args, err
}
