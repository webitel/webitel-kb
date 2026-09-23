package model

import "time"

// EmbeddingModel type values.
const (
	ModelTypeEmbedding = "embedding"
	ModelTypeReranker  = "reranker"
)

// EmbeddingStorageDimensions is the vector size of kb.chunk_embedding. The ANN
// index needs the size in the column type, so one installation stores exactly
// one dimension and every embedding model is registered with it.
const EmbeddingStorageDimensions int32 = 768

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

// modelInputFields are the input fields an update writes; the dimensions are
// fixed by the schema and the api key travels separately.
var modelInputFields = []string{
	"type", "name", "provider", "is_self_hosted", "model_ref", "endpoint",
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
