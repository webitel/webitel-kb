package queryobject

import (
	"github.com/webitel/webitel-kb/internal/store/util"
)

// ArticleFrom is the base relation of the article query object.
const ArticleFrom = "kb.article m"

// Join bits of the article query object.
const (
	articleJoinSpace = 1 << iota
	articleJoinCreatedBy
	articleJoinUpdatedBy
)

// articleIdentityFields are the columns every article projection carries.
var articleIdentityFields = []string{"id", "ver"}

// ArticleQuery builds SELECTs over knowledge-base articles.
type ArticleQuery struct {
	*baseQueryObject[*ArticleQuery]

	meta  map[string]fieldMetadata
	joins int
}

// NewArticleQuery starts a query over from, normally ArticleFrom.
func NewArticleQuery(from string) *ArticleQuery {
	q := new(ArticleQuery)
	q.baseQueryObject = newBaseQueryObject(from, q)

	return q
}

// WithFields selects the caller's fields plus the identity the etag is built
// from. An empty selection keeps the defaults.
func (q *ArticleQuery) WithFields(fields []string) *ArticleQuery {
	if len(fields) == 0 {
		return q
	}

	asked := make([]string, 0, len(fields)+len(articleIdentityFields))
	asked = append(asked, fields...)
	asked = append(asked, articleIdentityFields...)

	return q.baseQueryObject.WithFields(util.DeduplicateFields(asked))
}

func (q *ArticleQuery) DefaultFields() []string {
	return []string{
		"id", "domain_id", "space", "parent_id", "depth", "type", "subject",
		"tags", "state", "index_state", "published_version_id", "ver",
		"created_at", "updated_at", "created_by", "updated_by",
	}
}

func (q *ArticleQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":        {sqlExpr: "m.id", aliasedExpr: "m.id AS id", sortable: true},
			"domain_id": {sqlExpr: "s.domain_id", aliasedExpr: "s.domain_id AS domain_id", requiresJoin: articleJoinSpace},
			"space": {
				sqlExpr:      "s.name",
				aliasedExpr:  "s.id AS space_id, s.name AS space_name",
				requiresJoin: articleJoinSpace,
			},
			"parent_id":            {sqlExpr: "m.parent_id", aliasedExpr: "m.parent_id AS parent_id"},
			"depth":                {sqlExpr: "m.depth", aliasedExpr: "m.depth AS depth", sortable: true},
			"type":                 {sqlExpr: "m.type", aliasedExpr: "m.type AS type"},
			"subject":              {sqlExpr: "m.subject", aliasedExpr: "m.subject AS subject", sortable: true},
			"tags":                 {sqlExpr: "m.tags", aliasedExpr: "m.tags AS tags"},
			"state":                {sqlExpr: "m.state", aliasedExpr: "m.state AS state"},
			"index_state":          {sqlExpr: "m.index_state", aliasedExpr: "m.index_state AS index_state"},
			"published_version_id": {sqlExpr: "m.published_version_id", aliasedExpr: "m.published_version_id AS published_version_id"},
			"ver":                  {sqlExpr: "m.ver", aliasedExpr: "m.ver AS ver"},
			"created_at":           {sqlExpr: "m.created_at", aliasedExpr: "m.created_at AS created_at", sortable: true},
			"updated_at":           {sqlExpr: "m.updated_at", aliasedExpr: "m.updated_at AS updated_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: articleJoinCreatedBy,
				sortable:     true,
			},
			"updated_by": {
				sqlExpr:      "COALESCE(ub.name, ub.username)",
				aliasedExpr:  "ub.id AS updated_by_id, COALESCE(ub.name, ub.username) AS updated_by_name",
				requiresJoin: articleJoinUpdatedBy,
				sortable:     true,
			},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *ArticleQuery) EnsureJoins(required int) {
	missing := required &^ q.joins

	if missing&articleJoinSpace != 0 {
		q.builder = q.builder.Join("kb.space s ON s.id = m.space_id")
	}

	if missing&articleJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.created_by")
	}

	if missing&articleJoinUpdatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user ub ON ub.id = m.updated_by")
	}

	q.joins |= required
}

// WithVisible hides soft-deleted articles; every read applies it, write
// read-backs do not.
func (q *ArticleQuery) WithVisible() *ArticleQuery {
	q.builder = q.builder.Where("m.deleted_at IS NULL")

	return q
}

// WithDomainScope keeps the articles of spaces owned by the domain.
func (q *ArticleQuery) WithDomainScope(domainID int64) *ArticleQuery {
	q.EnsureJoins(articleJoinSpace)
	q.builder = q.builder.Where("s.domain_id = ?", domainID)

	return q
}

// WithLockForUpdate locks the selected article rows until the transaction
// ends. Locks only the article relation: a locking clause cannot cover the
// nullable side of the user joins.
func (q *ArticleQuery) WithLockForUpdate() *ArticleQuery {
	q.builder = q.builder.Suffix("FOR UPDATE OF m")

	return q
}

// WithParent keeps the children of the given parent: nil means any parent, a
// zero value means top-level articles, otherwise the children of that id.
func (q *ArticleQuery) WithParent(parentID *int64) *ArticleQuery {
	switch {
	case parentID == nil:
	case *parentID == 0:
		q.builder = q.builder.Where("m.parent_id IS NULL")
	default:
		q.builder = q.builder.Where("m.parent_id = ?", *parentID)
	}

	return q
}

// WithTags keeps articles carrying the given tags: all of them when matchAll,
// at least one otherwise. An empty list means any.
func (q *ArticleQuery) WithTags(tags []string, matchAll bool) *ArticleQuery {
	if len(tags) == 0 {
		return q
	}

	if matchAll {
		q.builder = q.builder.Where("m.tags @> ?", tags)

		return q
	}

	q.builder = q.builder.Where("m.tags && ?", tags)

	return q
}

// WithSpace keeps the articles of the given space; 0 means any.
func (q *ArticleQuery) WithSpace(spaceID int64) *ArticleQuery {
	if spaceID > 0 {
		q.builder = q.builder.Where("m.space_id = ?", spaceID)
	}

	return q
}

// WithType keeps articles of the given type code; 0 means any.
func (q *ArticleQuery) WithType(code int32) *ArticleQuery {
	if code > 0 {
		q.builder = q.builder.Where("m.type = ?", code)
	}

	return q
}

// WithState keeps articles in the given state code; 0 means any.
func (q *ArticleQuery) WithState(code int32) *ArticleQuery {
	if code > 0 {
		q.builder = q.builder.Where("m.state = ?", code)
	}

	return q
}

// WithSearch keeps articles whose subject contains the term,
// case-insensitively.
func (q *ArticleQuery) WithSearch(term string) *ArticleQuery {
	if term != "" {
		q.builder = q.builder.Where("m.subject ILIKE ?", "%"+EscapeLike(term)+"%")
	}

	return q
}

// WithIDs keeps articles with the given ids; empty means any.
func (q *ArticleQuery) WithIDs(ids []int64) *ArticleQuery {
	if len(ids) > 0 {
		q.builder = q.builder.Where("m.id = ANY(?)", ids)
	}

	return q
}
