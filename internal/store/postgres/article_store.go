package postgres

import (
	"context"
	"time"

	"github.com/Masterminds/squirrel"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

// defaultArticleSort orders listings when the caller does not.
const defaultArticleSort = "+subject"

// createRootArticleSQL inserts a top-level article; selecting from the
// caller's space enforces the domain scope in the same statement.
const createRootArticleSQL = `INSERT INTO kb.article
	(space_id, parent_id, depth, type, subject, tags, state, created_by, updated_by)
	SELECT s.id, NULL, 1, $3::smallint, $4, $5::text[], $6::smallint, $7::bigint, $7::bigint
	FROM kb.space s WHERE s.id = $1 AND s.domain_id = $2
	RETURNING *`

// createChildArticleSQL inserts a child article, deriving depth from a live
// parent of the same space; the depth constraint backstops the maximum.
const createChildArticleSQL = `INSERT INTO kb.article
	(space_id, parent_id, depth, type, subject, tags, state, created_by, updated_by)
	SELECT s.id, p.id, p.depth + 1, $4::smallint, $5, $6::text[], $7::smallint, $8::bigint, $8::bigint
	FROM kb.space s
	JOIN kb.article p ON p.space_id = s.id AND p.id = $3 AND p.deleted_at IS NULL
	WHERE s.id = $1 AND s.domain_id = $2
	RETURNING *`

// deleteArticleCTEs soft-deletes the article and its subtree. The root is
// written first, carrying the scope and version guards in its own WHERE so a
// concurrent writer cannot slip past them; the subtree walk starts from the
// written root, so a guard miss deletes nothing at all.
const deleteArticleCTEs = `WITH RECURSIVE root AS (
	UPDATE kb.article a SET deleted_at = now(), state = $4, updated_at = now(), updated_by = $5
	FROM kb.space s
	WHERE a.id = $1 AND s.id = a.space_id AND s.domain_id = $2
	  AND a.ver = $3 AND a.deleted_at IS NULL
	RETURNING a.*
), tree AS (
	SELECT root.id FROM root
	UNION ALL
	SELECT c.id FROM kb.article c JOIN tree t ON c.parent_id = t.id
	WHERE c.deleted_at IS NULL
), descendants AS (
	UPDATE kb.article a SET deleted_at = now(), state = $4, updated_at = now(), updated_by = $5
	FROM tree WHERE a.id = tree.id AND a.id <> $1
	RETURNING a.id
), m AS (
	SELECT * FROM root
) `

// articleVerSQL reads the current version of a visible article for the
// conflict/not-found distinction after a guarded write matched nothing.
const articleVerSQL = `SELECT m.ver FROM kb.article m
	JOIN kb.space s ON s.id = m.space_id
	WHERE m.id = $1 AND s.domain_id = $2 AND m.deleted_at IS NULL`

type articleStore struct {
	db Querier
}

var _ store.ArticleStore = (*articleStore)(nil)

// articleRecord is the flat scan target of the query object's column aliases;
// nullable columns scan through pointers.
type articleRecord struct {
	ID                 int64     `db:"id"`
	DomainID           int64     `db:"domain_id"`
	SpaceID            *int64    `db:"space_id"`
	SpaceName          *string   `db:"space_name"`
	ParentID           *int64    `db:"parent_id"`
	Depth              int32     `db:"depth"`
	Type               int32     `db:"type"`
	Subject            string    `db:"subject"`
	Tags               []string  `db:"tags"`
	State              int32     `db:"state"`
	IndexState         int32     `db:"index_state"`
	PublishedVersionID *int64    `db:"published_version_id"`
	Ver                int32     `db:"ver"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
	CreatedByID        *int64    `db:"created_by_id"`
	CreatedByName      *string   `db:"created_by_name"`
	UpdatedByID        *int64    `db:"updated_by_id"`
	UpdatedByName      *string   `db:"updated_by_name"`
}

func mapArticle(record *articleRecord) *model.Article {
	out := &model.Article{
		ID:         record.ID,
		DomainID:   record.DomainID,
		Depth:      record.Depth,
		Type:       record.Type,
		Subject:    record.Subject,
		Tags:       record.Tags,
		State:      record.State,
		IndexState: record.IndexState,
		Ver:        record.Ver,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
	}

	out.Space = mapLookup(record.SpaceID, record.SpaceName)
	if record.SpaceID != nil {
		out.SpaceID = *record.SpaceID
	}

	if record.ParentID != nil {
		out.ParentID = *record.ParentID
	}

	if record.PublishedVersionID != nil {
		out.PublishedVersionID = *record.PublishedVersionID
	}

	out.CreatedBy = mapLookup(record.CreatedByID, record.CreatedByName)
	out.UpdatedBy = mapLookup(record.UpdatedByID, record.UpdatedByName)

	return out
}

func (s *articleStore) List(
	ctx context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	sorts := util.SplitSort(opts.GetSort())
	if len(sorts) == 0 {
		sorts = []string{defaultArticleSort}
	}

	if !containsSortField(sorts, "id") {
		sorts = append(sorts, "+id")
	}

	sql, args, err := queryobject.NewArticleQuery(queryobject.ArticleFrom).
		WithVisible().
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithSearch(opts.GetSearch()).
		WithIDs(opts.GetIDs()).
		WithSpace(filter.SpaceID).
		WithType(filter.Type).
		WithState(filter.State).
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

	items, err := collectRows(rows, mapArticle)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

func (s *articleStore) Locate(ctx context.Context, opts options.Searcher) (*model.Article, error) {
	if len(opts.GetIDs()) != 1 {
		return nil, errLocateSingleID
	}

	sql, args, err := queryobject.NewArticleQuery(queryobject.ArticleFrom).
		WithVisible().
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithIDs(opts.GetIDs()).
		WithFields(opts.GetFields()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	item, err := collectRow(rows, mapArticle)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}

func (s *articleStore) Create(ctx context.Context, opts options.Creator, in *model.Article) (*model.Article, error) {
	session := opts.GetAuthOpts()

	typeCode := in.Type
	if typeCode == 0 {
		typeCode = model.ArticleTypeArticle
	}

	stateCode := in.State
	if stateCode == 0 {
		stateCode = model.ArticleStateDraft
	}

	var (
		writeSQL  string
		writeArgs []any
	)

	if in.ParentID == 0 {
		writeSQL = createRootArticleSQL
		writeArgs = []any{
			in.SpaceID, session.GetDomainID(),
			typeCode, in.Subject, nonNilSlice(in.Tags), stateCode, nullIfZero(session.GetUserID()),
		}
	} else {
		writeSQL = createChildArticleSQL
		writeArgs = []any{
			in.SpaceID, session.GetDomainID(), in.ParentID,
			typeCode, in.Subject, nonNilSlice(in.Tags), stateCode, nullIfZero(session.GetUserID()),
		}
	}

	return s.writeReturning(ctx, writeSQL, writeArgs, opts.GetFields())
}

func (s *articleStore) Update(
	ctx context.Context, opts options.Updator, in *model.Article, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Update("kb.article m").
		Set("subject", in.Subject).
		Set("tags", nonNilSlice(in.Tags)).
		Set("type", in.Type).
		Set("state", in.State).
		Set("updated_at", squirrel.Expr("now()")).
		Set("updated_by", nullIfZero(session.GetUserID())).
		Set("ver", squirrel.Expr("m.ver + 1")).
		Where("m.id = ?", opts.GetID()).
		Where("m.ver = ?", expectedVer).
		Where("m.deleted_at IS NULL").
		Where("EXISTS (SELECT 1 FROM kb.space s WHERE s.id = m.space_id AND s.domain_id = ?)", session.GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	updated, err := s.writeReturning(ctx, sql, args, opts.GetFields())
	if err != nil {
		return nil, s.resolveWriteMiss(ctx, err, opts.GetID(), session.GetDomainID())
	}

	return updated, nil
}

func (s *articleStore) Delete(
	ctx context.Context, opts options.Deleter, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	readSQL, readArgs, err := queryobject.NewArticleQuery("m").
		WithFields(opts.GetFields()).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	writeArgs := []any{
		opts.GetID(), session.GetDomainID(), expectedVer,
		model.ArticleStateInactive, nullIfZero(session.GetUserID()),
	}

	deleted, err := readBackCTEs(ctx, s.db, deleteArticleCTEs, writeArgs, readSQL, readArgs, mapArticle)
	if err != nil {
		return nil, s.resolveWriteMiss(ctx, err, opts.GetID(), session.GetDomainID())
	}

	return deleted, nil
}

// resolveWriteMiss tells a version conflict apart from a genuinely missing
// row after a guarded write matched nothing.
func (s *articleStore) resolveWriteMiss(ctx context.Context, err error, id, domainID int64) error {
	if errors.Code(err) != codes.NotFound {
		return err
	}

	var ver int32
	if scanErr := s.db.QueryRow(ctx, articleVerSQL, id, domainID).Scan(&ver); scanErr != nil {
		return err
	}

	return errors.Aborted(
		"article was changed concurrently",
		errors.WithID("kb.article.version_conflict"),
	)
}

// writeReturning reads the written row back via cteReadBack, rendering the
// read through the entity query object over the CTE named m.
func (s *articleStore) writeReturning(
	ctx context.Context, writeSQL string, writeArgs []any, fields []string,
) (*model.Article, error) {
	readSQL, readArgs, err := queryobject.NewArticleQuery("m").
		WithFields(fields).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	return cteReadBack(ctx, s.db, writeSQL, writeArgs, readSQL, readArgs, mapArticle)
}
