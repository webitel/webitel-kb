package service

import (
	"context"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/model"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

// suggestFixture is a domain of two lexical spaces, one of them reranking.
func suggestFixture() (*RetrievalService, *retrievalUow, *queryEmbedder) {
	svc, uow, provider := semanticServiceWithFakes()
	uow.spaces.resolvedMany = []*model.SpaceEmbedding{{SpaceID: 1}, {SpaceID: 2}}
	uow.spaces.rerankers = []*model.SpaceReranker{
		{SpaceID: 1, Enabled: true, ModelID: 30, Provider: "bge-reranker", ModelRef: "bge", Endpoint: "http://rerank"},
		{SpaceID: 2},
	}
	uow.retrieval.hits = []*model.ChunkHit{
		{ID: 1, ArticleID: 10, VersionID: 4, ChunkIndex: 0, Subject: "VPN", Content: "connect", Score: 0.03},
		{ID: 2, ArticleID: 20, VersionID: 5, ChunkIndex: 0, Subject: "Reset", Content: "password", Score: 0.02},
		{ID: 3, ArticleID: 10, VersionID: 4, ChunkIndex: 1, Subject: "VPN", Content: "disconnect", Score: 0.01},
		{ID: 4, ArticleID: 30, VersionID: 6, ChunkIndex: 0, Subject: "Billing", Content: "invoice", Score: 0.005},
	}
	uow.retrieval.items = []*model.ArticleSummary{
		{ID: 20, Subject: "Reset", Snippet: "leading text"},
		{ID: 10, Subject: "VPN", Snippet: "leading text"},
		{ID: 30, Subject: "Billing", Snippet: "leading text"},
	}
	provider.scores = []float64{0.2, 0.9, 0.4, 0.1}

	return svc, uow, provider
}

func TestSuggestGuards(t *testing.T) {
	tests := []struct {
		name        string
		query       model.SuggestQuery
		unknownTeam bool
		wantCode    codes.Code
		wantID      string
	}{
		{name: "a blank message suggests nothing", query: model.SuggestQuery{Message: " ", TeamID: 1}},
		{
			name:     "a team or spaces are required",
			query:    model.SuggestQuery{Message: "vpn"},
			wantCode: codes.InvalidArgument, wantID: "kb.retrieval.space_required",
		},
		{
			name:  "an unknown team is refused, not answered with the whole domain",
			query: model.SuggestQuery{Message: "vpn", TeamID: 7}, unknownTeam: true,
			wantCode: codes.InvalidArgument, wantID: "kb.retrieval.team_unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, _ := suggestFixture()
			uow.spaces.teamUnknown = tt.unknownTeam

			got, err := svc.Suggest(context.Background(), semanticOpts(), tt.query)
			if errors.Code(err) != tt.wantCode || errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v (id %q), want %v %q", err, errors.ID(err), tt.wantCode, tt.wantID)
			}

			if uow.retrieval.calls != 0 || (err == nil && (got.Articles == nil || got.Chunks == nil)) {
				t.Fatalf("calls = %d, answer = %+v", uow.retrieval.calls, got)
			}
		})
	}
}

