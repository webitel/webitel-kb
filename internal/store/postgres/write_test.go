package postgres

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// A read-back carrying its own placeholders would clash with the write's $N
// numbering: cteReadBack must reject it before anything reaches the database.
func TestCteReadBackRejectsFilteredRead(t *testing.T) {
	f := &fakeQuerier{}

	_, err := cteReadBack(
		context.Background(), f,
		"UPDATE kb.space SET name = $1", []any{"docs"},
		"SELECT m.id AS id FROM m WHERE m.id = $1", []any{int64(7)},
		mapSpace,
	)

	if got := errors.Code(err); got != codes.Internal {
		t.Fatalf("error code = %v, want %v", got, codes.Internal)
	}

	if len(f.sqls) != 0 {
		t.Fatalf("statement reached the database: %q", f.sqls)
	}
}
