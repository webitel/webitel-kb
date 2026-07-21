package model

import "time"

// EmbeddingModel type values.
const (
	ModelTypeEmbedding = "embedding"
	ModelTypeReranker  = "reranker"
)

// EmbeddingModel is a registry entry of an embedding or reranker model. The
// provider credential is deliberately not part of the read model: it is
// write-only and lives in its own store methods.
type EmbeddingModel struct {
	ID int64
	// DomainID is the owning domain; 0 marks a global model available to every
	// domain in read-only mode.
	DomainID     int64
	Type         string
	Name         string
	Provider     string
	IsSelfHosted bool
	// ModelRef is the provider model name.
	ModelRef string
	// Dimensions is the embedding vector size; 0 for rerankers.
	Dimensions int32
	// Endpoint is the self-hosted / Azure / BYOM url.
	Endpoint string
	// ValidatedAt is the time of the last successful test call; zero when the
	// model was never validated.
	ValidatedAt time.Time
	CreatedAt   time.Time
	// CreatedBy is nil when the creator is unknown or no longer exists.
	CreatedBy *Lookup
}

// EmbeddingModelFilter narrows an embedding model listing.
type EmbeddingModelFilter struct {
	// Type keeps only models of this type when non-empty.
	Type string
}
