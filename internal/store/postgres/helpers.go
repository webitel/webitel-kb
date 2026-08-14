package postgres

import (
	"slices"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// errLocateSingleID rejects a Locate whose options do not name exactly one id.
// The options constructor enforces this too; the stores re-check so a misbuilt
// caller cannot fetch an arbitrary row (no ids) or turn a multi-row result
// into an internal error.
var errLocateSingleID = errors.InvalidArgument(
	"exactly one id is required to locate an entity",
	errors.WithID("store.pg.locate_id"),
)

// containsSortField reports whether any "+field"/"-field" criterion names the
// field.
func containsSortField(sorts []string, field string) bool {
	return slices.ContainsFunc(sorts, func(s string) bool { return s[1:] == field })
}

// nullIfEmpty maps the zero value to NULL, keeping optional columns NULL
// instead of storing empty strings or zeroes.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func nullIfZero[T int32 | int64](v T) *T {
	if v == 0 {
		return nil
	}

	return &v
}

// nonNilSlice keeps a NOT NULL array column from receiving NULL.
func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return make([]T, 0)
	}

	return s
}

// defaultIfEmpty lets the database default fill a column the caller left empty.
func defaultIfEmpty(s string) any {
	if s == "" {
		return squirrel.Expr("DEFAULT")
	}

	return s
}
