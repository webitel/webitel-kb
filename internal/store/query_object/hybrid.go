package queryobject

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/model"
)

// RRFK softens the reciprocal rank so a top position on one side does not dominate.
const RRFK = 60

// BranchDepth is how many chunks each side contributes to the fusion.
const BranchDepth = 50

// lexicalFrom enters the chunks of the spaces a text query was parsed for.
const lexicalFrom = "tq JOIN kb.space s ON s.text_search_config = tq.config" +
	" JOIN kb.article m ON m.space_id = s.id JOIN kb.chunk c ON c.version_id = m.published_version_id"

// embeddingFrom enters through the vector of a chunk; the article only confirms the published text.
const embeddingFrom = "kb.chunk_embedding e JOIN kb.chunk c ON c.id = e.chunk_id JOIN kb.article m ON m.published_version_id = c.version_id"

// vectorBranch is the ranking of one model over its spaces.
type vectorBranch struct {
	builder squirrel.SelectBuilder
	modelID int64
	spaces  []int64
	column  string
	literal string
}

// HybridHits fuses the lexical and the vector ranking of chunks.
type HybridHits struct {
	term    string
	configs squirrel.SelectBuilder
	lex     squirrel.SelectBuilder
	vec     []vectorBranch
	filter  model.SearchFilter
	topK    int
}

// NewHybridHits starts the fused query of a term and its vectors.
func NewHybridHits(q model.HybridQuery) *HybridHits {
	h := &HybridHits{
		term:    q.Term,
		configs: newSpaceConfigs(),
		lex: squirrel.Select("c.id", "ts_rank_cd(c.tsv, tq.query) AS rank_key").
			From(lexicalFrom).
			Where("c.tsv @@ tq.query"),
		filter: q.Filter,
		topK:   q.TopK,
	}

	for _, mv := range q.Vectors {
		literal := VectorLiteral(mv.Vector)
		column := "e." + model.EmbeddingColumn(mv.Dimensions)

		h.vec = append(h.vec, vectorBranch{
			builder: squirrel.Select("c.id").
				Column(column+" <=> ?::vector AS rank_key", literal).
				From(embeddingFrom).
				Where("e.model_id = ?", mv.ModelID),
			modelID: mv.ModelID,
			spaces:  mv.SpaceIDs,
			column:  column,
			literal: literal,
		})
	}

	return h
}

// WithScope applies the criteria every side shares; a vector side keeps to its own spaces.
func (h *HybridHits) WithScope(domainID int64) *HybridHits {
	h.configs = scopeSpaceConfigs(h.configs, domainID, h.filter.SpaceIDs)

	lex := whereRetrievable(whereDomainScope(h.lex, domainID))
	lex = whereScopeSpaces(lex, h.filter.SpaceIDs)
	h.lex = whereTags(lex, h.filter.Tags, h.filter.TagsMatchAll).
		OrderBy("rank_key DESC", "c.id").
		Limit(BranchDepth)

	for i := range h.vec {
		// The scope sits on the vector row, so the filter needs no join to apply;
		// the spaces of a side arrive active already.
		vec := whereLiveArticle(h.vec[i].builder.Where("e.domain_id = ?", domainID)).
			Where("e.space_id = ANY(?)", h.vec[i].spaces)
		h.vec[i].builder = whereTags(vec, h.filter.Tags, h.filter.TagsMatchAll).
			OrderByClause(h.vec[i].column+" <=> ?::vector, c.id", h.vec[i].literal).
			Limit(BranchDepth)
	}

	return h
}

// WithAccess keeps the articles the plan lets the caller reach on every side.
func (h *HybridHits) WithAccess(plan AccessPlan) *HybridHits {
	predicate := plan.ArticlePredicate()

	h.lex = h.lex.Where(predicate)

	for i := range h.vec {
		h.vec[i].builder = h.vec[i].builder.Where(predicate)
	}

	return h
}

// ToSQL renders the whole fused statement.
func (h *HybridHits) ToSQL() (string, []any, error) {
	queries, args, err := textQueries(h.term, h.configs).ToSql()
	if err != nil {
		return "", nil, err
	}

	lex, lexArgs, err := h.lex.ToSql()
	if err != nil {
		return "", nil, err
	}

	with := fmt.Sprintf("WITH tq AS MATERIALIZED (%s), lex AS (%s)", queries, rankedList(lex))
	union := "SELECT * FROM lex"

	args = append(args, lexArgs...)

	if len(h.vec) > 0 {
		branches := make([]string, 0, len(h.vec))

		for _, side := range h.vec {
			sql, sideArgs, err := side.builder.ToSql()
			if err != nil {
				return "", nil, err
			}

			// A side carries its own order and limit, so it stays a parenthesized member of the union.
			branches = append(branches, "("+sql+")")
			args = append(args, sideArgs...)
		}

		with += fmt.Sprintf(
			", vec AS (SELECT id, row_number() OVER (ORDER BY rank_key, id) AS rank FROM (%s) t)",
			strings.Join(branches, " UNION ALL "),
		)
		union += " UNION ALL SELECT * FROM vec"
	}

	with += fmt.Sprintf(
		", h AS (SELECT id, sum(1.0 / (%d + rank)) AS score FROM (%s) branches GROUP BY id ORDER BY score DESC, id LIMIT %d)",
		RRFK, union, h.topK,
	)

	// The fusion orders the hits; the similarity is what a hit is worth.
	return h.withSimilarity(squirrel.Select(
		"c.id AS id", "m.id AS article_id", "c.version_id AS version_id", "c.chunk_index AS chunk_index",
		"m.subject AS subject", "c.content AS content",
	).
		From("h JOIN kb.chunk c ON c.id = h.id JOIN kb.article m ON m.published_version_id = c.version_id")).
		PrefixExpr(squirrel.Expr(with, args...)).
		OrderBy("h.score DESC", "c.id").
		PlaceholderFormat(squirrel.Dollar).
		ToSql()
}

// withSimilarity selects the cosine similarity of a hit to the query under the
// model of its space; NULL when the hit has no vector under that model.
func (h *HybridHits) withSimilarity(builder squirrel.SelectBuilder) squirrel.SelectBuilder {
	if len(h.vec) == 0 {
		return builder.Column("NULL::float8 AS similarity")
	}

	terms := make([]string, 0, len(h.vec))
	args := make([]any, 0, len(h.vec))

	for i, side := range h.vec {
		alias := fmt.Sprintf("e%d", i)

		builder = builder.LeftJoin(
			fmt.Sprintf("kb.chunk_embedding %[1]s ON %[1]s.chunk_id = c.id AND %[1]s.model_id = ? AND %[1]s.space_id = ANY(?)", alias),
			side.modelID, side.spaces,
		)
		terms = append(terms, fmt.Sprintf("1 - (%s.%s <=> ?::vector)", alias, strings.TrimPrefix(side.column, "e.")))
		args = append(args, side.literal)
	}

	return builder.Column(fmt.Sprintf("COALESCE(%s)::float8 AS similarity", strings.Join(terms, ", ")), args...)
}

// VectorLiteral renders a vector the way pgvector reads it.
func VectorLiteral(v []float32) string {
	parts := make([]string, 0, len(v))
	for _, x := range v {
		parts = append(parts, strconv.FormatFloat(float64(x), 'g', -1, 32))
	}

	return "[" + strings.Join(parts, ",") + "]"
}
