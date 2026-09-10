package grpc

import (
	"context"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/service"
	"github.com/webitel/webitel-kb/internal/store"
)

// retrievalStoreFake records what the handler path produced.
type retrievalStoreFake struct {
	filter model.SearchFilter
	term   string
	size   int
	page   int
	ids    []int64
	spaces []int64
	menu   struct {
		spaceID  int64
		parentID int64
		depth    int32
	}

	items []*model.ArticleSummary
	next  bool

	hybrid model.HybridQuery
	hits   []*model.ChunkHit
	hitErr error
}

func (f *retrievalStoreFake) SemanticSearch(
	_ context.Context, _ options.Searcher, q model.HybridQuery,
) ([]*model.ChunkHit, error) {
	f.hybrid = q

	return f.hits, f.hitErr
}

func (f *retrievalStoreFake) Search(
	_ context.Context, opts options.Searcher, filter model.SearchFilter,
) ([]*model.ArticleSummary, bool, error) {
	f.filter, f.term = filter, opts.GetSearch()
	f.size, f.page = opts.GetSize(), opts.GetPage()

	return f.items, f.next, nil
}

func (f *retrievalStoreFake) Resolve(
	_ context.Context, opts options.Searcher, spaceIDs []int64,
) ([]*model.ArticleSummary, error) {
	f.ids, f.spaces = opts.GetIDs(), spaceIDs
	f.size = opts.GetSize()

	return f.items, nil
}

func (f *retrievalStoreFake) Menu(
	_ context.Context, _ options.Searcher, spaceID, parentID int64, depthLimit int32,
) ([]*model.ArticleSummary, error) {
	f.menu.spaceID, f.menu.parentID, f.menu.depth = spaceID, parentID, depthLimit

	return f.items, nil
}

// retrievalUoWFake hands out the retrieval fake; spaces carry no vectors, a transaction is a pass-through.
type retrievalUoWFake struct {
	store.UnitOfWork

	retrieval *retrievalStoreFake
}

func (f *retrievalUoWFake) RetrievalStore() store.RetrievalStore { return f.retrieval }
func (f *retrievalUoWFake) SpaceStore() store.SpaceStore         { return fakeSpaces{&fakeUow{}} }

func (f *retrievalUoWFake) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	return fn(ctx, f)
}

func retrievalServerWithFake() (*RetrievalServer, *retrievalStoreFake) {
	fake := &retrievalStoreFake{}
	svc := service.NewRetrievalService(&retrievalUoWFake{retrieval: fake}, sealer{}, embedding.NewRegistry())

	return NewRetrievalServer(svc), fake
}

func TestRetrievalSearchMapsTheRequest(t *testing.T) {
	tests := []struct {
		name         string
		req          *kb.SearchRequest
		wantSize     int
		wantPage     int
		wantMatchAll bool
	}{
		{
			name:         "paging defaults",
			req:          &kb.SearchRequest{Query: "vpn"},
			wantSize:     options.DefaultSearchSize,
			wantPage:     1,
			wantMatchAll: true,
		},
		{
			name:         "all tags is the default match",
			req:          &kb.SearchRequest{Query: "vpn", Tags: []string{"a", "b"}, Size: 3, Page: 2},
			wantSize:     3,
			wantPage:     2,
			wantMatchAll: true,
		},
		{
			name:         "any tag",
			req:          &kb.SearchRequest{Query: "vpn", Tags: []string{"a"}, TagMatch: kb.TagMatch_TAG_MATCH_ANY, Size: 3},
			wantSize:     3,
			wantPage:     1,
			wantMatchAll: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, fake := retrievalServerWithFake()

			if _, err := server.Search(retrievalContext(), tt.req); err != nil {
				t.Fatalf("Search: %v", err)
			}

			if fake.term != tt.req.GetQuery() || fake.size != tt.wantSize || fake.page != tt.wantPage {
				t.Fatalf("term = %q, size = %d, page = %d", fake.term, fake.size, fake.page)
			}

			if fake.filter.TagsMatchAll != tt.wantMatchAll || len(fake.filter.Tags) != len(tt.req.GetTags()) {
				t.Fatalf("filter = %+v", fake.filter)
			}
		})
	}
}

