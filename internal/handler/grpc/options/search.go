package options

import (
	"context"
	"slices"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
	storeutil "github.com/webitel/webitel-kb/internal/store/util"
)

var _ options.Searcher = (*SearchOptions)(nil)

// SearchOption configures SearchOptions.
type SearchOption func(*SearchOptions) error

// SearchOptions is the read-request options implementation.
type SearchOptions struct {
	auth auth.Auther

	fields []string
	search string
	sort   string
	ids    []int64
	page   int
	size   int
}

// NewSearchOptions builds read options for a list request.
func NewSearchOptions(ctx context.Context, opts ...SearchOption) (*SearchOptions, error) {
	session, err := authFromContext(ctx)
	if err != nil {
		return nil, err
	}

	search := &SearchOptions{
		auth: session,
		page: 1,
		size: options.DefaultSearchSize,
	}

	for _, opt := range opts {
		if err := opt(search); err != nil {
			return nil, err
		}
	}

	return search, nil
}

// NewLocateOptions builds read options for a single-entity request.
func NewLocateOptions(ctx context.Context, opts ...SearchOption) (*SearchOptions, error) {
	search, err := NewSearchOptions(ctx, opts...)
	if err != nil {
		return nil, err
	}

	if len(search.ids) != 1 {
		return nil, errors.InvalidArgument(
			"exactly one id is required to locate an entity",
			errors.WithID("options.locate.id_required"),
		)
	}

	return search, nil
}

// WithPagination applies the requested page and size, resolving the defaults:
// an unset page is the first one, an unset size is options.DefaultSearchSize, a
// negative size disables paging and an oversized one is capped at
// options.MaxSearchSize rather than rejected.
func WithPagination(pager Pager) SearchOption {
	return func(s *SearchOptions) error {
		s.page = int(pager.GetPage())
		if s.page <= 0 {
			s.page = 1
		}

		switch size := int(pager.GetSize()); {
		case size < 0:
			s.size = options.UnlimitedSize
			s.page = 1
		case size == 0:
			s.size = options.DefaultSearchSize
		case size > options.MaxSearchSize:
			s.size = options.MaxSearchSize
		default:
			s.size = size
		}

		return nil
	}
}

// WithFields applies the requested output fields. Names are only normalized
// here; the store validates them against its own field metadata, so the allowed
// set is declared in exactly one place.
func WithFields(fielder Fielder) SearchOption {
	return func(s *SearchOptions) error {
		s.fields = storeutil.DeduplicateFields(storeutil.InlineFields(fielder.GetFields()))

		return nil
	}
}

// WithSort applies the requested sort criteria.
func WithSort(sorter Sorter) SearchOption {
	return func(s *SearchOptions) error {
		s.sort = sorter.GetSort()

		return nil
	}
}

// WithSearch applies the requested free-text search term.
func WithSearch(searcher Searcher) SearchOption {
	return func(s *SearchOptions) error {
		s.search = searcher.GetQ()

		return nil
	}
}

// WithIDs applies an id filter. The slice is copied: callers pass fields of the
// incoming request message, and the store must be free to sort or dedupe the
// ids without mutating that message.
func WithIDs(ids []int64) SearchOption {
	return func(s *SearchOptions) error {
		s.ids = slices.Clone(ids)

		return nil
	}
}

// WithID applies a single-id filter.
func WithID(id int64) SearchOption {
	return WithIDs([]int64{id})
}

func (s *SearchOptions) GetAuthOpts() auth.Auther { return s.auth }
func (s *SearchOptions) GetFields() []string      { return s.fields }
func (s *SearchOptions) GetSearch() string        { return s.search }
func (s *SearchOptions) GetPage() int             { return s.page }
func (s *SearchOptions) GetSize() int             { return s.size }
func (s *SearchOptions) GetSort() string          { return s.sort }
func (s *SearchOptions) GetIDs() []int64          { return s.ids }
