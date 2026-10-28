package postgres

import (
	"context"
	stderrors "errors"

	"github.com/jackc/pgx/v5"
)

// settingValueSQL reads one system setting of a domain.
const settingValueSQL = `SELECT s.value FROM call_center.system_settings s WHERE s.domain_id = $1 AND s.name = $2`

type settingStore struct {
	db Querier
}

func (s *settingStore) Value(ctx context.Context, domainID int64, name string) ([]byte, error) {
	var value []byte

	err := s.db.QueryRow(ctx, settingValueSQL, domainID, name).Scan(&value)
	if stderrors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, ParseError(err)
	}

	return value, nil
}
