package model

import (
	"slices"
	"strconv"
	"time"
)

// EmbeddingModel type values.
const (
	ModelTypeEmbedding = "embedding"
	ModelTypeReranker  = "reranker"
)

// EmbeddingDimensions are the vector sizes kb.chunk_embedding stores. The ANN
// index needs the size in the column type, so every size has its own column
// and an embedding model is registered with one of them.
var EmbeddingDimensions = []int32{768, 1024}

// StoresEmbeddingDimensions reports whether vectors of the size have a column.
func StoresEmbeddingDimensions(dims int32) bool {
	return slices.Contains(EmbeddingDimensions, dims)
}

// EmbeddingColumn names the kb.chunk_embedding column of a vector size.
func EmbeddingColumn(dims int32) string {
	return "embedding_" + strconv.Itoa(int(dims))
}

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
	// Dimensions is the embedding vector size, one of EmbeddingDimensions; 0 for
	// rerankers.
	Dimensions int32
	// Endpoint is the root of a self-hosted service; the route of each call is
	// appended. Cloud providers call their own API and ignore it.
	Endpoint string
	// ValidatedAt is the time of the last successful test call; zero when the
	// model was never validated.
	ValidatedAt time.Time
	CreatedAt   time.Time
	// CreatedBy is nil when the creator is unknown or no longer exists.
	CreatedBy *Lookup
}

// modelInputFields are the input fields an update writes; the api key travels
// separately.
var modelInputFields = []string{
	"type", "name", "provider", "is_self_hosted", "model_ref", "dimensions", "endpoint",
}

// Merge overlays the fields of in named by mask over a copy of the model.
func (m EmbeddingModel) Merge(in *EmbeddingModel, mask []string) *EmbeddingModel {
	merged := m

	for _, field := range maskOrAll(mask, modelInputFields) {
		switch field {
		case "type":
			merged.Type = in.Type
		case "name":
			merged.Name = in.Name
		case "provider":
			merged.Provider = in.Provider
		case "is_self_hosted":
			merged.IsSelfHosted = in.IsSelfHosted
		case "model_ref":
			merged.ModelRef = in.ModelRef
		case "dimensions":
			merged.Dimensions = in.Dimensions
		case "endpoint":
			merged.Endpoint = in.Endpoint
		}
	}

	return &merged
}

// EmbeddingModelFilter narrows an embedding model listing.
type EmbeddingModelFilter struct {
	// Type keeps only models of this type when non-empty.
	Type string
}
