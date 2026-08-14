package postgres

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// errCTEReadArgs rejects a read rendered over a CTE that carries its own
// placeholders, the $N numbering belongs to the statement the CTE wraps.
var errCTEReadArgs = errors.Internal(
	"storage error",
	errors.WithID("store.pg.cte_read_back_args"),
)

// cteReadBack wraps a write statement into a CTE named m — the alias entity
// query objects reference — and reads the written row back through the given
// SELECT in the same statement: atomic, and rendered by the exact code path
// every read uses. A write whose WHERE matched nothing (foreign domain, absent
// row) reads back zero rows and surfaces as not-found.
func cteReadBack[R, M any](
	ctx context.Context, db Querier,
	writeSQL string, writeArgs []any,
	readSQL string, readArgs []any,
	mapper func(*R) *M,
) (*M, error) {
	return readBackCTEs(ctx, db, "WITH m AS ("+writeSQL+") ", writeArgs, readSQL, readArgs, mapper)
}

// readBackCTEs reads a written row back through readSQL appended to a
// caller-composed CTE prefix whose last CTE is named m, serving writes that
// need more than one CTE (a recursive cascade).
func readBackCTEs[R, M any](
	ctx context.Context, db Querier,
	ctes string, writeArgs []any,
	readSQL string, readArgs []any,
	mapper func(*R) *M,
) (*M, error) {
	if len(readArgs) != 0 {
		return nil, errCTEReadArgs
	}

	rows, err := db.Query(ctx, ctes+readSQL, writeArgs...)
	if err != nil {
		return nil, ParseError(err)
	}

	item, err := collectRow(rows, mapper)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}
