package options

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
)

// stubAuther is a minimal Auther carrying an identity, so tests can assert that
// the session reaching the options is the very one on the context.
type stubAuther struct {
	auth.Auther

	userID int64
}

func (s stubAuther) GetUserId() int64 { return s.userID }

const testUserID int64 = 42

// stubRequest stands in for a generated List*Request: it satisfies Pager,
// Sorter, Searcher and Fielder structurally, exactly as the real ones do.
type stubRequest struct {
	page   int32
	size   int32
	sort   string
	q      string
	fields []string
}

func (r stubRequest) GetPage() int32      { return r.page }
func (r stubRequest) GetSize() int32      { return r.size }
func (r stubRequest) GetSort() string     { return r.sort }
func (r stubRequest) GetQ() string        { return r.q }
func (r stubRequest) GetFields() []string { return r.fields }

func authorizedContext() context.Context {
	return auth.WithSession(context.Background(), stubAuther{userID: testUserID})
}

func TestNewSearchOptionsRequiresSession(t *testing.T) {
	_, err := NewSearchOptions(context.Background())
	if err == nil {
		t.Fatal("NewSearchOptions without a session must fail")
	}
	if got := errors.Code(err); got != codes.Unauthenticated {
		t.Fatalf("error code = %v, want %v", got, codes.Unauthenticated)
	}
}

func TestWithPagination(t *testing.T) {
	tests := []struct {
		name     string
		page     int32
		size     int32
		wantPage int
		wantSize int
	}{
		{"defaults when unset", 0, 0, 1, options.DefaultSearchSize},
		{"explicit values pass through", 3, 25, 3, 25},
		{"negative page falls back to first", -2, 25, 1, 25},
		{"negative size disables paging", 1, -1, 1, options.UnlimitedSize},
		{"oversized is capped, not rejected", 1, options.MaxSearchSize + 1, 1, options.MaxSearchSize},
		{"max size is allowed", 1, options.MaxSearchSize, 1, options.MaxSearchSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewSearchOptions(authorizedContext(),
				WithPagination(stubRequest{page: tt.page, size: tt.size}))
			if err != nil {
				t.Fatalf("NewSearchOptions: %v", err)
			}
			if got := opts.GetPage(); got != tt.wantPage {
				t.Errorf("GetPage() = %d, want %d", got, tt.wantPage)
			}
			if got := opts.GetSize(); got != tt.wantSize {
				t.Errorf("GetSize() = %d, want %d", got, tt.wantSize)
			}
		})
	}
}

func TestWithIDsDoesNotAliasCallerSlice(t *testing.T) {
	// Handlers pass a field of the request message straight in; the store must be
	// able to sort or dedupe the ids without corrupting that message.
	caller := []int64{3, 1, 2}

	opts, err := NewSearchOptions(authorizedContext(), WithIDs(caller))
	if err != nil {
		t.Fatalf("NewSearchOptions: %v", err)
	}

	slices.Sort(opts.GetIDs())

	if !slices.Equal(caller, []int64{3, 1, 2}) {
		t.Fatalf("caller slice mutated to %v; WithIDs must copy", caller)
	}
}

func TestUnlimitedSizeResetsPage(t *testing.T) {
	// With paging disabled there is no further page, and a leftover page would
	// have the store compute a negative offset from (page-1)*size.
	opts, err := NewSearchOptions(authorizedContext(),
		WithPagination(stubRequest{page: 3, size: -1}))
	if err != nil {
		t.Fatalf("NewSearchOptions: %v", err)
	}

	if got := opts.GetSize(); got != options.UnlimitedSize {
		t.Fatalf("GetSize() = %d, want %d", got, options.UnlimitedSize)
	}

	if got := opts.GetPage(); got != 1 {
		t.Fatalf("GetPage() = %d, want 1 when paging is disabled", got)
	}
}

func TestSearchOptionsDefaultsWithoutBuilders(t *testing.T) {
	opts, err := NewSearchOptions(authorizedContext())
	if err != nil {
		t.Fatalf("NewSearchOptions: %v", err)
	}
	if got := opts.GetPage(); got != 1 {
		t.Errorf("GetPage() = %d, want 1", got)
	}
	if got := opts.GetSize(); got != options.DefaultSearchSize {
		t.Errorf("GetSize() = %d, want %d", got, options.DefaultSearchSize)
	}
}

func TestWithFields(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty stays empty", nil, []string{}},
		{"plain list", []string{"id", "name"}, []string{"id", "name"}},
		{"comma packed", []string{"id,name"}, []string{"id", "name"}},
		{"space packed", []string{"id name"}, []string{"id", "name"}},
		{"lowercased", []string{"ID", "Name"}, []string{"id", "name"}},
		{"deduplicated", []string{"id", "id,name", "name"}, []string{"id", "name"}},
		{"blanks dropped", []string{"id,,  ,name"}, []string{"id", "name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewSearchOptions(authorizedContext(), WithFields(stubRequest{fields: tt.in}))
			if err != nil {
				t.Fatalf("NewSearchOptions: %v", err)
			}
			if got := opts.GetFields(); !slices.Equal(got, tt.want) {
				t.Fatalf("GetFields() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearchOptionsPassThrough(t *testing.T) {
	opts, err := NewSearchOptions(authorizedContext(),
		WithSort(stubRequest{sort: "-created_at"}),
		WithSearch(stubRequest{q: "hello"}),
		WithIDs([]int64{1, 2}),
	)
	if err != nil {
		t.Fatalf("NewSearchOptions: %v", err)
	}
	if got := opts.GetSort(); got != "-created_at" {
		t.Errorf("GetSort() = %q, want %q", got, "-created_at")
	}
	if got := opts.GetSearch(); got != "hello" {
		t.Errorf("GetSearch() = %q, want %q", got, "hello")
	}
	if got := opts.GetIDs(); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("GetIDs() = %v, want [1 2]", got)
	}
	if opts.GetAuthOpts() == nil {
		t.Fatal("GetAuthOpts() = nil, want the session from context")
	}

	if got := opts.GetAuthOpts().GetUserId(); got != testUserID {
		t.Errorf("GetAuthOpts() carries user %d, want the context session %d", got, testUserID)
	}
}

func TestNewLocateOptions(t *testing.T) {
	tests := []struct {
		name    string
		ids     []int64
		wantErr bool
	}{
		{"exactly one id", []int64{7}, false},
		{"no ids", nil, true},
		{"more than one id", []int64{1, 2}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLocateOptions(authorizedContext(), WithIDs(tt.ids))

			if tt.wantErr {
				if err == nil {
					t.Fatal("NewLocateOptions must fail")
				}

				if got := errors.Code(err); got != codes.InvalidArgument {
					t.Fatalf("error code = %v, want %v", got, codes.InvalidArgument)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewLocateOptions: %v", err)
			}
		})
	}
}
