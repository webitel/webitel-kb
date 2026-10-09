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

func TestScoresInOrder(t *testing.T) {
	tests := []struct {
		name      string
		results   []rerankResult
		documents int
		want      []float64
		wantErr   bool
	}{
		{
			name:      "sorted by relevance",
			results:   []rerankResult{{Index: 2, RelevanceScore: 0.9}, {Index: 0, RelevanceScore: 0.5}, {Index: 1, RelevanceScore: 0.1}},
			documents: 3,
			want:      []float64{0.5, 0.1, 0.9},
		},
		{
			name:      "raw scores",
			results:   []rerankResult{{Index: 1, RelevanceScore: 4.4}, {Index: 0, RelevanceScore: -10.4}},
			documents: 2,
			want:      []float64{-10.4, 4.4},
		},
		{
			name:      "missing document",
			results:   []rerankResult{{Index: 0, RelevanceScore: 0.9}},
			documents: 2,
			wantErr:   true,
		},
		{
			name:      "extra result",
			results:   []rerankResult{{Index: 0}, {Index: 1}, {Index: 1}},
			documents: 2,
			wantErr:   true,
		},
		{
			name:      "document scored twice",
			results:   []rerankResult{{Index: 0}, {Index: 0}},
			documents: 2,
			wantErr:   true,
		},
		{
			name:      "index out of range",
			results:   []rerankResult{{Index: 0}, {Index: 2}},
			documents: 2,
			wantErr:   true,
		},
		{
			name:      "negative index",
			results:   []rerankResult{{Index: -1}, {Index: 0}},
			documents: 2,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scoresInOrder(tt.results, tt.documents)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("scores = %v, want %v", got, tt.want)
			}
		})
	}
}

// rerankCall is what a fake rerank server saw.
type rerankCall struct {
	path string
	auth string
	body rerankRequest
}

// rerankServer scores document i as i/10 and lists the results by relevance,
// not by input order, the way Cohere does.
func rerankServer(t *testing.T, calls *[]rerankCall) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := rerankCall{path: r.URL.Path, auth: r.Header.Get("Authorization")}
		_ = json.NewDecoder(r.Body).Decode(&call.body)
		*calls = append(*calls, call)

		out := rerankResponse{}
		for i := len(call.body.Documents) - 1; i >= 0; i-- {
			out.Results = append(out.Results, rerankResult{Index: i, RelevanceScore: float64(i) / 10})
		}

		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestRerankProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider func(base string) Provider
		endpoint func(base string) string
		apiKey   string
		wantPath string
		wantAuth string
	}{
		{
			name:     "self-hosted at its endpoint",
			provider: func(string) Provider { return NewEndpoint() },
			endpoint: func(base string) string { return base },
			wantPath: "/rerank",
		},
		{
			name:     "self-hosted endpoint with a version",
			provider: func(string) Provider { return NewEndpoint() },
			endpoint: func(base string) string { return base + "/v1/" },
			wantPath: "/v1/rerank",
		},
		{
			name:     "self-hosted endpoint with the path on it",
			provider: func(string) Provider { return NewEndpoint() },
			endpoint: func(base string) string { return base + "/v1/rerank/" },
			wantPath: "/v1/rerank",
		},
		{
			name:     "cohere at its default base",
			provider: func(base string) Provider { return NewCohere(WithCohereBaseURL(base + "/v2")) },
			endpoint: func(string) string { return "" },
			apiKey:   "secret",
			wantPath: "/v2/rerank",
			wantAuth: "Bearer secret",
		},
		{
			name:     "cohere ignores a registered endpoint",
			provider: func(base string) Provider { return NewCohere(WithCohereBaseURL(base + "/v2")) },
			endpoint: func(string) string { return "http://elsewhere.invalid" },
			apiKey:   "secret",
			wantPath: "/v2/rerank",
			wantAuth: "Bearer secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []rerankCall

			srv := rerankServer(t, &calls)

			res, err := tt.provider(srv.URL).Rerank(context.Background(), RerankRequest{
				ModelRef:  "BAAI/bge-reranker-v2-m3",
				APIKey:    tt.apiKey,
				Endpoint:  tt.endpoint(srv.URL),
				Query:     "q",
				Documents: []string{"d0", "d1", "d2"},
			})
			if err != nil {
				t.Fatalf("Rerank: %v", err)
			}

			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}

			got := calls[0]
			if got.path != tt.wantPath {
				t.Errorf("path = %q, want %q", got.path, tt.wantPath)
			}

			if got.auth != tt.wantAuth {
				t.Errorf("authorization = %q, want %q", got.auth, tt.wantAuth)
			}

			want := rerankRequest{
				Model: "BAAI/bge-reranker-v2-m3", Query: "q", Documents: []string{"d0", "d1", "d2"}, TopN: 3,
			}
			if got.body.Model != want.Model || got.body.Query != want.Query ||
				!slices.Equal(got.body.Documents, want.Documents) || got.body.TopN != want.TopN {
				t.Errorf("body = %+v, want %+v", got.body, want)
			}

			if !slices.Equal(res.Scores, []float64{0, 0.1, 0.2}) {
				t.Errorf("scores = %v, want document order", res.Scores)
			}
		})
	}
}

func TestRerankNoDocuments(t *testing.T) {
	var calls []rerankCall

	srv := rerankServer(t, &calls)

	res, err := NewEndpoint().Rerank(context.Background(), RerankRequest{Endpoint: srv.URL, Query: "q"})
	if err != nil || len(res.Scores) != 0 {
		t.Fatalf("scores = %v, err = %v", res.Scores, err)
	}

	if len(calls) != 0 {
		t.Errorf("calls = %d, want none", len(calls))
	}
}

func TestRerankErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{
			name:   "rejected key",
			status: http.StatusUnauthorized,
			body:   `{"message":"invalid api token"}`,
			check: func(t *testing.T, err error) {
				t.Helper()

				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
					t.Fatalf("want APIError 401, got %v", err)
				}
			},
		},
		{
			name:   "not the cohere contract",
			status: http.StatusOK,
			body:   `{"scores":[0.9,0.1]}`,
			check: func(t *testing.T, err error) {
				t.Helper()

				if err == nil {
					t.Fatal("want error for an answer without results")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := NewCohere(WithCohereBaseURL(srv.URL)).Rerank(context.Background(), RerankRequest{
				ModelRef: "rerank-v3.5", APIKey: "k", Query: "q", Documents: []string{"a", "b"},
			})
			tt.check(t, err)
		})
	}
}

func TestCohereEmbedUnsupported(t *testing.T) {
	_, err := NewCohere().Embed(context.Background(), EmbedRequest{Texts: []string{"x"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
