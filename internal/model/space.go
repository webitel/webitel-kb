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

// SpaceEmbedding is the embedding model a space is indexed with, for the indexer.
type SpaceEmbedding struct {
	SpaceID int64
	// VectorSearchEnabled is false when the space is not embedded; the rest is then unset.
	VectorSearchEnabled bool
	// ModelID is 0 when the space has no model.
	ModelID    int64
	Provider   string
	ModelRef   string
	Dimensions int32
	Endpoint   string
	// Config is the stored credential, encrypted.
	Config []byte
	// APIKey is the opened credential; empty for self-hosted providers. Secret.
	APIKey string
	// Validated reports the registration test call; not enforced.
	Validated bool
}

// SpaceReranker is the cross-encoder a space reranks with.
type SpaceReranker struct {
	SpaceID int64
	// Enabled is false when the space does not rerank; the rest is then unset.
	Enabled  bool
	ModelID  int64
	Provider string
	ModelRef string
	Endpoint string
	// Config is the stored credential, encrypted; empty for self-hosted providers.
	Config []byte
}

// spaceInputFields are the input fields an update writes; team_ids travel
// separately.
var spaceInputFields = []string{
	"name", "description", "language", "embedding_model_id", "reranker_model_id",
	"vector_search_enabled", "rerank_enabled", "chunking_strategy", "home_article_id",
}

// Merge overlays the fields of in named by mask over a copy of the space.
func (s Space) Merge(in *Space, mask []string) *Space {
	merged := s

	for _, field := range maskOrAll(mask, spaceInputFields) {
		switch field {
		case "name":
			merged.Name = in.Name
		case "description":
			merged.Description = in.Description
		case "language":
			if in.Language != "" {
				merged.Language = in.Language
			}
		case "embedding_model_id":
			merged.EmbeddingModelID = in.EmbeddingModelID
		case "reranker_model_id":
			merged.RerankerModelID = in.RerankerModelID
		case "vector_search_enabled":
			merged.VectorSearchEnabled = in.VectorSearchEnabled
		case "rerank_enabled":
			merged.RerankEnabled = in.RerankEnabled
		case "chunking_strategy":
			merged.ChunkingStrategy = in.ChunkingStrategy
		case "home_article_id":
			merged.HomeArticleID = in.HomeArticleID
		}
	}

	return &merged
}

// maskOrAll returns mask, or all when mask is empty.
func maskOrAll(mask, all []string) []string {
	if len(mask) == 0 {
		return all
	}

	return mask
}
