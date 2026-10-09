package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// embedCall is what a fake embeddings server saw.
type embedCall struct {
	path   string
	auth   string
	apiKey string
	body   embedRequest
}

// embedServer answers input i with the vector [i, i] and lists the vectors in
// reverse order, so the caller must place them by index.
func embedServer(t *testing.T, calls *[]embedCall) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := embedCall{path: r.URL.Path, auth: r.Header.Get("Authorization"), apiKey: r.Header.Get("api-key")}
		_ = json.NewDecoder(r.Body).Decode(&call.body)
		*calls = append(*calls, call)

		out := embedResponse{}
		for i := len(call.body.Input) - 1; i >= 0; i-- {
			out.Data = append(out.Data, embedData{Index: i, Embedding: []float32{float32(i), float32(i)}})
		}

		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestEmbedProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider func(base string) Provider
		endpoint func(base string) string
		apiKey   string
		wantPath string
		wantAuth string
		wantKey  string
		wantDims int
	}{
		{
			name:     "self-hosted at its root",
			provider: func(string) Provider { return NewEndpoint() },
			endpoint: func(base string) string { return base },
			wantPath: "/v1/embeddings",
		},
		{
			name:     "self-hosted registered with the route",
			provider: func(string) Provider { return NewEndpoint() },
			endpoint: func(base string) string { return base + "/v1/embeddings" },
			wantPath: "/v1/embeddings",
		},
		{
			name:     "openai at its base, with the key and the size",
			provider: func(base string) Provider { return NewOpenAI(WithOpenAIBaseURL(base)) },
			endpoint: func(string) string { return "" },
			apiKey:   "secret",
			wantPath: "/v1/embeddings",
			wantAuth: "Bearer secret",
			wantDims: 768,
		},
		{
			name:     "openai ignores a registered endpoint",
			provider: func(base string) Provider { return NewOpenAI(WithOpenAIBaseURL(base)) },
			endpoint: func(string) string { return "http://elsewhere.invalid" },
			apiKey:   "secret",
			wantPath: "/v1/embeddings",
			wantAuth: "Bearer secret",
			wantDims: 768,
		},
		{
			name:     "azure at the root of its resource",
			provider: func(string) Provider { return NewAzure() },
			endpoint: func(base string) string { return base },
			apiKey:   "secret",
			wantPath: "/openai/v1/embeddings",
			wantKey:  "secret",
			wantDims: 768,
		},
		{
			name:     "azure registered with the v1 base",
			provider: func(string) Provider { return NewAzure() },
			endpoint: func(base string) string { return base + "/openai/v1/" },
			apiKey:   "secret",
			wantPath: "/openai/v1/embeddings",
			wantKey:  "secret",
			wantDims: 768,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []embedCall

			srv := embedServer(t, &calls)

			res, err := tt.provider(srv.URL).Embed(context.Background(), EmbedRequest{
				ModelRef:   "m",
				APIKey:     tt.apiKey,
				Endpoint:   tt.endpoint(srv.URL),
				Dimensions: 768,
				Texts:      []string{"a", "b", "c"},
			})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}

			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}

			got := calls[0]
			if got.path != tt.wantPath || got.auth != tt.wantAuth || got.apiKey != tt.wantKey {
				t.Errorf("path = %q, authorization = %q, api-key = %q; want %q, %q, %q",
					got.path, got.auth, got.apiKey, tt.wantPath, tt.wantAuth, tt.wantKey)
			}

			if got.body.Model != "m" || !slices.Equal(got.body.Input, []string{"a", "b", "c"}) || got.body.Dimensions != tt.wantDims {
				t.Errorf("body = %+v", got.body)
			}

			for i, v := range res.Vectors {
				if len(v) != 2 || v[0] != float32(i) {
					t.Errorf("vector %d = %v, want the one answered for input %d", i, v, i)
				}
			}
		})
	}
}

func TestEmbedNoTexts(t *testing.T) {
	var calls []embedCall

	srv := embedServer(t, &calls)

	res, err := NewEndpoint().Embed(context.Background(), EmbedRequest{Endpoint: srv.URL})
	if err != nil || len(res.Vectors) != 0 || len(calls) != 0 {
		t.Fatalf("vectors = %v, err = %v, calls = %d", res.Vectors, err, len(calls))
	}
}

func TestEmbedErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		apiErr bool
	}{
		{name: "rejected key", status: http.StatusUnauthorized, body: `{"error":{"message":"Incorrect API key"}}`, apiErr: true},
		{name: "missing vector", status: http.StatusOK, body: `{"data":[{"index":0,"embedding":[1]}]}`},
		{name: "vector answered twice", status: http.StatusOK, body: `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`},
		{name: "unknown input", status: http.StatusOK, body: `{"data":[{"index":0,"embedding":[1]},{"index":5,"embedding":[2]}]}`},
		{name: "not the openai contract", status: http.StatusOK, body: `{"embeddings":[[1],[2]]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := NewOpenAI(WithOpenAIBaseURL(srv.URL)).Embed(context.Background(), EmbedRequest{
				ModelRef: "text-embedding-3-small", APIKey: "k", Texts: []string{"a", "b"},
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

func TestEmbedAsksForNoSizeWithoutOne(t *testing.T) {
	var raw []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer srv.Close()

	if _, err := NewOpenAI(WithOpenAIBaseURL(srv.URL)).Embed(context.Background(), EmbedRequest{
		ModelRef: "text-embedding-3-small", APIKey: "k", Texts: []string{"a"},
	}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if strings.Contains(string(raw), "dimensions") {
		t.Fatalf("body %s asks for a size", raw)
	}
}

func TestOpenAIRerankUnsupported(t *testing.T) {
	_, err := NewOpenAI().Rerank(context.Background(), RerankRequest{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
