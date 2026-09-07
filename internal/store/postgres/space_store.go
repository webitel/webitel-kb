package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
	"github.com/webitel/webitel-kb/internal/store/util"
)

const spaceTable = "kb.space"

// defaultSpaceSort orders listings when the caller does not.
const defaultSpaceSort = "+name"

type spaceStore struct {
	db Querier
}

var _ store.SpaceStore = (*spaceStore)(nil)

// spaceRecord is the flat scan target of the query object's column aliases;
// nullable columns scan through pointers, the team binding arrives as JSON.
type spaceRecord struct {
	ID                     int64     `db:"id"`
	DomainID               int64     `db:"domain_id"`
	Name                   string    `db:"name"`
	Description            *string   `db:"description"`
	Language               string    `db:"language"`
	EmbeddingModelID       *int64    `db:"embedding_model_id"`
	TargetEmbeddingModelID *int64    `db:"target_embedding_model_id"`
	RerankerModelID        *int64    `db:"reranker_model_id"`
	VectorSearchEnabled    bool      `db:"vector_search_enabled"`
	RerankEnabled          bool      `db:"rerank_enabled"`
	ChunkingStrategy       string    `db:"chunking_strategy"`
	HomeArticleID          *int64    `db:"home_article_id"`
	Teams                  []byte    `db:"teams"`
	CreatedAt              time.Time `db:"created_at"`
	UpdatedAt              time.Time `db:"updated_at"`
	CreatedByID            *int64    `db:"created_by_id"`
	CreatedByName          *string   `db:"created_by_name"`
	UpdatedByID            *int64    `db:"updated_by_id"`
	UpdatedByName          *string   `db:"updated_by_name"`
}

func mapSpace(record *spaceRecord) *model.Space {
	out := &model.Space{
		ID:                  record.ID,
		DomainID:            record.DomainID,
		Name:                record.Name,
		Language:            record.Language,
		VectorSearchEnabled: record.VectorSearchEnabled,
		RerankEnabled:       record.RerankEnabled,
		ChunkingStrategy:    record.ChunkingStrategy,
		CreatedAt:           record.CreatedAt,
		UpdatedAt:           record.UpdatedAt,
	}

	if record.Description != nil {
		out.Description = *record.Description
	}

	if record.EmbeddingModelID != nil {
		out.EmbeddingModelID = *record.EmbeddingModelID
	}

	if record.TargetEmbeddingModelID != nil {
		out.TargetEmbeddingModelID = *record.TargetEmbeddingModelID
	}

	if record.RerankerModelID != nil {
		out.RerankerModelID = *record.RerankerModelID
	}

	if record.HomeArticleID != nil {
		out.HomeArticleID = *record.HomeArticleID
	}

	if len(record.Teams) != 0 {
		// json_agg(json_build_object(...)) output is valid by construction; a
		// shape divergence would surface here as an empty Teams, not an error.
		_ = json.Unmarshal(record.Teams, &out.Teams)
	}

	out.CreatedBy = mapLookup(record.CreatedByID, record.CreatedByName)
	out.UpdatedBy = mapLookup(record.UpdatedByID, record.UpdatedByName)

	return out
}

func mapLookup(id *int64, name *string) *model.Lookup {
	if id == nil {
		return nil
	}

	lookup := &model.Lookup{ID: *id}
	if name != nil {
		lookup.Name = *name
	}

	return lookup
}

func (s *spaceStore) List(ctx context.Context, opts options.Searcher) ([]*model.Space, bool, error) {
	sorts := util.SplitSort(opts.GetSort())
	if len(sorts) == 0 {
		sorts = []string{defaultSpaceSort}
	}

	if !containsSortField(sorts, "id") {
		sorts = append(sorts, "+id")
	}

	sql, args, err := queryobject.NewSpaceQuery(queryobject.SpaceFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
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

	items, err := collectRows(rows, mapSpace)
	if err != nil {
		return nil, false, ParseError(err)
	}

	items, next := util.ResolvePaging(opts.GetSize(), items)

	return items, next, nil
}

func (s *spaceStore) Locate(ctx context.Context, opts options.Searcher) (*model.Space, error) {
	return s.locateSpace(ctx, opts, false)
}

func (s *spaceStore) LocateForUpdate(ctx context.Context, opts options.Searcher) (*model.Space, error) {
	return s.locateSpace(ctx, opts, true)
}

func (s *spaceStore) locateSpace(ctx context.Context, opts options.Searcher, lock bool) (*model.Space, error) {
	if len(opts.GetIDs()) != 1 {
		return nil, errLocateSingleID
	}

	query := queryobject.NewSpaceQuery(queryobject.SpaceFrom).
		WithDomainScope(opts.GetAuthOpts().GetDomainID()).
		WithIDs(opts.GetIDs()).
		WithFields(opts.GetFields())

	if lock {
		query.WithLockForUpdate()
	}

	sql, args, err := query.ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, ParseError(err)
	}

	item, err := collectRow(rows, mapSpace)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}