func TestSuggestPicksTheSpaces(t *testing.T) {
	tests := []struct {
		name       string
		query      model.SuggestQuery
		bound      []int64
		wantAsked  int64
		wantIDs    []int64
		wantFilter []int64
	}{
		{
			name:       "the asked spaces win",
			query:      model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{2, 1}, TeamID: 7},
			bound:      []int64{9},
			wantIDs:    []int64{2, 1},
			wantFilter: []int64{1, 2},
		},
		{
			name:       "the team binding otherwise",
			query:      model.SuggestQuery{Message: "vpn", TeamID: 7},
			bound:      []int64{1, 2},
			wantAsked:  7,
			wantIDs:    []int64{1, 2},
			wantFilter: []int64{1, 2},
		},
		{
			name:       "a team without a binding sees every space",
			query:      model.SuggestQuery{Message: "vpn", TeamID: 7},
			bound:      []int64{},
			wantAsked:  7,
			wantIDs:    []int64{},
			wantFilter: []int64{1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, _ := suggestFixture()
			uow.spaces.teamSpaces = tt.bound

			if _, err := svc.Suggest(context.Background(), semanticOpts(), tt.query); err != nil {
				t.Fatalf("Suggest: %v", err)
			}

			if uow.spaces.teamAsked != tt.wantAsked || uow.spaces.resolvedDomain != 5 || !reflect.DeepEqual(uow.spaces.resolvedIDs, tt.wantIDs) {
				t.Fatalf("team asked = %d, resolved domain = %d, ids = %v", uow.spaces.teamAsked, uow.spaces.resolvedDomain, uow.spaces.resolvedIDs)
			}

			// The fused ranking and the summaries keep to the spaces that answered.
			if !reflect.DeepEqual(uow.retrieval.hybrid.Filter.SpaceIDs, tt.wantFilter) || !reflect.DeepEqual(uow.retrieval.spaces, tt.wantFilter) ||
				!reflect.DeepEqual(uow.spaces.rerankerIDs, tt.wantFilter) {
				t.Fatalf("filter = %v, resolve spaces = %v, reranker spaces = %v", uow.retrieval.hybrid.Filter.SpaceIDs, uow.retrieval.spaces, uow.spaces.rerankerIDs)
			}
		})
	}
}

func TestSuggestRunsTheCandidatePool(t *testing.T) {
	svc, uow, _ := suggestFixture()

	if _, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	want := model.HybridQuery{Term: "vpn", Filter: model.SearchFilter{SpaceIDs: []int64{1, 2}}, Vectors: []model.ModelVector{}, TopK: suggestCandidates}
	if !reflect.DeepEqual(uow.retrieval.hybrid, want) || uow.txCalls != 1 {
		t.Fatalf("hybrid = %+v, transactions = %d", uow.retrieval.hybrid, uow.txCalls)
	}
}

func TestSuggestReranksTheHits(t *testing.T) {
	svc, uow, provider := suggestFixture()

	got, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}, ReturnChunks: true})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	wantReq := embedding.RerankRequest{
		ModelRef: "bge", Endpoint: "http://rerank", Query: "vpn",
		Documents: []string{"VPN\nconnect", "Reset\npassword", "VPN\ndisconnect", "Billing\ninvoice"},
	}
	if len(provider.reranks) != 1 || !reflect.DeepEqual(provider.reranks[0], wantReq) {
		t.Fatalf("rerank requests = %+v, want %+v", provider.reranks, wantReq)
	}

	want := []*model.ChunkHit{
		{ID: 2, ArticleID: 20, VersionID: 5, ChunkIndex: 0, Subject: "Reset", Content: "password", Score: 0.9},
		{ID: 3, ArticleID: 10, VersionID: 4, ChunkIndex: 1, Subject: "VPN", Content: "disconnect", Score: 0.4},
		{ID: 1, ArticleID: 10, VersionID: 4, ChunkIndex: 0, Subject: "VPN", Content: "connect", Score: 0.2},
	}
	if !reflect.DeepEqual(got.Chunks, want) || len(got.Articles) != 0 {
		t.Fatalf("chunks = %+v, articles = %d", got.Chunks, len(got.Articles))
	}

	// The fused hits are left as they were.
	if uow.retrieval.hits[0].Score != 0.03 {
		t.Fatalf("fused score = %v, want untouched", uow.retrieval.hits[0].Score)
	}
}

func TestSuggestOpensTheRerankerCredential(t *testing.T) {
	svc, uow, provider := suggestFixture()
	uow.spaces.rerankers = []*model.SpaceReranker{
		{SpaceID: 1, Enabled: true, ModelID: 30, Provider: "cohere", ModelRef: "rerank-v3", Config: []byte("enc:k1")},
	}

	if _, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	if len(provider.reranks) != 1 || provider.reranks[0].APIKey != "k1" {
		t.Fatalf("rerank requests = %+v, want the opened credential", provider.reranks)
	}
}

func TestSuggestWithoutARerankerKeepsTheFusionOrder(t *testing.T) {
	svc, uow, provider := suggestFixture()
	uow.spaces.rerankers = []*model.SpaceReranker{{SpaceID: 1}, {SpaceID: 2}}

	got, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}, ReturnChunks: true})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	if len(provider.reranks) != 0 || !reflect.DeepEqual(got.Chunks, uow.retrieval.hits[:suggestSize]) {
		t.Fatalf("rerank requests = %d, chunks = %+v", len(provider.reranks), got.Chunks)
	}
}