func TestRetrievalSearchMapsTheAnswer(t *testing.T) {
	server, fake := retrievalServerWithFake()
	moment := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	fake.next = true
	fake.items = []*model.ArticleSummary{{
		ID: 11, SpaceID: 7, ParentID: 3, Depth: 2, Type: model.ArticleTypeFAQ,
		Subject: "VPN", Snippet: "how to", Tags: []string{"net"},
		State: model.ArticleStateActive, Body: "full text",
		CreatedAt: moment, UpdatedAt: moment,
	}}

	res, err := server.Search(retrievalContext(), &kb.SearchRequest{Query: "vpn"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if !res.GetNext() || len(res.GetItems()) != 1 {
		t.Fatalf("response = %+v", res)
	}

	got := res.GetItems()[0]
	want := &kb.ArticleSummary{
		Id: 11, SpaceId: 7, ParentId: 3, Depth: 2, Type: kb.ArticleType_FAQ,
		Subject: "VPN", Snippet: "how to", Tags: []string{"net"},
		State: kb.ArticleState_ACTIVE, Body: "full text",
		CreatedAt: moment.UnixMilli(), UpdatedAt: moment.UnixMilli(),
	}

	if got.GetId() != want.GetId() || got.GetSpaceId() != want.GetSpaceId() ||
		got.GetParentId() != want.GetParentId() || got.GetDepth() != want.GetDepth() ||
		got.GetType() != want.GetType() || got.GetSubject() != want.GetSubject() ||
		got.GetSnippet() != want.GetSnippet() || got.GetState() != want.GetState() ||
		got.GetBody() != want.GetBody() || got.GetCreatedAt() != want.GetCreatedAt() ||
		got.GetUpdatedAt() != want.GetUpdatedAt() {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

func TestRetrievalResolveReadsTheWholeBatch(t *testing.T) {
	server, fake := retrievalServerWithFake()
	fake.items = []*model.ArticleSummary{{ID: 3}, {ID: 1}}

	res, err := server.Resolve(retrievalContext(), &kb.ResolveRequest{Ids: []int64{3, 1}, SpaceIds: []int64{7}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// A page would drop what the caller asked for.
	if fake.size != options.UnlimitedSize {
		t.Fatalf("size = %d, want the batch unpaged", fake.size)
	}

	if len(fake.ids) != 2 || fake.ids[0] != 3 || len(fake.spaces) != 1 {
		t.Fatalf("ids = %v, spaces = %v", fake.ids, fake.spaces)
	}

	if len(res.GetItems()) != 2 || res.GetItems()[0].GetId() != 3 {
		t.Fatalf("items = %+v, want the asked order", res.GetItems())
	}
}

func TestRetrievalEmptyAnswersAreEmptyLists(t *testing.T) {
	server, _ := retrievalServerWithFake()
	ctx := retrievalContext()

	search, err := server.Search(ctx, &kb.SearchRequest{Query: "nothing matches"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	resolve, err := server.Resolve(ctx, &kb.ResolveRequest{Ids: []int64{404}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	menu, err := server.Menu(ctx, &kb.MenuRequest{SpaceId: 7})
	if err != nil {
		t.Fatalf("Menu: %v", err)
	}

	if search.GetItems() == nil || resolve.GetItems() == nil || menu.GetItems() == nil {
		t.Fatal("an empty answer is an empty list, not a missing one")
	}
}

func TestRetrievalMenuMapsTheRequest(t *testing.T) {
	server, fake := retrievalServerWithFake()

	if _, err := server.Menu(retrievalContext(), &kb.MenuRequest{SpaceId: 7, ParentId: 3, DepthLimit: 2}); err != nil {
		t.Fatalf("Menu: %v", err)
	}

	if fake.menu.spaceID != 7 || fake.menu.parentID != 3 || fake.menu.depth != 2 {
		t.Fatalf("menu = %+v", fake.menu)
	}
}

func TestRetrievalRequiresASession(t *testing.T) {
	server, _ := retrievalServerWithFake()

	calls := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "search",
			call: func(ctx context.Context) error {
				_, err := server.Search(ctx, &kb.SearchRequest{Query: "vpn"})

				return err
			},
		},
		{
			name: "resolve",
			call: func(ctx context.Context) error {
				_, err := server.Resolve(ctx, &kb.ResolveRequest{Ids: []int64{1}})

				return err
			},
		},
		{
			name: "menu",
			call: func(ctx context.Context) error {
				_, err := server.Menu(ctx, &kb.MenuRequest{SpaceId: 7})

				return err
			},
		},
	}

	for _, tt := range calls {
		if err := tt.call(context.Background()); errors.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: error = %v, want Unauthenticated", tt.name, err)
		}
	}
}

func retrievalContext() context.Context {
	return auth.WithSession(context.Background(), modelSession{})
}

func TestSemanticSearchFullPath(t *testing.T) {
	server, fake := retrievalServerWithFake()
	fake.hits = []*model.ChunkHit{
		{ID: 1, ArticleID: 10, VersionID: 4, ChunkIndex: 2, Subject: "VPN", Content: "how to connect", Score: 0.03},
	}

	resp, err := server.SemanticSearch(retrievalContext(), &kb.SemanticSearchRequest{
		Query: "vpn", SpaceIds: []int64{3}, TopK: 5, IncludeCitations: true, Tags: []string{"net"}, TagMatch: kb.TagMatch_TAG_MATCH_ALL,
	})
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	want := model.HybridQuery{
		Term:    "vpn",
		Filter:  model.SearchFilter{SpaceIDs: []int64{3}, Tags: []string{"net"}, TagsMatchAll: true},
		Vectors: []model.ModelVector{},
		TopK:    5,
	}
	if !reflect.DeepEqual(fake.hybrid, want) {
		t.Fatalf("hybrid = %+v, want %+v", fake.hybrid, want)
	}

	chunks := resp.GetChunks()
	if len(chunks) != 1 || chunks[0].GetArticleId() != 10 || chunks[0].GetVersionId() != 4 || chunks[0].GetChunkIndex() != 2 ||
		chunks[0].GetContent() != "how to connect" || chunks[0].GetScore() != 0.03 {
		t.Fatalf("chunks = %+v", chunks)
	}

	citations := resp.GetCitations()
	if len(citations) != 1 || citations[0].GetArticleId() != 10 || citations[0].GetTitle() != "VPN" ||
		citations[0].GetSnippet() != "how to connect" || citations[0].GetUrl() != "" {
		t.Fatalf("citations = %+v", citations)
	}
}

func TestSemanticSearchEmptyIsEmptyNotNil(t *testing.T) {
	server, _ := retrievalServerWithFake()

	resp, err := server.SemanticSearch(retrievalContext(), &kb.SemanticSearchRequest{Query: "vpn", SpaceIds: []int64{3}})
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	if resp.GetChunks() == nil || resp.GetCitations() == nil || len(resp.GetChunks()) != 0 || len(resp.GetCitations()) != 0 {
		t.Fatalf("response = %+v, want empty non-nil slices", resp)
	}
}
