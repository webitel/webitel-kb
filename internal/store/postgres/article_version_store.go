package postgres

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

// defaultVersionSort lists the history newest first.
const defaultVersionSort = "-version_number"

// createVersionSQL appends a version to an article of the caller's domain.
// The number continues the article's own sequence, the search vector is built
// under the configuration of the space language, and a restore may only point
// at a version of the same article.
const createVersionSQL = `INSERT INTO kb.article_version
	(article_id, version_number, subject, body_rich_text, body_markdown, body_plain, tsv, restored_from, notes, created_by)
	SELECT a.id,
	       (SELECT COALESCE(max(v.version_number), 0) + 1 FROM kb.article_version v WHERE v.article_id = a.id),
	       $3, $4::jsonb, $5, $6, to_tsvector($7::regconfig, $6), $8::bigint, $9, $10::bigint
	FROM kb.article a JOIN kb.space s ON s.id = a.space_id
	WHERE a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL
	  AND ($8::bigint IS NULL OR EXISTS (
	      SELECT 1 FROM kb.article_version src WHERE src.id = $8::bigint AND src.article_id = a.id))
	RETURNING *`

type articleVersionStore struct {
	db Querier
}

var _ store.ArticleVersionStore = (*articleVersionStore)(nil)

// articleVersionRecord is the flat scan target of the query object's column
// aliases; nullable columns scan through pointers.
type articleVersionRecord struct {
	ID            int64     `db:"id"`
	ArticleID     int64     `db:"article_id"`
	VersionNumber int32     `db:"version_number"`
	Subject       string    `db:"subject"`
	BodyRichText  []byte    `db:"body_rich_text"`
	BodyMarkdown  string    `db:"body_markdown"`
	BodyPlain     string    `db:"body_plain"`
	RestoredFrom  *int64    `db:"restored_from"`
	Notes         *string   `db:"notes"`
	CreatedAt     time.Time `db:"created_at"`
	CreatedByID   *int64    `db:"created_by_id"`
	CreatedByName *string   `db:"created_by_name"`
}

func mapArticleVersion(record *articleVersionRecord) *model.ArticleVersion {
	out := &model.ArticleVersion{
		ID:            record.ID,
		ArticleID:     record.ArticleID,
		VersionNumber: record.VersionNumber,
		Subject:       record.Subject,
		BodyRichText:  record.BodyRichText,
		BodyMarkdown:  record.BodyMarkdown,
		BodyPlain:     record.BodyPlain,
		CreatedAt:     record.CreatedAt,
	}

	if record.RestoredFrom != nil {
		out.RestoredFrom = *record.RestoredFrom
	}

	if record.Notes != nil {
		out.Notes = *record.Notes
	}

	out.CreatedBy = mapLookup(record.CreatedByID, record.CreatedByName)

	return out
}

func (s *articleVersionStore) List(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.ArticleVersion, bool, error) {
	sorts := util.SplitSort(opts.GetSort())
	if len(sorts) == 0 {
		sorts = []string{defaultVersionSort}
	}

	if !containsSortField(sorts, "version_number") {
		sorts = append(sorts, "-version_number")
	}

	sql, args, err := queryobject.NewArticleVersionQuery(queryobject.ArticleVersionFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithArticle(articleID).
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

	items, err := collectRows(rows, mapArticleVersion)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

func (s *articleVersionStore) Locate(
	ctx context.Context, opts options.Searcher, articleID int64, number int32,
) (*model.ArticleVersion, error) {
	sql, args, err := queryobject.NewArticleVersionQuery(queryobject.ArticleVersionFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithArticle(articleID).
		WithNumber(number).
		WithFields(opts.GetFields()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	item, err := collectRow(rows, mapArticleVersion)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}

func (s *articleVersionStore) Create(
	ctx context.Context, opts options.Creator, in *model.ArticleVersion, textSearchConfig string,
) (*model.ArticleVersion, error) {
	session := opts.GetAuthOpts()

	readSQL, readArgs, err := queryobject.NewArticleVersionQuery("m").
		WithFields(opts.GetFields()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	created, err := cteReadBack(ctx, s.db, createVersionSQL, []any{
		in.ArticleID, session.GetDomainID(), in.Subject, in.BodyRichText,
		in.BodyMarkdown, in.BodyPlain, textSearchConfig,
		nullIfZero(in.RestoredFrom), nullIfEmpty(in.Notes), nullIfZero(session.GetUserID()),
	}, readSQL, readArgs, mapArticleVersion)
	if err != nil {
		return nil, versionWriteError(err)
	}

	return created, nil
}

// versionWriteError reports a lost race for the next version number as a
// retryable conflict rather than a duplicate entity.
func versionWriteError(err error) error {
	if errors.Code(err) != codes.AlreadyExists {
		return err
	}

	var pgErr *pgconn.PgError
	if !stderrors.As(err, &pgErr) || pgErr.Code != pgerrcode.UniqueViolation {
		return err
	}

	return errors.Aborted(
		"article was versioned concurrently",
		errors.WithID("kb.article.version_race"),
	)
}
