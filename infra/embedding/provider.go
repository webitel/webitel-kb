// Package embedding provides HTTP clients for embedding and reranker model
// providers used to validate registered models and, later, to embed queries at
// retrieval time.
package embedding

import (
	"context"
	"errors"
)

// ErrUnsupported reports that a provider or operation is not implemented.
var ErrUnsupported = errors.New("embedding: unsupported provider or operation")

// TaskType selects the embedding purpose. Some providers (e.g. Gemini) produce
// asymmetric vectors and require the document/query distinction.
type TaskType uint8

const (
	// TaskDocument embeds content to be stored and searched (the default).
	TaskDocument TaskType = iota
	// TaskQuery embeds a search query.
	TaskQuery
)

// EmbedRequest carries everything a provider needs to embed one or more texts.
type EmbedRequest struct {
	// ModelRef is the provider model name (cloud) or model id (self-hosted).
	ModelRef string
	// APIKey is the cloud provider credential; empty for self-hosted models.
	APIKey string
	// Endpoint is the self-hosted/BYOM service URL; empty for cloud defaults.
	Endpoint string
	// Dimensions is the desired output dimension; 0 uses the provider default.
	Dimensions int
	// Task is the embedding purpose (document or query).
	Task TaskType
	// Texts are the inputs to embed, one vector is returned per text in order.
	Texts []string
}

// EmbedResult holds one vector per input text, in request order.
type EmbedResult struct {
	Vectors [][]float32
}

// RerankRequest carries a query and candidate documents to score.
type RerankRequest struct {
	ModelRef  string
	APIKey    string
	Endpoint  string
	Query     string
	Documents []string
}

// RerankResult holds one relevance score per document, in request order.
type RerankResult struct {
	Scores []float64
}

// Provider embeds texts and reranks documents for a single backend.
type Provider interface {
	Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error)
	Rerank(ctx context.Context, req RerankRequest) (RerankResult, error)
}
