package service

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/metrics"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

// queryEmbedder records embed and rerank requests and plays back canned answers.
type queryEmbedder struct {
	requests []embedding.EmbedRequest
	vectors  map[string][]float32
	err      error

	reranks   []embedding.RerankRequest
	scores    []float64
	rerankErr error
}

func (p *queryEmbedder) Embed(_ context.Context, req embedding.EmbedRequest) (embedding.EmbedResult, error) {
	p.requests = append(p.requests, req)

	if p.err != nil {
		return embedding.EmbedResult{}, p.err
	}

	return embedding.EmbedResult{Vectors: [][]float32{p.vectors[req.ModelRef]}}, nil
}

func (p *queryEmbedder) Rerank(_ context.Context, req embedding.RerankRequest) (embedding.RerankResult, error) {
	p.reranks = append(p.reranks, req)

	if p.rerankErr != nil {
		return embedding.RerankResult{}, p.rerankErr
	}

	return embedding.RerankResult{Scores: p.scores}, nil
}

// queryResolver hands out one embedder for every provider key.
type queryResolver struct{ embedder *queryEmbedder }

func (r queryResolver) ForModel(string) (embedding.Provider, error) { return r.embedder, nil }

func semanticServiceWithFakes() (*RetrievalService, *retrievalUow, *queryEmbedder) {
	embedder := &queryEmbedder{vectors: map[string][]float32{}}
	uow := &retrievalUow{retrieval: &retrievalStoreFake{}, spaces: &fakeSpaceStore{}}

	return NewRetrievalService(uow, fakeSealer{}, queryResolver{embedder}, metrics.Noop(), discardLogger()), uow, embedder
}

func semanticOpts() options.Searcher {
	return &readOpts{auth: fakeAuther{domainID: 5}, size: options.DefaultSearchSize, page: 1}
}

func TestSemanticSearchGuards(t *testing.T) {
	tests := []struct {
		name      string
		query     model.SemanticQuery
		wantCode  codes.Code
		wantCalls int
	}{
		{name: "a blank query matches nothing", query: model.SemanticQuery{Query: "  ", SpaceIDs: []int64{1}}},
		{name: "spaces are required", query: model.SemanticQuery{Query: "vpn"}, wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, _ := semanticServiceWithFakes()

			hits, citations, err := svc.SemanticSearch(context.Background(), semanticOpts(), tt.query)
			if errors.Code(err) != tt.wantCode {
				t.Fatalf("error = %v, want %v", err, tt.wantCode)
			}

			if uow.retrieval.calls != tt.wantCalls || (err == nil && (hits == nil || citations == nil)) {
				t.Fatalf("calls = %d, hits = %v, citations = %v", uow.retrieval.calls, hits, citations)
			}
		})
	}
}

