package postgres

import (
	"context"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

const embeddingModelTable = "kb.embedding_model"

// defaultEmbeddingModelSort orders listings when the caller does not.
const defaultEmbeddingModelSort = "+name"

type embeddingModelStore struct {
	db Querier
}

var _ store.EmbeddingModelStore = (*embeddingModelStore)(nil)

// embeddingModelRecord is the flat scan target of the query object's column
// aliases; nullable columns scan through pointers.
type embeddingModelRecord struct {
	ID            int64      `db:"id"`
	DomainID      *int64     `db:"domain_id"`
	Type          string     `db:"type"`
	Name          string     `db:"name"`
	Provider      string     `db:"provider"`
	IsSelfHosted  bool       `db:"is_self_hosted"`
	ModelRef      *string    `db:"model_ref"`
	Dimensions    *int32     `db:"dimensions"`
	Endpoint      *string    `db:"endpoint"`
	ValidatedAt   *time.Time `db:"validated_at"`
	CreatedAt     time.Time  `db:"created_at"`
	CreatedByID   *int64     `db:"created_by_id"`
	CreatedByName *string    `db:"created_by_name"`
}

func mapEmbeddingModel(record *embeddingModelRecord) *model.EmbeddingModel {
	out := &model.EmbeddingModel{
		ID:           record.ID,
		Type:         record.Type,
		Name:         record.Name,
		Provider:     record.Provider,
		IsSelfHosted: record.IsSelfHosted,
		CreatedAt:    record.CreatedAt,
	}

	if record.DomainID != nil {
		out.DomainID = *record.DomainID
	}

	if record.ModelRef != nil {
		out.ModelRef = *record.ModelRef
	}

	if record.Dimensions != nil {
		out.Dimensions = *record.Dimensions
	}

	if record.Endpoint != nil {
		out.Endpoint = *record.Endpoint
	}

	if record.ValidatedAt != nil {
		out.ValidatedAt = *record.ValidatedAt
	}

	if record.CreatedByID != nil {
		out.CreatedBy = &model.Lookup{ID: *record.CreatedByID}
		if record.CreatedByName != nil {
			out.CreatedBy.Name = *record.CreatedByName
		}
	}

	return out
}

func (s *embeddingModelStore) List(
	ctx context.Context, opts options.Searcher, filter model.EmbeddingModelFilter,
) ([]*model.EmbeddingModel, bool, error) {
	sorts := util.SplitSort(opts.GetSort())
	if len(sorts) == 0 {
		sorts = []string{defaultEmbeddingModelSort}
	}

	if !containsSortField(sorts, "id") {
		sorts = append(sorts, "+id")
	}

	sql, args, err := queryobject.NewEmbeddingModelQuery(queryobject.EmbeddingModelFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithType(filter.Type).
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

	items, err := collectRows(rows, mapEmbeddingModel)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

func (s *embeddingModelStore) Locate(ctx context.Context, opts options.Searcher) (*model.EmbeddingModel, error) {
	if len(opts.GetIDs()) != 1 {
		return nil, errLocateSingleID
	}

	sql, args, err := queryobject.NewEmbeddingModelQuery(queryobject.EmbeddingModelFrom).
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

	item, err := collectRow(rows, mapEmbeddingModel)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}

func (s *embeddingModelStore) Create(
	ctx context.Context, opts options.Creator, in *model.EmbeddingModel, config []byte,
) (*model.EmbeddingModel, error) {
	session := opts.GetAuthOpts()

	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Insert(embeddingModelTable).
		Columns(
			"domain_id", "type", "name", "provider", "is_self_hosted",
			"model_ref", "dimensions", "endpoint", "config", "created_by",
		).
		Values(
			session.GetDomainID(), in.Type, in.Name, in.Provider, in.IsSelfHosted,
			nullIfEmpty(in.ModelRef), nullIfZero(in.Dimensions), nullIfEmpty(in.Endpoint),
			config, nullIfZero(session.GetUserID()),
		).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *embeddingModelStore) Update(
	ctx context.Context, opts options.Updator, in *model.EmbeddingModel, config []byte, keepConfig bool,
) (*model.EmbeddingModel, error) {
	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Update(embeddingModelTable).
		Set("name", in.Name).
		Set("provider", in.Provider).
		Set("is_self_hosted", in.IsSelfHosted).
		Set("model_ref", nullIfEmpty(in.ModelRef)).
		Set("dimensions", nullIfZero(in.Dimensions)).
		Set("endpoint", nullIfEmpty(in.Endpoint)).
		// A changed registration must pass validation again.
		Set("validated_at", nil)

	if !keepConfig {
		builder = builder.Set("config", config)
	}

	sql, args, err := builder.
		Where("id = ?", opts.GetID()).
		Where("domain_id = ?", opts.GetAuthOpts().GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *embeddingModelStore) Delete(ctx context.Context, opts options.Deleter) (*model.EmbeddingModel, error) {
	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Delete(embeddingModelTable).
		Where("id = ?", opts.GetID()).
		Where("domain_id = ?", opts.GetAuthOpts().GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *embeddingModelStore) MarkValidated(ctx context.Context, opts options.Updator) (*model.EmbeddingModel, error) {
	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Update(embeddingModelTable).
		Set("validated_at", squirrel.Expr("now()")).
		Where("id = ?", opts.GetID()).
		Where("domain_id = ?", opts.GetAuthOpts().GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *embeddingModelStore) GetConfig(ctx context.Context, id, domainID int64) ([]byte, error) {
	const sql = `SELECT config FROM kb.embedding_model WHERE id = $1 AND (domain_id = $2 OR domain_id IS NULL)`

	var config []byte
	if err := s.db.QueryRow(ctx, sql, id, domainID).Scan(&config); err != nil {
		return nil, ParseError(err)
	}

	return config, nil
}

// writeReturning reads the written row back via cteReadBack, rendering the
// read through the entity query object over the CTE named m.
func (s *embeddingModelStore) writeReturning(
	ctx context.Context, writeSQL string, writeArgs []any, fields []string,
) (*model.EmbeddingModel, error) {
	readSQL, readArgs, err := queryobject.NewEmbeddingModelQuery("m").
		WithFields(fields).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	return cteReadBack(ctx, s.db, writeSQL, writeArgs, readSQL, readArgs, mapEmbeddingModel)
}
