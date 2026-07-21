package postgres

import (
	"github.com/jackc/pgx/v5"
)

// collectRows scans every row into the flat record type R by column name and
// maps each record to the domain model. Matching is lax: record fields without
// a matching column keep their zero value, which is how partial field
// selections scan. The rows are closed either way.
func collectRows[R, M any](rows pgx.Rows, mapper func(*R) *M) ([]*M, error) {
	records, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[R])
	if err != nil {
		return nil, err
	}

	models := make([]*M, 0, len(records))
	for _, record := range records {
		models = append(models, mapper(record))
	}

	return models, nil
}

// collectRow scans exactly one row the way collectRows does; no rows surface
// as pgx.ErrNoRows for ParseError to map.
func collectRow[R, M any](rows pgx.Rows, mapper func(*R) *M) (*M, error) {
	record, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[R])
	if err != nil {
		return nil, err
	}

	return mapper(record), nil
}