func TestSemanticSearchGroupsSpacesByModel(t *testing.T) {
	svc, uow, provider := semanticServiceWithFakes()
	uow.spaces.resolvedMany = []*model.SpaceEmbedding{
		{SpaceID: 1, VectorSearchEnabled: true, ModelID: 9, Provider: "gemini", ModelRef: "a", Dimensions: 768, Config: []byte("enc:k1")},
		{SpaceID: 2, VectorSearchEnabled: true, ModelID: 9, Provider: "gemini", ModelRef: "a", Dimensions: 768, Config: []byte("enc:k1")},
		{SpaceID: 3, VectorSearchEnabled: true, ModelID: 11, Provider: "bge-m3", ModelRef: "b", Dimensions: 768, Endpoint: "http://embed"},
		{SpaceID: 4, VectorSearchEnabled: false},
	}
	provider.vectors = map[string][]float32{"a": {0.1}, "b": {0.2}}

	_, _, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{
		Query: "vpn", SpaceIDs: []int64{1, 2, 3, 4, 99}, Tags: []string{"net"},
	})
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	if uow.spaces.resolvedDomain != 5 || !reflect.DeepEqual(uow.spaces.resolvedIDs, []int64{1, 2, 3, 4, 99}) {
		t.Fatalf("resolved domain = %d, ids = %v", uow.spaces.resolvedDomain, uow.spaces.resolvedIDs)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("embed calls = %d, want one per model", len(provider.requests))
	}

	for _, req := range provider.requests {
		if req.Task != embedding.TaskQuery || req.Dimensions != 768 || !reflect.DeepEqual(req.Texts, []string{"vpn"}) {
			t.Errorf("embed request = %+v", req)
		}

		if req.ModelRef == "a" && req.APIKey != "k1" {
			t.Errorf("cloud key = %q, want the opened credential", req.APIKey)
		}

		if req.ModelRef == "b" && (req.APIKey != "" || req.Endpoint != "http://embed") {
			t.Errorf("self-hosted request = %+v", req)
		}
	}

	got := uow.retrieval.hybrid
	want := model.HybridQuery{
		Term:   "vpn",
		Filter: model.SearchFilter{SpaceIDs: []int64{1, 2, 3, 4, 99}, Tags: []string{"net"}},
		Vectors: []model.ModelVector{
			{ModelID: 9, SpaceIDs: []int64{1, 2}, Vector: []float32{0.1}},
			{ModelID: 11, SpaceIDs: []int64{3}, Vector: []float32{0.2}},
		},
		TopK: 10,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hybrid query = %+v\n want %+v", got, want)
	}

	if uow.txCalls != 1 {
		t.Fatalf("transactions = %d, want 1", uow.txCalls)
	}
}

func TestSemanticSearchTopK(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "default", in: 0, want: 10},
		{name: "as asked", in: 7, want: 7},
		{name: "capped at the branch depth", in: 500, want: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, _ := semanticServiceWithFakes()
			uow.spaces.resolvedMany = []*model.SpaceEmbedding{{SpaceID: 1}}

			_, _, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{Query: "vpn", SpaceIDs: []int64{1}, TopK: tt.in})
			if err != nil {
				t.Fatalf("SemanticSearch: %v", err)
			}

			if uow.retrieval.hybrid.TopK != tt.want {
				t.Fatalf("top k = %d, want %d", uow.retrieval.hybrid.TopK, tt.want)
			}
		})
	}
}

func TestSemanticSearchRefusals(t *testing.T) {
	cloud := func() []*model.SpaceEmbedding {
		return []*model.SpaceEmbedding{{SpaceID: 1, VectorSearchEnabled: true, ModelID: 9, Provider: "gemini", ModelRef: "a", Config: []byte("enc:k")}}
	}

	tests := []struct {
		name     string
		spaces   []*model.SpaceEmbedding
		embedErr error
		wantCode codes.Code
		wantID   string
	}{
		{
			name:     "a vector space without a model",
			spaces:   []*model.SpaceEmbedding{{SpaceID: 1, VectorSearchEnabled: true}},
			wantCode: codes.Aborted, wantID: "kb.space.model_unset",
		},
		{
			name:     "a rejected credential",
			spaces:   cloud(),
			embedErr: &embedding.APIError{StatusCode: 401},
			wantCode: codes.FailedPrecondition, wantID: "kb.model.credential_rejected",
		},
		{
			name:     "an unavailable provider",
			spaces:   cloud(),
			embedErr: &embedding.APIError{StatusCode: 503},
			wantCode: codes.Unavailable, wantID: "kb.retrieval.embedding_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, provider := semanticServiceWithFakes()
			uow.spaces.resolvedMany = tt.spaces
			provider.err = tt.embedErr

			_, _, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{Query: "vpn", SpaceIDs: []int64{1}})
			if errors.Code(err) != tt.wantCode || errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v (id %q), want %v %q", err, errors.ID(err), tt.wantCode, tt.wantID)
			}

			if uow.retrieval.calls != 0 {
				t.Fatalf("store calls = %d, want none", uow.retrieval.calls)
			}
		})
	}
}

