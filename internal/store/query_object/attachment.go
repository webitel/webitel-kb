package queryobject

// AttachmentFrom is the base relation of the attachment query object: the
// binding of a Storage file to an article.
const AttachmentFrom = "kb.attachment m"

// Join bits of the attachment query object.
const attachmentJoinCreatedBy = 1 << iota

// AttachmentQuery builds SELECTs over the files attached to articles.
type AttachmentQuery struct {
	*baseQueryObject[*AttachmentQuery]

	meta  map[string]fieldMetadata
	joins int
}

// NewAttachmentQuery starts a query over from, normally AttachmentFrom.
func NewAttachmentQuery(from string) *AttachmentQuery {
	q := new(AttachmentQuery)
	q.baseQueryObject = newBaseQueryObject(from, q)

	return q
}

func (q *AttachmentQuery) DefaultFields() []string {
	return []string{"id", "name", "size", "mime", "created_at", "created_by"}
}

// IdentityFields is the id the download link of a file is issued for.
func (q *AttachmentQuery) IdentityFields() []string { return []string{"id"} }

func (q *AttachmentQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":         {sqlExpr: "m.file_id", aliasedExpr: "m.file_id AS id", sortable: true},
			"name":       {sqlExpr: "m.name", aliasedExpr: "m.name AS name", sortable: true},
			"size":       {sqlExpr: "m.size", aliasedExpr: "m.size AS size", sortable: true},
			"mime":       {sqlExpr: "m.mime", aliasedExpr: "m.mime AS mime"},
			"created_at": {sqlExpr: "m.created_at", aliasedExpr: "m.created_at AS created_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: attachmentJoinCreatedBy,
				sortable:     true,
			},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *AttachmentQuery) EnsureJoins(required int) {
	missing := required &^ q.joins

	if missing&attachmentJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.created_by")
	}

	q.joins |= required
}

// WithDomainScope keeps the files of articles the domain owns.
func (q *AttachmentQuery) WithDomainScope(domainID int64) *AttachmentQuery {
	q.builder = q.builder.Where(
		"EXISTS (SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id"+
			" WHERE a.id = m.article_id AND s.domain_id = ? AND a.deleted_at IS NULL)",
		domainID,
	)

	return q
}

// WithArticle keeps the files of the given article.
func (q *AttachmentQuery) WithArticle(articleID int64) *AttachmentQuery {
	q.builder = q.builder.Where("m.article_id = ?", articleID)

	return q
}

// WithSearch keeps files whose name contains the term; empty means any.
func (q *AttachmentQuery) WithSearch(term string) *AttachmentQuery {
	if term != "" {
		q.builder = q.builder.Where("m.name ILIKE ?", "%"+EscapeLike(term)+"%")
	}

	return q
}

// WithIDs keeps files with the given ids; empty means any.
func (q *AttachmentQuery) WithIDs(ids []int64) *AttachmentQuery {
	if len(ids) > 0 {
		q.builder = q.builder.Where("m.file_id = ANY(?)", ids)
	}

	return q
}
