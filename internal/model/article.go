package model

import "time"

// Article type codes.
const (
	ArticleTypeArticle int32 = 1
	ArticleTypeFAQ     int32 = 2
)

// Article lifecycle state codes.
const (
	ArticleStateDraft    int32 = 1
	ArticleStateActive   int32 = 2
	ArticleStateInactive int32 = 3
)

// Article indexing state codes.
const (
	IndexStatePending  int32 = 1
	IndexStateIndexing int32 = 2
	IndexStateIndexed  int32 = 3
	IndexStateFailed   int32 = 4
)

// Article is a knowledge-base article: the content unit of a space, arranged
// in a hierarchy up to five levels deep.
type Article struct {
	ID       int64
	DomainID int64
	// Space owning the article.
	Space *Lookup
	// SpaceID targets the owning space on writes.
	SpaceID int64
	// ParentID is the parent article; 0 for a top-level article.
	ParentID int64
	Depth    int32
	Type     int32
	Subject  string
	Tags     []string
	State    int32
	// IndexState is the re-indexing progress, owned by the indexing pipeline.
	IndexState int32
	// PublishedVersionID points to the live version; 0 before first publish.
	PublishedVersionID int64
	// Ver is the optimistic-lock counter carried by the public etag.
	Ver       int32
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy *Lookup
	UpdatedBy *Lookup
}

// ArticleFilter narrows article listings; zero values disable a criterion.
type ArticleFilter struct {
	SpaceID int64
	Type    int32
	State   int32
}
