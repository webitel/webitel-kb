package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEndpointEmbed(t *testing.T) {
	var gotPath, gotTask string
	var gotTexts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req endpointEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotTask = req.Task
		gotTexts = req.Texts

		_ = json.NewEncoder(w).Encode(endpointEmbedResponse{
			Embeddings: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		})
	}))
	defer srv.Close()

	e := NewEndpoint()
	res, err := e.Embed(context.Background(), EmbedRequest{
		ModelRef: "BAAI/bge-m3",
		Endpoint: srv.URL,
		Texts:    []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if gotPath != "/embed" {
		t.Errorf("path = %q, want /embed", gotPath)
	}
	if gotTask != "document" {
		t.Errorf("task = %q, want document", gotTask)
	}
	if len(gotTexts) != 2 {
		t.Errorf("texts = %v", gotTexts)
	}
	if len(res.Vectors) != 2 || res.Vectors[1][1] != 0.4 {
		t.Errorf("vectors = %v", res.Vectors)
	}
}

func TestEndpointRerank(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(endpointRerankResponse{Scores: []float64{0.9, 0.1}})
	}))
	defer srv.Close()

	e := NewEndpoint()
	res, err := e.Rerank(context.Background(), RerankRequest{
		ModelRef:  "BAAI/bge-reranker",
		Endpoint:  srv.URL,
		Query:     "q",
		Documents: []string{"d1", "d2"},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if gotPath != "/rerank" {
		t.Errorf("path = %q, want /rerank", gotPath)
	}
	if len(res.Scores) != 2 || res.Scores[0] != 0.9 {
		t.Errorf("scores = %v", res.Scores)
	}
}

func TestEndpointRequiresURL(t *testing.T) {
	e := NewEndpoint()
	if _, err := e.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}}); err == nil {
		t.Error("expected error for missing endpoint url")
	}
	if _, err := e.Rerank(context.Background(), RerankRequest{Documents: []string{"x"}}); err == nil {
		t.Error("expected error for missing endpoint url")
	}
}

func TestEndpointCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(endpointEmbedResponse{Embeddings: [][]float32{{0.1}}})
	}))
	defer srv.Close()

	e := NewEndpoint()
	_, err := e.Embed(context.Background(), EmbedRequest{Endpoint: srv.URL, Texts: []string{"a", "b"}})
	if err == nil {
		t.Fatal("expected mismatch error when vectors count != texts count")
	}
}
