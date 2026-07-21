package postgres

import (
	stderrors "errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// ParseError maps a database error to a coded application error. Callers
// branch on errors.Code; the driver error stays attached as the cause.
// Context comes from the structured pgconn fields — the human-readable Detail
// text is locale-dependent and is never parsed.
func ParseError(err error) error {
	if err == nil {
		return nil
	}

	// Not-found deliberately does not reveal whether the row exists but is
	// inaccessible: every read is domain-scoped, and the two cases must be
	// indistinguishable to the caller.
	if stderrors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(
			"entity does not exist or access is denied",
			errors.WithID("store.pg.not_found"),
			errors.WithCause(err),
		)
	}

	var pgErr *pgconn.PgError
	if stderrors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgerrcode.UniqueViolation:
			return errors.New(
				"invalid input: entity already exists",
				errors.WithCode(codes.AlreadyExists),
				errors.WithID("store.pg.unique"),
				errors.WithCause(err),
				errors.WithValue("constraint", pgErr.ConstraintName),
			)
		case pgerrcode.ForeignKeyViolation:
			return errors.Aborted(
				"invalid input: referenced entity does not exist or is still referenced",
				errors.WithID("store.pg.foreign_key"),
				errors.WithCause(err),
				errors.WithValue("constraint", pgErr.ConstraintName),
				errors.WithValue("table", pgErr.TableName),
			)
		case pgerrcode.CheckViolation:
			return errors.Aborted(
				"invalid input: value violates a constraint",
				errors.WithID("store.pg.check"),
				errors.WithCause(err),
				errors.WithValue("constraint", pgErr.ConstraintName),
			)
		case pgerrcode.NotNullViolation:
			return errors.Aborted(
				"invalid input: required value is missing",
				errors.WithID("store.pg.not_null"),
				errors.WithCause(err),
				errors.WithValue("column", pgErr.TableName+"."+pgErr.ColumnName),
			)
		}
	}

	return errors.Internal(
		"storage error",
		errors.WithID("store.pg.internal"),
		errors.WithCause(err),
	)
}