func TestSuggestKeepsAnsweringWhenTheRerankFails(t *testing.T) {
	tests := []struct {
		name      string
		rerankers []*model.SpaceReranker
		lookupErr error
		rerankErr error
		scores    []float64
	}{
		{
			name: "spaces with different rerankers",
			rerankers: []*model.SpaceReranker{
				{SpaceID: 1, Enabled: true, ModelID: 30, Provider: "bge-reranker"},
				{SpaceID: 2, Enabled: true, ModelID: 31, Provider: "bge-reranker"},
			},
		},
		{
			name:      "the reranker config cannot be read",
			lookupErr: stderrors.New("connection reset"),
		},
		{
			name:      "a cloud reranker with no stored credential",
			rerankers: []*model.SpaceReranker{{SpaceID: 1, Enabled: true, ModelID: 30, Provider: "cohere", ModelRef: "rerank-v3"}},
		},
		{name: "a rejected credential", rerankErr: &embedding.APIError{StatusCode: 403}},
		{name: "an unavailable reranker", rerankErr: &embedding.APIError{StatusCode: 502}},
		{name: "a reranker that scores the wrong count", scores: []float64{0.1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, provider := suggestFixture()
			uow.spaces.rerankersErr = tt.lookupErr
			provider.rerankErr = tt.rerankErr

			if tt.rerankers != nil {
				uow.spaces.rerankers = tt.rerankers
			}

			if tt.scores != nil {
				provider.scores = tt.scores
			}

			got, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{
				Message: "vpn", SpaceIDs: []int64{1}, ReturnChunks: true,
			})
			if err != nil {
				t.Fatalf("Suggest: %v", err)
			}

			if !reflect.DeepEqual(got.Chunks, uow.retrieval.hits[:suggestSize]) {
				t.Fatalf("chunks = %+v, want the fusion order untouched", got.Chunks)
			}
		})
	}
}

func TestSuggestAnswersArticles(t *testing.T) {
	long := strings.Repeat("x", queryobject.ExcerptLength+20)

	svc, uow, _ := suggestFixture()
	uow.retrieval.hits[1].Content = long

	got, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	// Reranked order: 20 (0.9), 10 (0.4 then 0.2), 30 (0.1); one id per article.
	if !reflect.DeepEqual(uow.retrieval.ids, []int64{20, 10, 30}) || !reflect.DeepEqual(uow.retrieval.spaces, []int64{1, 2}) {
		t.Fatalf("resolve ids = %v, spaces = %v", uow.retrieval.ids, uow.retrieval.spaces)
	}

	want := []*model.ArticleSummary{
		{ID: 20, Subject: "Reset", Snippet: long[:queryobject.ExcerptLength]},
		{ID: 10, Subject: "VPN", Snippet: "disconnect"},
		{ID: 30, Subject: "Billing", Snippet: "invoice"},
	}
	if !reflect.DeepEqual(got.Articles, want) || len(got.Chunks) != 0 {
		t.Fatalf("articles = %+v, chunks = %d", got.Articles, len(got.Chunks))
	}
}

func TestSuggestSizeCapsBothModes(t *testing.T) {
	svc, uow, provider := suggestFixture()
	uow.retrieval.hits = append(uow.retrieval.hits, &model.ChunkHit{ID: 5, ArticleID: 40, Subject: "Extra", Content: "more"})
	provider.scores = []float64{0.5, 0.4, 0.3, 0.2, 0.1}

	chunks, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}, ReturnChunks: true})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	if len(chunks.Chunks) != suggestSize {
		t.Fatalf("chunks = %d, want %d of the five hits", len(chunks.Chunks), suggestSize)
	}

	if _, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	// Four articles among the hits, the first three by score are asked for.
	if !reflect.DeepEqual(uow.retrieval.ids, []int64{10, 20, 30}) {
		t.Fatalf("article ids = %v", uow.retrieval.ids)
	}
}

