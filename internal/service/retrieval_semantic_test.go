package service

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/embedding"
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

	return NewRetrievalService(uow, fakeSealer{}, queryResolver{embedder}, settingsStub{}, discardLogger()), uow, embedder
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
		{SpaceID: 1, VectorSearchEnabled: true, ModelID: 9, Provider: "gemini", ModelRef: "a", Dimensions: 1, Config: []byte("enc:k1")},
		{SpaceID: 2, VectorSearchEnabled: true, ModelID: 9, Provider: "gemini", ModelRef: "a", Dimensions: 1, Config: []byte("enc:k1")},
		{SpaceID: 3, VectorSearchEnabled: true, ModelID: 11, Provider: "bge-m3", ModelRef: "b", Dimensions: 2, Endpoint: "http://embed"},
		{SpaceID: 4, VectorSearchEnabled: false},
	}
	provider.vectors = map[string][]float32{"a": {0.1}, "b": {0.2, 0.3}}

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
		if req.Task != embedding.TaskQuery || !reflect.DeepEqual(req.Texts, []string{"vpn"}) {
			t.Errorf("embed request = %+v", req)
		}

		if (req.ModelRef == "a" && req.Dimensions != 1) || (req.ModelRef == "b" && req.Dimensions != 2) {
			t.Errorf("dimensions = %d, want the model's", req.Dimensions)
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
		Filter: model.SearchFilter{SpaceIDs: []int64{1, 2, 3, 4, 99}, Tags: []string{"net"}, TagMatch: model.TagMatchAny},
		Vectors: []model.ModelVector{
			{ModelID: 9, Dimensions: 1, SpaceIDs: []int64{1, 2}, Vector: []float32{0.1}},
			{ModelID: 11, Dimensions: 2, SpaceIDs: []int64{3}, Vector: []float32{0.2, 0.3}},
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

func TestSemanticSearchRefusesAVectorOfAnotherSize(t *testing.T) {
	svc, uow, provider := semanticServiceWithFakes()
	uow.spaces.resolvedMany = []*model.SpaceEmbedding{
		{SpaceID: 1, VectorSearchEnabled: true, ModelID: 9, Provider: "bge-m3", ModelRef: "a", Dimensions: 3, Endpoint: "http://embed"},
	}
	provider.vectors = map[string][]float32{"a": {0.1, 0.2}}

	_, _, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{Query: "vpn", SpaceIDs: []int64{1}})
	if errors.ID(err) != "kb.model.dimensions_mismatch" || errors.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v, want kb.model.dimensions_mismatch", err)
	}

	if uow.retrieval.hybrid.Vectors != nil {
		t.Fatalf("a vector of another size reached the store: %+v", uow.retrieval.hybrid)
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
			wantCode: codes.FailedPrecondition, wantID: "kb.space.model_unset",
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

func TestClipQuery(t *testing.T) {
	words := func(word string, n int) string { return strings.Repeat(word+" ", n) }

	tests := []struct {
		name string
		in   string
		// min and max bound the clipped length in runes.
		min, max int
		// suffix is what the clipped query must end with.
		suffix string
	}{
		{name: "a short query is kept", in: "how do I reset my password?", min: 27, max: 27, suffix: "password?"},
		{name: "a query at the limit is kept", in: strings.Repeat("a", maxQueryRunes), min: maxQueryRunes, max: maxQueryRunes},
		{
			name: "a long query ends on a word", in: words("доставка", 1000),
			min: maxQueryRunes - len([]rune("доставка ")), max: maxQueryRunes, suffix: "доставка",
		},
		{name: "runes are counted, not bytes", in: words("ї", 3000), min: maxQueryRunes - 2, max: maxQueryRunes, suffix: "ї"},
		{
			name: "a query without spaces is cut at the limit", in: strings.Repeat("я", maxQueryRunes+500),
			min: maxQueryRunes, max: maxQueryRunes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clipQuery(tt.in)

			if !strings.HasPrefix(tt.in, got) {
				t.Fatal("clipped query is not a prefix of the query")
			}

			if n := len([]rune(got)); n < tt.min || n > tt.max {
				t.Fatalf("clipQuery = %d runes, want %d..%d", n, tt.min, tt.max)
			}

			if !strings.HasSuffix(got, tt.suffix) {
				t.Fatalf("clipped query does not end with %q", tt.suffix)
			}
		})
	}
}

func TestLongQueriesReachTheModelsClipped(t *testing.T) {
	long := strings.Repeat("vpn ", 1000)
	clipped := clipQuery(long)

	tests := []struct {
		name   string
		run    func(svc *RetrievalService) error
		rerank bool
	}{
		{
			name: "semantic search",
			run: func(svc *RetrievalService) error {
				_, _, err := svc.SemanticSearch(context.Background(), semanticOpts(), model.SemanticQuery{Query: long, SpaceIDs: []int64{1}})

				return err
			},
		},
		{
			name: "suggest",
			run: func(svc *RetrievalService) error {
				_, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: long, SpaceIDs: []int64{1}})

				return err
			},
			rerank: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, provider := suggestFixture()
			uow.spaces.resolvedMany[0] = &model.SpaceEmbedding{
				SpaceID: 1, VectorSearchEnabled: true, ModelID: 9, Provider: "bge-m3", ModelRef: "a", Dimensions: 1,
				Endpoint: "http://embed",
			}
			provider.vectors = map[string][]float32{"a": {0.1}}

			if err := tt.run(svc); err != nil {
				t.Fatalf("run: %v", err)
			}

			if len(provider.requests) != 1 || !reflect.DeepEqual(provider.requests[0].Texts, []string{clipped}) {
				t.Fatalf("embedded texts = %d, want the clipped query", len(provider.requests))
			}

			if uow.retrieval.hybrid.Term != clipped {
				t.Fatalf("full-text term = %d runes, want the clipped query", len([]rune(uow.retrieval.hybrid.Term)))
			}

			if tt.rerank && (len(provider.reranks) != 1 || provider.reranks[0].Query != clipped) {
				t.Fatalf("rerank requests = %d, want the clipped query", len(provider.reranks))
			}
		})
	}
}
