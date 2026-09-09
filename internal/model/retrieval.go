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
