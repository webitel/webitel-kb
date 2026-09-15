package postgres

import (
	"context"
	"time"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

// defaultAttachmentSort lists the newest upload first.
const defaultAttachmentSort = "-created_at"

// deleteAttachmentSQL removes a file the way Storage itself does: the row is
// flagged and the Storage cleanup job disposes of the object later. The file
// must belong to the domain and to an article that still exists there, and be
// a knowledge-base upload.
const deleteAttachmentSQL = `UPDATE storage.files f SET removed = true
	WHERE f.id = $1 AND f.domain_id = $2 AND f.uuid = $3 AND f.channel = $4 AND f.removed IS NOT TRUE
	  AND EXISTS (SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id
	      WHERE a.id = $5 AND s.domain_id = f.domain_id AND a.deleted_at IS NULL)
	RETURNING f.*`

type attachmentStore struct {
	db Querier
}

var _ store.AttachmentStore = (*attachmentStore)(nil)

// attachmentRecord is the flat scan target of the query object's column
// aliases; nullable columns scan through pointers.
type attachmentRecord struct {
	ID            int64      `db:"id"`
	Name          string     `db:"name"`
	Size          int64      `db:"size"`
	Mime          *string    `db:"mime"`
	CreatedAt     *time.Time `db:"created_at"`
	CreatedByID   *int64     `db:"created_by_id"`
	CreatedByName *string    `db:"created_by_name"`
	Source        *string    `db:"source"`
}

func mapAttachment(record *attachmentRecord) *model.Attachment {
	out := &model.Attachment{
		ID:   record.ID,
		Name: record.Name,
		Size: record.Size,
	}

	if record.Mime != nil {
		out.Mime = *record.Mime
	}

	if record.CreatedAt != nil {
		out.CreatedAt = *record.CreatedAt
	}

	if record.Source != nil {
		out.Source = *record.Source
	}

	out.CreatedBy = mapLookup(record.CreatedByID, record.CreatedByName)

	return out
}

func (s *attachmentStore) List(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.Attachment, bool, error) {
	sorts := util.SplitSort(opts.GetSort())
	if len(sorts) == 0 {
		sorts = []string{defaultAttachmentSort}
	}

	if !containsSortField(sorts, "id") {
		sorts = append(sorts, "+id")
	}

	sql, args, err := queryobject.NewAttachmentQuery(queryobject.AttachmentFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithArticle(articleID).
		WithAttached().
		WithSearch(opts.GetSearch()).
		WithIDs(opts.GetIDs()).
		WithFields(opts.GetFields()).
		WithSort(sorts...).
		WithPaging(opts.GetSize(), opts.GetPage()).
		ToSQL()
	if err != nil {
		return nil, false, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, err := collectRows(rows, mapAttachment)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

func (s *attachmentStore) Delete(
	ctx context.Context, opts options.Deleter, articleID int64,
) (*model.Attachment, error) {
	readSQL, readArgs, err := queryobject.NewAttachmentQuery("m").
		WithFields(opts.GetFields()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	return cteReadBack(ctx, s.db, deleteAttachmentSQL, []any{
		opts.GetID(), opts.GetAuthOpts().GetDomainID(),
		queryobject.ArticleReference(articleID), queryobject.AttachmentChannel, articleID,
	}, readSQL, readArgs, mapAttachment)
}