func TestSuggestEmptyPaths(t *testing.T) {
	tests := []struct {
		name   string
		spaces []*model.SpaceEmbedding
		hits   []*model.ChunkHit
	}{
		{name: "unknown spaces", spaces: []*model.SpaceEmbedding{}},
		{name: "no hits", spaces: []*model.SpaceEmbedding{{SpaceID: 1}}, hits: []*model.ChunkHit{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow, provider := suggestFixture()
			uow.spaces.resolvedMany = tt.spaces
			uow.retrieval.hits = tt.hits

			got, err := svc.Suggest(context.Background(), semanticOpts(), model.SuggestQuery{Message: "vpn", SpaceIDs: []int64{1}})
			if err != nil {
				t.Fatalf("Suggest: %v", err)
			}

			if len(got.Articles) != 0 || len(got.Chunks) != 0 || got.Articles == nil || got.Chunks == nil ||
				len(provider.reranks) != 0 || uow.spaces.rerankerIDs != nil {
				t.Fatalf("answer = %+v, reranks = %d, reranker lookups = %v", got, len(provider.reranks), uow.spaces.rerankerIDs)
			}
		})
	}
}

func TestPickReranker(t *testing.T) {
	tests := []struct {
		name    string
		spaces  []*model.SpaceReranker
		wantID  int64
		wantErr bool
	}{
		{name: "none", spaces: []*model.SpaceReranker{{SpaceID: 1}, {SpaceID: 2, ModelID: 30}}},
		{name: "one", spaces: []*model.SpaceReranker{{SpaceID: 1}, {SpaceID: 2, Enabled: true, ModelID: 30}}, wantID: 30},
		{name: "shared", spaces: []*model.SpaceReranker{{SpaceID: 1, Enabled: true, ModelID: 30}, {SpaceID: 2, Enabled: true, ModelID: 30}}, wantID: 30},
		{name: "conflict", spaces: []*model.SpaceReranker{{SpaceID: 1, Enabled: true, ModelID: 30}, {SpaceID: 2, Enabled: true, ModelID: 31}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickReranker(tt.spaces)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want one = %v", err, tt.wantErr)
			}

			var gotID int64
			if got != nil {
				gotID = got.ModelID
			}

			if gotID != tt.wantID {
				t.Fatalf("picked = %d, want %d", gotID, tt.wantID)
			}
		})
	}
}

func TestOrderByScore(t *testing.T) {
	hits := []*model.ChunkHit{{ID: 1, Score: 0.03}, {ID: 2, Score: 0.02}, {ID: 3, Score: 0.01}}

	tests := []struct {
		name   string
		scores []float64
		want   []int64
	}{
		{name: "reversed", scores: []float64{0.1, 0.5, 0.9}, want: []int64{3, 2, 1}},
		{name: "ties keep the fusion order", scores: []float64{0.5, 0.5, 0.9}, want: []int64{3, 1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := orderByScore(hits, tt.scores)

			ids := make([]int64, 0, len(got))
			for i, hit := range got {
				ids = append(ids, hit.ID)

				if hit.Score != tt.scores[hit.ID-1] {
					t.Errorf("hit %d score = %v, want %v", i, hit.Score, tt.scores[hit.ID-1])
				}
			}

			if !reflect.DeepEqual(ids, tt.want) {
				t.Fatalf("order = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestLeadingHits(t *testing.T) {
	hits := []*model.ChunkHit{{ID: 1, ArticleID: 10}, {ID: 2, ArticleID: 10}, {ID: 3, ArticleID: 20}, {ID: 4, ArticleID: 30}}

	tests := []struct {
		name string
		n    int
		want []int64
	}{
		{name: "first chunk of every article", n: 5, want: []int64{1, 3, 4}},
		{name: "cut at n articles", n: 2, want: []int64{1, 3}},
		{name: "none", n: 0, want: []int64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := leadingHits(hits, tt.n)

			ids := make([]int64, 0, len(got))
			for _, hit := range got {
				ids = append(ids, hit.ID)
			}

			if !reflect.DeepEqual(ids, tt.want) {
				t.Fatalf("leading = %v, want %v", ids, tt.want)
			}
		})
	}
}
