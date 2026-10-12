package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestCohereEmbed(t *testing.T) {
	tests := []struct {
		name          string
		modelRef      string
		task          TaskType
		wantInputType string
		wantDimension int
	}{
		{
			name:          "document of a sized model",
			modelRef:      "embed-v4.0",
			task:          TaskDocument,
			wantInputType: "search_document",
			wantDimension: 1024,
		},
		{
			name:          "query of a sized model",
			modelRef:      "embed-v5.0-fast",
			task:          TaskQuery,
			wantInputType: "search_query",
			wantDimension: 1024,
		},
		{
			name:          "fixed-size model is not asked for a size",
			modelRef:      "embed-multilingual-v3.0",
			task:          TaskDocument,
			wantInputType: "search_document",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				gotPath, gotAuth string
				got              cohereEmbedRequest
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
				_ = json.NewDecoder(r.Body).Decode(&got)
				_, _ = w.Write([]byte(`{"embeddings":{"float":[[0,0],[1,1]]}}`))
			}))
			defer srv.Close()

			res, err := NewCohere(WithCohereBaseURL(srv.URL)).Embed(context.Background(), EmbedRequest{
				ModelRef:   tt.modelRef,
				APIKey:     "secret",
				Endpoint:   "http://elsewhere.invalid",
				Dimensions: 1024,
				Task:       tt.task,
				Texts:      []string{"a", "b"},
			})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}

			if gotPath != "/embed" || gotAuth != "Bearer secret" {
				t.Errorf("path = %q, authorization = %q", gotPath, gotAuth)
			}

			if got.Model != tt.modelRef || !slices.Equal(got.Texts, []string{"a", "b"}) ||
				got.InputType != tt.wantInputType || !slices.Equal(got.EmbeddingTypes, []string{"float"}) ||
				got.OutputDimension != tt.wantDimension {
				t.Errorf("body = %+v", got)
			}

			if len(res.Vectors) != 2 || res.Vectors[1][0] != 1 {
				t.Errorf("vectors = %v, want them in input order", res.Vectors)
			}
		})
	}
}

func TestCohereEmbedErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		apiErr bool
	}{
		{name: "rejected key", status: http.StatusUnauthorized, body: `{"message":"invalid api token"}`, apiErr: true},
		{name: "missing vector", status: http.StatusOK, body: `{"embeddings":{"float":[[1]]}}`},
		{name: "no float vectors", status: http.StatusOK, body: `{"embeddings":{"int8":[[1],[2]]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := NewCohere(WithCohereBaseURL(srv.URL)).Embed(context.Background(), EmbedRequest{
				ModelRef: "embed-v4.0", APIKey: "k", Dimensions: 1024, Texts: []string{"a", "b"},
			})
			if err == nil {
				t.Fatal("want error")
			}

			var apiErr *APIError
			if errors.As(err, &apiErr) != tt.apiErr {
				t.Errorf("err = %v, api error = %v", err, tt.apiErr)
			}
		})
	}
}

func TestCohereEmbedNoTexts(t *testing.T) {
	res, err := NewCohere(WithCohereBaseURL("http://unreachable.invalid")).Embed(context.Background(), EmbedRequest{})
	if err != nil || len(res.Vectors) != 0 {
		t.Fatalf("vectors = %v, err = %v", res.Vectors, err)
	}
}
