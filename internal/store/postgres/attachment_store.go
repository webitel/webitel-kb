package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

// defaultAttachmentSort lists the newest binding first.
const defaultAttachmentSort = "-created_at"

// attachSQL binds a file to an article; selecting from the article of the
// caller's domain enforces the scope in the same statement. Binding a file
// twice refreshes its metadata instead of failing.
const attachSQL = `INSERT INTO kb.attachment (article_id, file_id, name, mime, size, created_by)
	SELECT a.id, $3::bigint, $4::text, $5::text, $6::bigint, $7::bigint
	FROM kb.article a JOIN kb.space s ON s.id = a.space_id
	WHERE a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL
	ON CONFLICT (article_id, file_id) DO UPDATE
		SET name = EXCLUDED.name, mime = EXCLUDED.mime, size = EXCLUDED.size
	RETURNING *`

// unboundFilesSQL keeps the files no article binds any more; Storage keeps the
// bytes while one does.
const unboundFilesSQL = `SELECT f.id FROM unnest($1::bigint[]) AS f(id)
	WHERE NOT EXISTS (SELECT 1 FROM kb.attachment m WHERE m.file_id = f.id)
	ORDER BY f.id`

// deleteAttachmentSQL unbinds a file from an article of the caller's domain.
// The file itself is Storage's to remove.
const deleteAttachmentSQL = `DELETE FROM kb.attachment m
	USING kb.article a JOIN kb.space s ON s.id = a.space_id
	WHERE m.article_id = $1 AND m.file_id = $2
	  AND a.id = m.article_id AND s.domain_id = $3 AND a.deleted_at IS NULL
	RETURNING m.*`

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

func (s *attachmentStore) Attach(
	ctx context.Context, opts options.Creator, articleID int64, in *model.Attachment,
) (*model.Attachment, error) {
	session := opts.GetAuthOpts()

	return s.writeReturning(ctx, attachSQL, []any{
		articleID, session.GetDomainID(),
		in.ID, in.Name, nullIfEmpty(in.Mime), in.Size, nullIfZero(session.GetUserID()),
	}, opts.GetFields())
}

func (s *attachmentStore) Delete(
	ctx context.Context, opts options.Deleter, articleID int64,
) (*model.Attachment, error) {
	return s.writeReturning(ctx, deleteAttachmentSQL, []any{
		articleID, opts.GetID(), opts.GetAuthOpts().GetDomainID(),
	}, opts.GetFields())
}

// Unbound asks nothing for an empty set.
func (s *attachmentStore) Unbound(ctx context.Context, fileIDs []int64) ([]int64, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, unboundFilesSQL, fileIDs)
	if err != nil {
		return nil, ParseError(err)
	}

	unbound, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, ParseError(err)
	}

	return unbound, nil
}

// writeReturning reads the written row back via cteReadBack, rendering the
// read through the entity query object over the CTE named m.
func (s *attachmentStore) writeReturning(
	ctx context.Context, writeSQL string, writeArgs []any, fields []string,
) (*model.Attachment, error) {
	readSQL, readArgs, err := queryobject.NewAttachmentQuery("m").
		WithFields(fields).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	return cteReadBack(ctx, s.db, writeSQL, writeArgs, readSQL, readArgs, mapAttachment)
}
