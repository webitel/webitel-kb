package queryobject

// SpaceFrom is the base relation of the space query object.
const SpaceFrom = "kb.space m"

// Join bits of the space query object.
const (
	spaceJoinCreatedBy = 1 << iota
	spaceJoinUpdatedBy
)

// spaceTeamsExpr aggregates the team binding into a JSON array right in the
// SELECT list.
const spaceTeamsExpr = `(SELECT COALESCE(json_agg(json_build_object('id', t.id, 'name', t.name) ORDER BY t.name), '[]')` +
	` FROM kb.team_space ts JOIN call_center.cc_team t ON t.id = ts.team_id WHERE ts.space_id = m.id) AS teams`

// SpaceQuery builds SELECTs over knowledge-base spaces.
type SpaceQuery struct {
	*baseQueryObject[*SpaceQuery]

	meta  map[string]fieldMetadata
	joins int
}

// NewSpaceQuery starts a query over from, normally SpaceFrom.
func NewSpaceQuery(from string) *SpaceQuery {
	q := new(SpaceQuery)
	q.baseQueryObject = newBaseQueryObject(from, q)

	return q
}

func (q *SpaceQuery) DefaultFields() []string {
	return []string{
		"id", "domain_id", "name", "description", "language",
		"embedding_model_id", "target_embedding_model_id", "reranker_model_id",
		"vector_search_enabled", "rerank_enabled", "chunking_strategy", "home_article_id",
		"teams", "created_at", "updated_at", "created_by", "updated_by",
	}
}

func (q *SpaceQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":                        {sqlExpr: "m.id", aliasedExpr: "m.id AS id", sortable: true},
			"domain_id":                 {sqlExpr: "m.domain_id", aliasedExpr: "m.domain_id AS domain_id"},
			"name":                      {sqlExpr: "m.name", aliasedExpr: "m.name AS name", sortable: true},
			"description":               {sqlExpr: "m.description", aliasedExpr: "m.description AS description"},
			"language":                  {sqlExpr: "m.language", aliasedExpr: "m.language AS language", sortable: true},
			"embedding_model_id":        {sqlExpr: "m.embedding_model_id", aliasedExpr: "m.embedding_model_id AS embedding_model_id"},
			"target_embedding_model_id": {sqlExpr: "m.target_embedding_model_id", aliasedExpr: "m.target_embedding_model_id AS target_embedding_model_id"},
			"reranker_model_id":         {sqlExpr: "m.reranker_model_id", aliasedExpr: "m.reranker_model_id AS reranker_model_id"},
			"vector_search_enabled":     {sqlExpr: "m.vector_search_enabled", aliasedExpr: "m.vector_search_enabled AS vector_search_enabled"},
			"rerank_enabled":            {sqlExpr: "m.rerank_enabled", aliasedExpr: "m.rerank_enabled AS rerank_enabled"},
			"chunking_strategy":         {sqlExpr: "m.chunking_strategy", aliasedExpr: "m.chunking_strategy AS chunking_strategy"},
			"home_article_id":           {sqlExpr: "m.home_article_id", aliasedExpr: "m.home_article_id AS home_article_id"},
			"teams":                     {sqlExpr: "teams", aliasedExpr: spaceTeamsExpr},
			"created_at":                {sqlExpr: "m.created_at", aliasedExpr: "m.created_at AS created_at", sortable: true},
			"updated_at":                {sqlExpr: "m.updated_at", aliasedExpr: "m.updated_at AS updated_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: spaceJoinCreatedBy,
				sortable:     true,
			},
			"updated_by": {
				sqlExpr:      "COALESCE(ub.name, ub.username)",
				aliasedExpr:  "ub.id AS updated_by_id, COALESCE(ub.name, ub.username) AS updated_by_name",
				requiresJoin: spaceJoinUpdatedBy,
				sortable:     true,
			},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *SpaceQuery) EnsureJoins(required int) {
	missing := required &^ q.joins

	if missing&spaceJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.created_by")
	}

	if missing&spaceJoinUpdatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user ub ON ub.id = m.updated_by")
	}

	q.joins |= required
}

// WithDomainScope keeps the spaces of the given domain.
func (q *SpaceQuery) WithDomainScope(domainID int64) *SpaceQuery {
	q.builder = q.builder.Where("m.domain_id = ?", domainID)

	return q
}

// WithLockForUpdate locks the selected space rows until the transaction ends,
// so a read-then-write flow cannot race a concurrent writer. Locks only the
// space relation: a locking clause cannot cover the nullable side of the user
// joins.
func (q *SpaceQuery) WithLockForUpdate() *SpaceQuery {
	q.builder = q.builder.Suffix("FOR UPDATE OF m")

	return q
}

// WithSearch keeps spaces whose name contains the term, case-insensitively.
func (q *SpaceQuery) WithSearch(term string) *SpaceQuery {
	if term != "" {
		q.builder = q.builder.Where("m.name ILIKE ?", "%"+EscapeLike(term)+"%")
	}

	return q
}

// WithIDs keeps spaces with the given ids; empty means any.
func (q *SpaceQuery) WithIDs(ids []int64) *SpaceQuery {
	if len(ids) > 0 {
		q.builder = q.builder.Where("m.id = ANY(?)", ids)
	}

	return q
}
