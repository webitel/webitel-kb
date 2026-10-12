package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiEmbed(t *testing.T) {
	var (
		gotPath, gotKey, gotTaskType string
		gotDim                       int
		gotModel                     string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")

		var req geminiBatchRequest

		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Requests) > 0 {
			gotTaskType = req.Requests[0].TaskType
			gotDim = req.Requests[0].OutputDimensionality
			gotModel = req.Requests[0].Model
		}

		// Return a non-normalized 4-dim vector per request.
		resp := geminiBatchResponse{}
		for range req.Requests {
			resp.Embeddings = append(resp.Embeddings, struct {
				Values []float32 `json:"values"`
			}{Values: []float32{3, 0, 4, 0}})
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewGemini(WithGeminiBaseURL(srv.URL))

	res, err := g.Embed(context.Background(), EmbedRequest{
		ModelRef:   "gemini-embedding-001",
		APIKey:     "secret-key",
		Dimensions: 4,
		Task:       TaskQuery,
		Texts:      []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if gotPath != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
		t.Errorf("path = %q", gotPath)
	}

	if gotModel != "models/gemini-embedding-001" {
		t.Errorf("model = %q, want models/ prefix", gotModel)
	}

	if gotKey != "secret-key" {
		t.Errorf("api key header = %q", gotKey)
	}

	if gotTaskType != "RETRIEVAL_QUERY" {
		t.Errorf("taskType = %q, want RETRIEVAL_QUERY", gotTaskType)
	}

	if gotDim != 4 {
		t.Errorf("outputDimensionality = %d, want 4", gotDim)
	}

	if len(res.Vectors) != 2 {
		t.Fatalf("got %d vectors, want 2", len(res.Vectors))
	}

	// 4 < 3072 → must be L2-normalized: (3,0,4,0) has norm 5 → (0.6,0,0.8,0).
	got := res.Vectors[0]
	if math.Abs(float64(got[0])-0.6) > 1e-6 || math.Abs(float64(got[2])-0.8) > 1e-6 {
		t.Errorf("vector not normalized: %v", got)
	}
}

func TestGeminiEmbedErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	g := NewGemini(WithGeminiBaseURL(srv.URL))
	_, err := g.Embed(context.Background(), EmbedRequest{ModelRef: "m", Texts: []string{"x"}})

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want APIError 401, got %v", err)
	}
}

func TestGeminiRerankUnsupported(t *testing.T) {
	g := NewGemini()

	_, err := g.Rerank(context.Background(), RerankRequest{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestGeminiEmbedEmpty(t *testing.T) {
	g := NewGemini()

	res, err := g.Embed(context.Background(), EmbedRequest{Texts: nil})
	if err != nil || len(res.Vectors) != 0 {
		t.Fatalf("empty embed: vectors=%v err=%v", res.Vectors, err)
	}
}

func TestGeminiTask(t *testing.T) {
	tests := []struct {
		name         string
		modelRef     string
		task         TaskType
		wantTaskType string
		wantText     string
	}{
		{
			name:         "001 query by task type",
			modelRef:     "gemini-embedding-001",
			task:         TaskQuery,
			wantTaskType: "RETRIEVAL_QUERY",
			wantText:     "x",
		},
		{
			name:         "001 document by task type",
			modelRef:     "models/gemini-embedding-001",
			task:         TaskDocument,
			wantTaskType: "RETRIEVAL_DOCUMENT",
			wantText:     "x",
		},
		{
			name:     "2 query by prefix",
			modelRef: "gemini-embedding-2",
			task:     TaskQuery,
			wantText: "task: search result | query: x",
		},
		{
			name:     "2 document by prefix",
			modelRef: "models/gemini-embedding-2-preview",
			task:     TaskDocument,
			wantText: "title: none | text: x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got geminiBatchRequest

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&got)
				_, _ = w.Write([]byte(`{"embeddings":[{"values":[1,0]}]}`))
			}))
			defer srv.Close()

			if _, err := NewGemini(WithGeminiBaseURL(srv.URL)).Embed(context.Background(), EmbedRequest{
				ModelRef: tt.modelRef, APIKey: "k", Task: tt.task, Texts: []string{"x"},
			}); err != nil {
				t.Fatalf("Embed: %v", err)
			}

			req := got.Requests[0]
			if req.TaskType != tt.wantTaskType || req.Content.Parts[0].Text != tt.wantText {
				t.Errorf("taskType = %q, text = %q; want %q, %q",
					req.TaskType, req.Content.Parts[0].Text, tt.wantTaskType, tt.wantText)
			}
		})
	}
}