func TestSemanticSearchCitations(t *testing.T) {
	long := strings.Repeat("x", queryobject.ExcerptLength+20)

	svc, uow, _ := semanticServiceWithFakes()
	uow.spaces.resolvedMany = []*model.SpaceEmbedding{{SpaceID: 1}}
	uow.retrieval.hits = []*model.ChunkHit{
		{ID: 1, ArticleID: 10, Subject: "VPN", Content: long, Score: 0.03},
		{ID: 2, ArticleID: 20, Subject: "Reset", Content: "short", Score: 0.02},
		{ID: 3, ArticleID: 10, Subject: "VPN", Content: "later chunk", Score: 0.01},
	}

	tests := []struct {
		name    string
		include bool
		want    []*model.Citation
	}{
		{name: "off", include: false, want: []*model.Citation{}},
		{name: "one per article in fusion order", include: true, want: []*model.Citation{
			{ArticleID: 10, Title: "VPN", Snippet: long[:queryobject.ExcerptLength]},
			{ArticleID: 20, Title: "Reset", Snippet: "short"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, citations, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{
				Query: "vpn", SpaceIDs: []int64{1}, IncludeCitations: tt.include,
			})
			if err != nil {
				t.Fatalf("SemanticSearch: %v", err)
			}

			if len(hits) != 3 || !reflect.DeepEqual(citations, tt.want) {
				t.Fatalf("hits = %d, citations = %+v", len(hits), citations)
			}
		})
	}
}

// discardLogger is the logger of a test: the service logs, nothing reads it.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// providerCall is one call the service reported to its metrics.
type providerCall struct {
	kind, provider, modelRef string
	failed                   bool
}

type recordingMetrics struct{ calls []providerCall }

func (m *recordingMetrics) Embedding(_ context.Context, provider, modelRef string, _ time.Duration, err error) {
	m.calls = append(m.calls, providerCall{"embedding", provider, modelRef, err != nil})
}

func (m *recordingMetrics) Rerank(_ context.Context, provider, modelRef string, _ time.Duration, err error) {
	m.calls = append(m.calls, providerCall{"rerank", provider, modelRef, err != nil})
}

func TestProviderCallsReachTheMetrics(t *testing.T) {
	providerErr := errors.New("provider is down")

	tests := []struct {
		name string
		call func(svc *RetrievalService)
		err  error
		want providerCall
	}{
		{
			name: "embedding answered",
			call: func(svc *RetrievalService) {
				_, _ = svc.embedWith(context.Background(), "q",
					&model.SpaceEmbedding{Provider: "bge-m3", ModelRef: "BAAI/bge-m3", Dimensions: 3})
			},
			want: providerCall{"embedding", "bge-m3", "BAAI/bge-m3", false},
		},
		{
			name: "embedding failed",
			call: func(svc *RetrievalService) {
				_, _ = svc.embedWith(context.Background(), "q",
					&model.SpaceEmbedding{Provider: "bge-m3", ModelRef: "BAAI/bge-m3", Dimensions: 3})
			},
			err:  providerErr,
			want: providerCall{"embedding", "bge-m3", "BAAI/bge-m3", true},
		},
		{
			name: "rerank failed",
			call: func(svc *RetrievalService) {
				_, _ = svc.rerankWith(context.Background(), "q",
					&model.SpaceReranker{Enabled: true, Provider: "bge-reranker", ModelRef: "BAAI/bge-reranker-v2-m3"},
					[]*model.ChunkHit{{}})
			},
			err:  providerErr,
			want: providerCall{"rerank", "bge-reranker", "BAAI/bge-reranker-v2-m3", true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, embedder := semanticServiceWithFakes()
			embedder.err, embedder.rerankErr = tt.err, tt.err
			recorder := &recordingMetrics{}
			svc.metrics = recorder

			tt.call(svc)

			if len(recorder.calls) != 1 || recorder.calls[0] != tt.want {
				t.Fatalf("recorded %+v, want [%+v]", recorder.calls, tt.want)
			}
		})
	}
}
