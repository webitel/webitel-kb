package embedding

import (
	"context"
	"net/http"
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

	target, err := serviceURL(base, "rerank")
	if err != nil {
		return RerankResult{}, err
	}

	var out rerankResponse
	if err := client.doJSON(ctx, http.MethodPost, target, headers, body, &out); err != nil {
		return RerankResult{}, err
	}

	scores, err := scoresInOrder(out.Results, len(req.Documents))
	if err != nil {
		return RerankResult{}, err
	}

	return RerankResult{Scores: scores}, nil
}

// scoresInOrder puts the scores back in document order.
func scoresInOrder(results []rerankResult, documents int) ([]float64, error) {
	ordered, err := byIndex(results, documents, func(r rerankResult) int { return r.Index })
	if err != nil {
		return nil, err
	}

	scores := make([]float64, documents)
	for i, r := range ordered {
		scores[i] = r.RelevanceScore
	}

	return scores, nil
}
