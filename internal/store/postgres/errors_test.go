package postgres

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

func TestParseError(t *testing.T) {
	tests := []struct {
		name     string
		in       error
		wantCode codes.Code
	}{
		{"nil passes through", nil, codes.OK},
		{"no rows is not found", pgx.ErrNoRows, codes.NotFound},
		{
			"wrapped no rows is not found",
			fmt.Errorf("scan: %w", pgx.ErrNoRows),
			codes.NotFound,
		},
		{
			"unique violation",
			&pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "space_name_uq"},
			codes.AlreadyExists,
		},
		{
			"foreign key violation",
			&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation, ConstraintName: "space_model_fk", TableName: "space"},
			codes.Aborted,
		},
		{
			"check violation",
			&pgconn.PgError{Code: pgerrcode.CheckViolation, ConstraintName: "dim_check"},
			codes.Aborted,
		},
		{
			"not null violation",
			&pgconn.PgError{Code: pgerrcode.NotNullViolation, TableName: "space", ColumnName: "name"},
			codes.Aborted,
		},
		{
			"unknown pg error is internal",
			&pgconn.PgError{Code: pgerrcode.SerializationFailure},
			codes.Internal,
		},
		{
			"non-pg error is internal",
			stderrors.New("connection reset"),
			codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseError(tt.in)

			if tt.in == nil {
				if got != nil {
					t.Fatalf("ParseError(nil) = %v, want nil", got)
				}

				return
			}

			if got == nil {
				t.Fatal("ParseError returned nil for a non-nil error")
			}

			if code := errors.Code(got); code != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", code, tt.wantCode, got)
			}
		})
	}
}

func TestParseErrorKeepsCause(t *testing.T) {
	pgErr := &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "space_name_uq"}

	got := ParseError(pgErr)

	var cause *pgconn.PgError
	if !stderrors.As(got, &cause) || cause.ConstraintName != "space_name_uq" {
		t.Fatalf("driver error lost as cause: %v", got)
	}
}
