package queryobject

import "strconv"

// AttachmentFrom is the base relation of the attachment query object: the
// Storage file table, read in place.
const AttachmentFrom = "storage.files m"

// AttachmentChannel is the Storage upload channel of knowledge-base files.
const AttachmentChannel = "knowledgebase"

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
	return []string{"id", "name", "size", "mime", "created_at", "created_by", "source"}
}

// IdentityFields is the id the download link of a file is issued for.
func (q *AttachmentQuery) IdentityFields() []string { return []string{"id"} }

func (q *AttachmentQuery) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id":   {sqlExpr: "m.id", aliasedExpr: "m.id AS id", sortable: true},
			"name": {sqlExpr: "COALESCE(m.view_name, m.name)", aliasedExpr: "COALESCE(m.view_name, m.name) AS name", sortable: true},
			"size": {sqlExpr: "m.size", aliasedExpr: "m.size AS size", sortable: true},
			"mime": {sqlExpr: "m.mime_type", aliasedExpr: "m.mime_type AS mime"},
			"created_at": {sqlExpr: "m.uploaded_at", aliasedExpr: "m.uploaded_at AS created_at", sortable: true},
			"created_by": {
				sqlExpr:      "COALESCE(cb.name, cb.username)",
				aliasedExpr:  "cb.id AS created_by_id, COALESCE(cb.name, cb.username) AS created_by_name",
				requiresJoin: attachmentJoinCreatedBy,
				sortable:     true,
			},
			"source": {sqlExpr: "m.channel", aliasedExpr: "m.channel AS source"},
		}
	}

	return q.meta
}

// EnsureJoins appends the join clauses of newly required bits; repeated calls
// with the same mask add nothing.
func (q *AttachmentQuery) EnsureJoins(required int) {
	missing := required &^ q.joins

	if missing&attachmentJoinCreatedBy != 0 {
		q.builder = q.builder.LeftJoin("directory.wbt_user cb ON cb.id = m.uploaded_by")
	}

	q.joins |= required
}

// WithDomainScope keeps the files of the domain.
func (q *AttachmentQuery) WithDomainScope(domainID int64) *AttachmentQuery {
	q.builder = q.builder.Where("m.domain_id = ?", domainID)

	return q
}

// ArticleReference is the value Storage keeps in uuid for a file uploaded
// under the article: its id, as the upload path carries it.
func ArticleReference(articleID int64) string {
	return strconv.FormatInt(articleID, 10)
}

// WithArticle keeps the files uploaded under the article, and only while the
// article exists in the file's domain: a file referencing an unknown or
// deleted article is not attached to anything.
func (q *AttachmentQuery) WithArticle(articleID int64) *AttachmentQuery {
	q.builder = q.builder.
		Where("m.uuid = ?", ArticleReference(articleID)).
		Where(
			"EXISTS (SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id"+
				" WHERE a.id = ? AND s.domain_id = m.domain_id AND a.deleted_at IS NULL)",
			articleID,
		)

	return q
}

// WithAttached keeps the files of the knowledge-base channel that were not
// removed.
func (q *AttachmentQuery) WithAttached() *AttachmentQuery {
	q.builder = q.builder.
		Where("m.channel = ?", AttachmentChannel).
		Where("m.removed IS NOT TRUE")

	return q
}

// WithSearch keeps files whose name contains the term; empty means any.
func (q *AttachmentQuery) WithSearch(term string) *AttachmentQuery {
	if term != "" {
		q.builder = q.builder.Where("COALESCE(m.view_name, m.name) ILIKE ?", "%"+EscapeLike(term)+"%")
	}

	return q
}

// WithIDs keeps files with the given ids; empty means any.
func (q *AttachmentQuery) WithIDs(ids []int64) *AttachmentQuery {
	if len(ids) > 0 {
		q.builder = q.builder.Where("m.id = ANY(?)", ids)
	}

	return q
}
