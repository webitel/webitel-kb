package queryobject

// ArticleVersionFrom is the base relation of the article version query object.
const ArticleVersionFrom = "kb.article_version m"

// Join bits of the article version query object.
const articleVersionJoinCreatedBy = 1 << iota

// ArticleVersionQuery builds SELECTs over article versions. The search vector
// is not selectable: it serves retrieval, not the read model.
type ArticleVersionQuery struct {
	*baseQueryObject[*ArticleVersionQuery]

	meta  map[string]fieldMetadata
	joins int
}

// NewArticleVersionQuery starts a query over from, normally
// ArticleVersionFrom.
func NewArticleVersionQuery(from string) *ArticleVersionQuery {
	q := new(ArticleVersionQuery)
	q.baseQueryObject = newBaseQueryObject(from, q)

	return q
}

func (q *ArticleVersionQuery) DefaultFields() []string {
	return []string{
		"id", "article_id", "version_number", "subject", "body_rich_text",
		"body_markdown", "body_plain", "restored_from", "notes",
		"created_at", "created_by",
	}
}

// IdentityFields is the id the save flow addresses a stored version by.
func (q *ArticleVersionQuery) IdentityFields() []string { return []string{"id"} }

func (q *ArticleVersionQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":             {sqlExpr: "m.id", aliasedExpr: "m.id AS id", sortable: true},
			"article_id":     {sqlExpr: "m.article_id", aliasedExpr: "m.article_id AS article_id"},
			"version_number": {sqlExpr: "m.version_number", aliasedExpr: "m.version_number AS version_number", sortable: true},
			"subject":        {sqlExpr: "m.subject", aliasedExpr: "m.subject AS subject"},
			"body_rich_text": {sqlExpr: "m.body_rich_text", aliasedExpr: "m.body_rich_text AS body_rich_text"},
			"body_markdown":  {sqlExpr: "m.body_markdown", aliasedExpr: "m.body_markdown AS body_markdown"},
			"body_plain":     {sqlExpr: "m.body_plain", aliasedExpr: "m.body_plain AS body_plain"},
			"restored_from":  {sqlExpr: "m.restored_from", aliasedExpr: "m.restored_from AS restored_from"},
			"notes":          {sqlExpr: "m.notes", aliasedExpr: "m.notes AS notes"},
			"created_at":     {sqlExpr: "m.created_at", aliasedExpr: "m.created_at AS created_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: articleVersionJoinCreatedBy,
				sortable:     true,
			},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *ArticleVersionQuery) EnsureJoins(required int) {
	missing := required &^ q.joins

	if missing&articleVersionJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.created_by")
	}

	q.joins |= required
}

// WithDomainScope keeps the versions of articles owned by the domain.
func (q *ArticleVersionQuery) WithDomainScope(domainID int64) *ArticleVersionQuery {
	q.builder = q.builder.Where(
		"EXISTS (SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id"+
			" WHERE a.id = m.article_id AND s.domain_id = ? AND a.deleted_at IS NULL)",
		domainID,
	)

	return q
}

// WithArticle keeps the versions of the given article.
func (q *ArticleVersionQuery) WithArticle(articleID int64) *ArticleVersionQuery {
	q.builder = q.builder.Where("m.article_id = ?", articleID)

	return q
}

// WithNumber keeps the single version carrying the given number.
func (q *ArticleVersionQuery) WithNumber(number int32) *ArticleVersionQuery {
	q.builder = q.builder.Where("m.version_number = ?", number)

	return q
}
