package options

import (
	"github.com/webitel/webitel-kb/internal/auth"
)

const (
	// DefaultSearchSize is the page size applied when the caller does not ask for one.
	DefaultSearchSize = 10

	// MaxSearchSize caps an explicitly requested page size, so a single request
	// cannot ask the database for an unbounded result set.
	MaxSearchSize = 5000

	// UnlimitedSize is the size value that disables paging entirely. Callers opt
	// in by requesting a negative size; the page is then always the first one.
	UnlimitedSize = -1
)

// Searcher carries the criteria of a read request: the authorized caller, the
// requested output fields, and the paging, sorting and filtering criteria.
type Searcher interface {
	// GetAuthOpts returns the authorized caller session. Never nil: options
	// constructors reject a request without one.
	GetAuthOpts() auth.Auther

	// GetFields returns the requested output fields. Field names are not
	// validated here; the store rejects unknown ones against its own metadata.
	GetFields() []string

	// GetSearch returns the free-text search term, empty when not requested.
	GetSearch() string

	// GetPage returns the 1-based page number. Always 1 when GetSize reports
	// UnlimitedSize, so a store can compute (page-1)*size unconditionally.
	GetPage() int

	// GetSize returns the page size, or UnlimitedSize to disable paging.
	GetSize() int

	// GetSort returns the sort criteria, empty when not requested.
	GetSort() string

	// GetIDs returns the id filter, empty when not requested. The slice is owned
	// by the options, so a store may sort or dedupe it in place.
	GetIDs() []int64
}
