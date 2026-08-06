package model

import "time"

// Space is a knowledge-base space: the top-level container that fixes the
// language and retrieval configuration of its articles.
type Space struct {
	ID          int64
	DomainID    int64
	Name        string
	Description string
	// Language is immutable after creation: it drives the full-text search
	// configuration and, together with the embedding model, the retrieval
	// behavior of the space.
	Language string
	// EmbeddingModelID is the active embedding model; 0 when vector search is
	// not configured. Immutable once set, except the one-way upgrade from unset.
	EmbeddingModelID int64
	// TargetEmbeddingModelID is the pending model-migration target; read-only,
	// never written through the API.
	TargetEmbeddingModelID int64
	// RerankerModelID is the optional cross-encoder reranker; 0 when unset.
	RerankerModelID     int64
	VectorSearchEnabled bool
	RerankEnabled       bool
	ChunkingStrategy    string
	// HomeArticleID is the article shown as the space home page; 0 when unset.
	HomeArticleID int64
	// Teams the space is bound to as default operator visibility; empty when no
	// binding is configured.
	Teams     []Lookup
	CreatedAt time.Time
	UpdatedAt time.Time
	CreatedBy *Lookup
	UpdatedBy *Lookup
}
