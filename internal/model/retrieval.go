package model

import "time"

// ArticleSummary is what the retrieval endpoints return about an article.
type ArticleSummary struct {
	ID       int64
	SpaceID  int64
	ParentID int64
	Depth    int32
	Type     int32
	Subject  string
	// Snippet is a short excerpt of the body.
	Snippet string
	Tags    []string
	State   int32
	// Body is the whole text of an FAQ in a menu answer.
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SearchFilter narrows a full-text search.
type SearchFilter struct {
	// SpaceIDs keeps the articles of those spaces.
	SpaceIDs []int64
	// Tags keeps articles carrying them.
	Tags         []string
	TagsMatchAll bool
}

// SemanticQuery is a hybrid search request as the service takes it.
type SemanticQuery struct {
	Query        string
	SpaceIDs     []int64
	Tags         []string
	TagsMatchAll bool
	// TopK is how many chunks to return; 0 asks for the default.
	TopK             int
	IncludeCitations bool
}

// ModelVector is the query embedded under one model, with the spaces that model serves.
type ModelVector struct {
	ModelID  int64
	SpaceIDs []int64
	Vector   []float32
}

// HybridQuery is what the store fuses: the lexical term and one vector per model.
type HybridQuery struct {
	Term    string
	Filter  SearchFilter
	Vectors []ModelVector
	TopK    int
}

// ChunkHit is one fused result.
type ChunkHit struct {
	ID         int64
	ArticleID  int64
	VersionID  int64
	ChunkIndex int32
	Subject    string
	Content    string
	// Score is the reciprocal rank fusion score.
	Score float64
}

// Citation points a consumer at the article a hit came from.
type Citation struct {
	ArticleID int64
	Title     string
	Snippet   string
	// URL is empty until the article route is settled.
	URL string
}
