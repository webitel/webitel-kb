package postgres

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"
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
	// The read-back must carry no filters, so it renders no placeholders;
	// anything else would clash with the write's $N numbering.
	if len(readArgs) != 0 {
		return nil, errors.Internal(
			"storage error",
			errors.WithID("store.pg.cte_read_back_args"),
		)
	}

	rows, err := db.Query(ctx, "WITH m AS ("+writeSQL+") "+readSQL, writeArgs...)
	if err != nil {
		return nil, ParseError(err)
	}

	item, err := collectRow(rows, mapper)
	if err != nil {
		return nil, ParseError(err)
	}

	return item, nil
}
