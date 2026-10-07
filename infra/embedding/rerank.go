package embedding

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// rerankRequest is the Cohere rerank request, also served by vLLM and
// llama.cpp server.
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

// rerankResponse lists the scored documents, ordered by relevance.
type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerank scores the documents over the Cohere rerank contract at base.
func rerank(
	ctx context.Context, client *httpClient, base string, headers map[string]string, req RerankRequest,
) (RerankResult, error) {
	if len(req.Documents) == 0 {
		return RerankResult{Scores: make([]float64, 0)}, nil
	}

	body := rerankRequest{
		Model:     req.ModelRef,
		Query:     req.Query,
		Documents: req.Documents,
		TopN:      len(req.Documents),
	}

	// A base registered with the path already on it still means the same route.
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/rerank")

	var out rerankResponse
	if err := client.doJSON(ctx, http.MethodPost, endpointURL(base, "rerank"), headers, body, &out); err != nil {
		return RerankResult{}, err
	}

	scores, err := scoresInOrder(out.Results, len(req.Documents))
	if err != nil {
		return RerankResult{}, err
	}

	return RerankResult{Scores: scores}, nil
}

// scoresInOrder puts the scores back in document order; every document must be
// scored exactly once.
func scoresInOrder(results []rerankResult, documents int) ([]float64, error) {
	if len(results) != documents {
		return nil, fmt.Errorf("embedding: reranker scored %d of %d documents", len(results), documents)
	}

	scores := make([]float64, documents)
	seen := make([]bool, documents)

	for _, r := range results {
		if r.Index < 0 || r.Index >= documents {
			return nil, fmt.Errorf("embedding: reranker scored unknown document %d", r.Index)
		}

		if seen[r.Index] {
			return nil, fmt.Errorf("embedding: reranker scored document %d twice", r.Index)
		}

		seen[r.Index] = true
		scores[r.Index] = r.RelevanceScore
	}

	return scores, nil
}
