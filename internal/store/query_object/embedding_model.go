package queryobject

// EmbeddingModelFrom is the base relation of the embedding model query object.
const EmbeddingModelFrom = "kb.embedding_model m"

// Join bits of the embedding model query object.
const embeddingModelJoinCreatedBy = 1 << iota

// EmbeddingModelQuery builds SELECTs over the model registry.
type EmbeddingModelQuery struct {
	*baseQueryObject[*EmbeddingModelQuery]

	meta  map[string]fieldMetadata
	joins int
}

// NewEmbeddingModelQuery starts a query over from, normally EmbeddingModelFrom.
func NewEmbeddingModelQuery(from string) *EmbeddingModelQuery {
	q := new(EmbeddingModelQuery)
	q.baseQueryObject = newBaseQueryObject(from, q)

	return q
}

func (q *EmbeddingModelQuery) DefaultFields() []string {
	return []string{
		"id", "domain_id", "type", "name", "provider", "is_self_hosted",
		"model_ref", "dimensions", "endpoint", "validated_at", "created_at", "created_by",
	}
}

func (q *EmbeddingModelQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":             {sqlExpr: "m.id", aliasedExpr: "m.id AS id", sortable: true},
			"domain_id":      {sqlExpr: "m.domain_id", aliasedExpr: "m.domain_id AS domain_id"},
			"type":           {sqlExpr: "m.type", aliasedExpr: "m.type AS type", sortable: true},
			"name":           {sqlExpr: "m.name", aliasedExpr: "m.name AS name", sortable: true},
			"provider":       {sqlExpr: "m.provider", aliasedExpr: "m.provider AS provider", sortable: true},
			"is_self_hosted": {sqlExpr: "m.is_self_hosted", aliasedExpr: "m.is_self_hosted AS is_self_hosted"},
			"model_ref":      {sqlExpr: "m.model_ref", aliasedExpr: "m.model_ref AS model_ref"},
			"dimensions":     {sqlExpr: "m.dimensions", aliasedExpr: "m.dimensions AS dimensions", sortable: true},
			"endpoint":       {sqlExpr: "m.endpoint", aliasedExpr: "m.endpoint AS endpoint"},
			"validated_at":   {sqlExpr: "m.validated_at", aliasedExpr: "m.validated_at AS validated_at", sortable: true},
			"created_at":     {sqlExpr: "m.created_at", aliasedExpr: "m.created_at AS created_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: embeddingModelJoinCreatedBy,
				sortable:     true,
			},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *EmbeddingModelQuery) EnsureJoins(required int) {
	if missing := required &^ q.joins; missing&embeddingModelJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.created_by")
	}

	q.joins |= required
}

// WithDomainScope keeps the domain's own models and the global ones — reads see
// both, and global models stay read-only because writes scope to the domain
// column alone.
func (q *EmbeddingModelQuery) WithDomainScope(domainID int64) *EmbeddingModelQuery {
	q.builder = q.builder.Where("(m.domain_id = ? OR m.domain_id IS NULL)", domainID)

	return q
}

// WithType keeps models of the given type; empty means any.
func (q *EmbeddingModelQuery) WithType(modelType string) *EmbeddingModelQuery {
	if modelType != "" {
		q.builder = q.builder.Where("m.type = ?", modelType)
	}

	return q
}

// WithSearch keeps models whose name contains the term, case-insensitively.
func (q *EmbeddingModelQuery) WithSearch(term string) *EmbeddingModelQuery {
	if term != "" {
		q.builder = q.builder.Where("m.name ILIKE ?", "%"+escapeLike(term)+"%")
	}

	return q
}

// WithIDs keeps models with the given ids; empty means any.
func (q *EmbeddingModelQuery) WithIDs(ids []int64) *EmbeddingModelQuery {
	if len(ids) > 0 {
		q.builder = q.builder.Where("m.id = ANY(?)", ids)
	}

	return q
}