func (s *spaceStore) Create(ctx context.Context, opts options.Creator, in *model.Space) (*model.Space, error) {
	session := opts.GetAuthOpts()

	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Insert(spaceTable).
		Columns(
			"domain_id", "name", "description", "language",
			"embedding_model_id", "reranker_model_id",
			"vector_search_enabled", "rerank_enabled", "chunking_strategy",
			"home_article_id", "created_by", "updated_by",
		).
		Values(
			session.GetDomainID(), in.Name, nullIfEmpty(in.Description), in.Language,
			nullIfZero(in.EmbeddingModelID), nullIfZero(in.RerankerModelID),
			in.VectorSearchEnabled, in.RerankEnabled, defaultIfEmpty(in.ChunkingStrategy),
			nullIfZero(in.HomeArticleID), nullIfZero(session.GetUserID()), nullIfZero(session.GetUserID()),
		).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *spaceStore) Update(ctx context.Context, opts options.Updator, in *model.Space) (*model.Space, error) {
	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Update(spaceTable).
		Set("name", in.Name).
		Set("description", nullIfEmpty(in.Description)).
		Set("embedding_model_id", nullIfZero(in.EmbeddingModelID)).
		Set("reranker_model_id", nullIfZero(in.RerankerModelID)).
		Set("vector_search_enabled", in.VectorSearchEnabled).
		Set("rerank_enabled", in.RerankEnabled).
		Set("chunking_strategy", defaultIfEmpty(in.ChunkingStrategy)).
		Set("home_article_id", nullIfZero(in.HomeArticleID)).
		Set("updated_at", squirrel.Expr("now()")).
		Set("updated_by", nullIfZero(opts.GetAuthOpts().GetUserID())).
		Where("id = ?", opts.GetID()).
		Where("domain_id = ?", opts.GetAuthOpts().GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

func (s *spaceStore) Delete(ctx context.Context, opts options.Deleter) (*model.Space, error) {
	sql, args, err := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Delete(spaceTable).
		Where("id = ?", opts.GetID()).
		Where("domain_id = ?", opts.GetAuthOpts().GetDomainID()).
		Suffix("RETURNING *").
		ToSql()
	if err != nil {
		return nil, ParseError(err)
	}

	return s.writeReturning(ctx, sql, args, opts.GetFields())
}

// ReplaceTeams rewrites the binding to exactly the given set.
func (s *spaceStore) ReplaceTeams(ctx context.Context, spaceID, domainID, userID int64, teamIDs []int64) error {
	const deleteSQL = `DELETE FROM kb.team_space ts USING kb.space s
		WHERE ts.space_id = s.id AND ts.space_id = $1 AND s.domain_id = $2`

	if _, err := s.db.Exec(ctx, deleteSQL, spaceID, domainID); err != nil {
		return ParseError(err)
	}

	if len(teamIDs) == 0 {
		return nil
	}

	const insertSQL = `INSERT INTO kb.team_space (team_id, space_id, created_by)
		SELECT DISTINCT unnest($1::bigint[]), s.id, $3::bigint FROM kb.space s WHERE s.id = $2 AND s.domain_id = $4`

	if _, err := s.db.Exec(ctx, insertSQL, teamIDs, spaceID, nullIfZero(userID), domainID); err != nil {
		return ParseError(err)
	}

	return nil
}

func (s *spaceStore) HasArticles(ctx context.Context, spaceID, domainID int64) (bool, error) {
	const sql = `SELECT EXISTS (
		SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id
		WHERE a.space_id = $1 AND s.domain_id = $2)`

	var has bool
	if err := s.db.QueryRow(ctx, sql, spaceID, domainID).Scan(&has); err != nil {
		return false, ParseError(err)
	}

	return has, nil
}

// resolveEmbeddingSQL reads a space with its embedding model. Outer join: a
// space without a model still answers.
const resolveEmbeddingSQL = `SELECT
	s.vector_search_enabled,
	coalesce(s.embedding_model_id, 0) AS model_id,
	coalesce(m.provider, '')          AS provider,
	coalesce(m.model_ref, '')         AS model_ref,
	coalesce(m.dimensions, 0)         AS dimensions,
	coalesce(m.endpoint, '')          AS endpoint,
	m.config,
	m.validated_at IS NOT NULL        AS validated
	FROM kb.space s
	LEFT JOIN kb.embedding_model m ON m.id = s.embedding_model_id
	WHERE s.id = $1`

func (s *spaceStore) ResolveEmbedding(ctx context.Context, spaceID int64) (*model.SpaceEmbedding, error) {
	var found model.SpaceEmbedding

	err := s.db.QueryRow(ctx, resolveEmbeddingSQL, spaceID).Scan(
		&found.VectorSearchEnabled,
		&found.ModelID,
		&found.Provider,
		&found.ModelRef,
		&found.Dimensions,
		&found.Endpoint,
		&found.Config,
		&found.Validated,
	)
	if err != nil {
		return nil, ParseError(err)
	}

	return &found, nil
}

// writeReturning reads the written row back via cteReadBack, rendering the
// read through the entity query object over the CTE named m.
func (s *spaceStore) writeReturning(
	ctx context.Context, writeSQL string, writeArgs []any, fields []string,
) (*model.Space, error) {
	readSQL, readArgs, err := queryobject.NewSpaceQuery("m").
		WithFields(fields).
		ToSQL()
	if err != nil {
		return nil, ParseError(err)
	}

	return cteReadBack(ctx, s.db, writeSQL, writeArgs, readSQL, readArgs, mapSpace)
}
