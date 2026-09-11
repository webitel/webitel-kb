package service

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// retrievalStoreFake records what reached the store and plays back summaries.
type retrievalStoreFake struct {
	calls  int
	filter model.SearchFilter
	term   string
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
	f.calls++
	f.hybrid = q

	return f.hits, f.hitErr
}

func (f *retrievalStoreFake) Search(
	_ context.Context, opts options.Searcher, filter model.SearchFilter,
) ([]*model.ArticleSummary, bool, error) {
	f.calls++
	f.filter = filter
	f.term = opts.GetSearch()

	return f.items, f.next, nil
}

func (f *retrievalStoreFake) Resolve(
	_ context.Context, _ options.Searcher, ids, spaceIDs []int64,
) ([]*model.ArticleSummary, error) {
	f.calls++
	f.ids = ids
	f.spaces = spaceIDs

	return f.items, nil
}

func (f *retrievalStoreFake) Menu(
	_ context.Context, _ options.Searcher, spaceID, parentID int64, depthLimit int32,
) ([]*model.ArticleSummary, error) {
	f.calls++
	f.menu.spaceID, f.menu.parentID, f.menu.depth = spaceID, parentID, depthLimit

	return f.items, nil
}

// retrievalUow hands out the retrieval and space fakes; a transaction is a pass-through.
type retrievalUow struct {
	store.UnitOfWork

	retrieval *retrievalStoreFake
	spaces    *fakeSpaceStore
	txCalls   int
}

func (u *retrievalUow) RetrievalStore() store.RetrievalStore { return u.retrieval }
func (u *retrievalUow) SpaceStore() store.SpaceStore         { return u.spaces }

func (u *retrievalUow) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	u.txCalls++

	return fn(ctx, u)
}

// readOpts is the read-request options of a retrieval call.
type readOpts struct {
	auth   auth.Auther
	search string
	ids    []int64
	size   int
	page   int
}

func (o *readOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *readOpts) GetFields() []string      { return nil }
func (o *readOpts) GetSearch() string        { return o.search }
func (o *readOpts) GetPage() int             { return o.page }
func (o *readOpts) GetSize() int             { return o.size }
func (o *readOpts) GetSort() string          { return "" }
func (o *readOpts) GetIDs() []int64          { return o.ids }

func retrievalServiceWithFake() (*RetrievalService, *retrievalStoreFake) {
	svc, uow, _ := semanticServiceWithFakes()

	return svc, uow.retrieval
}

func TestRetrievalSearchAnswersABlankQueryItself(t *testing.T) {
	// A blank query matches nothing by construction.
	for _, term := range []string{"", "   ", "\t\n"} {
		svc, fake := retrievalServiceWithFake()

		items, next, err := svc.Search(
			context.Background(),
			&readOpts{auth: fakeAuther{domainID: 5}, search: term, size: 10, page: 1},
			model.SearchFilter{},
		)
		if err != nil {
			t.Fatalf("term %q: Search: %v", term, err)
		}

		if len(items) != 0 || next || fake.calls != 0 {
			t.Fatalf("term %q: items = %v, next = %v, store calls = %d", term, items, next, fake.calls)
		}
	}
}

func TestRetrievalSearchPassesTheCriteria(t *testing.T) {
	svc, fake := retrievalServiceWithFake()
	fake.items = []*model.ArticleSummary{{ID: 1}}
	fake.next = true

	filter := model.SearchFilter{SpaceIDs: []int64{7}, Tags: []string{"vpn"}, TagsMatchAll: true}

	items, next, err := svc.Search(
		context.Background(),
		&readOpts{auth: fakeAuther{domainID: 5}, search: "reset", size: 10, page: 1},
		filter,
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(items) != 1 || !next {
		t.Fatalf("items = %v, next = %v", items, next)
	}

	if fake.term != "reset" || !reflect.DeepEqual(fake.filter, filter) {
		t.Fatalf("term = %q, filter = %+v", fake.term, fake.filter)
	}
}

func TestRetrievalResolve(t *testing.T) {
	tests := []struct {
		name      string
		ids       []int64
		wantCalls int
		wantCode  codes.Code
	}{
		{name: "nothing asked is nothing read", ids: nil, wantCalls: 0},
		{name: "ids reach the store", ids: []int64{3, 1}, wantCalls: 1},
		{name: "an unbounded batch is refused", ids: make([]int64, options.MaxSearchSize+1), wantCalls: 0, wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, fake := retrievalServiceWithFake()

			items, err := svc.Resolve(
				context.Background(),
				&readOpts{auth: fakeAuther{domainID: 5}, ids: tt.ids, size: options.UnlimitedSize, page: 1},
				[]int64{7},
			)

			if got := errors.Code(err); got != tt.wantCode {
				t.Fatalf("error = %v, want code %v", err, tt.wantCode)
			}

			if fake.calls != tt.wantCalls {
				t.Fatalf("store calls = %d, want %d", fake.calls, tt.wantCalls)
			}

			if tt.wantCalls == 0 {
				if len(items) != 0 {
					t.Fatalf("items = %v, want none", items)
				}

				return
			}

			if !reflect.DeepEqual(fake.ids, tt.ids) || !reflect.DeepEqual(fake.spaces, []int64{7}) {
				t.Fatalf("ids = %v, spaces = %v", fake.ids, fake.spaces)
			}
		})
	}
}

func TestRetrievalMenuRequiresASpace(t *testing.T) {
	svc, fake := retrievalServiceWithFake()

	if _, err := svc.Menu(context.Background(), &readOpts{auth: fakeAuther{domainID: 5}}, 0, 0, 1); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("error = %v, want InvalidArgument", err)
	}

	if fake.calls != 0 {
		t.Fatalf("store calls = %d, want none", fake.calls)
	}
}

func TestRetrievalMenuDepth(t *testing.T) {
	tests := []struct {
		name  string
		asked int32
		want  int32
	}{
		{name: "unset is one level", asked: 0, want: 1},
		{name: "negative is one level", asked: -3, want: 1},
		{name: "asked depth is kept", asked: 3, want: 3},
		{name: "no menu outgrows the hierarchy", asked: 9, want: model.MaxArticleDepth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, fake := retrievalServiceWithFake()

			if _, err := svc.Menu(
				context.Background(),
				&readOpts{auth: fakeAuther{domainID: 5}},
				7, 3, tt.asked,
			); err != nil {
				t.Fatalf("Menu: %v", err)
			}

			if fake.menu.depth != tt.want || fake.menu.spaceID != 7 || fake.menu.parentID != 3 {
				t.Fatalf("menu = %+v, want depth %d of space 7 under 3", fake.menu, tt.want)
			}
		})
	}
}
