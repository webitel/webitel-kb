package postgres

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
)

// accessOpts asks for one row in the given mode.
type accessOpts struct {
	fakeSearchOpts

	access auth.AccessMode
}

func (o *accessOpts) GetAccess() auth.AccessMode { return o.access }

func TestChangeMiss(t *testing.T) {
	type row struct{}

	tests := []struct {
		name     string
		mode     auth.AccessMode
		err      error
		readable bool
		wantCode codes.Code
		wantID   string
		wantRead bool
	}{
		{name: "a read miss stays not found", mode: auth.Read, err: pgx.ErrNoRows, wantCode: codes.NotFound},
		{
			name: "a change of a row the caller sees is refused", mode: auth.Edit, err: pgx.ErrNoRows, readable: true,
			wantCode: codes.PermissionDenied, wantID: "kb.article.access_denied", wantRead: true,
		},
		{
			name: "a change of a row the caller cannot see stays not found", mode: auth.Delete, err: pgx.ErrNoRows,
			wantCode: codes.NotFound, wantRead: true,
		},
		{name: "another failure passes", mode: auth.Edit, err: stderrors.New("boom"), wantCode: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				read     bool
				readMode auth.AccessMode
			)

			reader := func(_ context.Context, opts options.Searcher) (*row, error) {
				read, readMode = true, opts.GetAccess()

				if !tt.readable {
					return nil, ParseError(pgx.ErrNoRows)
				}

				return &row{}, nil
			}

			opts := &accessOpts{access: tt.mode}
			err := changeMiss(context.Background(), opts, tt.err, errArticleAccessDenied, reader)

			if errors.Code(err) != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", errors.Code(err), tt.wantCode, err)
			}

			if tt.wantID != "" && errors.ID(err) != tt.wantID {
				t.Errorf("id = %q, want %q", errors.ID(err), tt.wantID)
			}

			if read != tt.wantRead || (read && readMode != auth.Read) {
				t.Errorf("read again = %v in %v, want %v for reading", read, readMode, tt.wantRead)
			}
		})
	}
}
