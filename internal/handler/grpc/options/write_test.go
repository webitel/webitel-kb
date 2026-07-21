package options

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

func TestWriteOptionsRequireSession(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context) error
	}{
		{"create", func(ctx context.Context) error {
			_, err := NewCreateOptions(ctx)

			return err
		}},
		{"update", func(ctx context.Context) error {
			_, err := NewUpdateOptions(ctx, WithUpdateID(1))

			return err
		}},
		{"delete", func(ctx context.Context) error {
			_, err := NewDeleteOptions(ctx, WithDeleteID(1))

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(context.Background())
			if err == nil {
				t.Fatal("options without a session must fail")
			}
			if got := errors.Code(err); got != codes.Unauthenticated {
				t.Fatalf("error code = %v, want %v", got, codes.Unauthenticated)
			}
		})
	}
}

func TestWriteOptionsRequireID(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, int64) error
	}{
		{"update", func(ctx context.Context, id int64) error {
			_, err := NewUpdateOptions(ctx, WithUpdateID(id))

			return err
		}},
		{"delete", func(ctx context.Context, id int64) error {
			_, err := NewDeleteOptions(ctx, WithDeleteID(id))

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("unset id is rejected", func(t *testing.T) {
				err := tt.call(authorizedContext(), 0)
				if err == nil {
					t.Fatal("must fail without an id")
				}

				if got := errors.Code(err); got != codes.InvalidArgument {
					t.Fatalf("error code = %v, want %v", got, codes.InvalidArgument)
				}
			})
			t.Run("id accepted", func(t *testing.T) {
				if err := tt.call(authorizedContext(), 1); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			})
		})
	}
}

func TestCreateOptions(t *testing.T) {
	opts, err := NewCreateOptions(authorizedContext(),
		WithCreateFields(stubRequest{fields: []string{"ID,name", "name"}}))
	if err != nil {
		t.Fatalf("NewCreateOptions: %v", err)
	}
	if got := opts.GetFields(); !slices.Equal(got, []string{"id", "name"}) {
		t.Errorf("GetFields() = %v, want [id name]", got)
	}
	if opts.GetAuthOpts() == nil {
		t.Fatal("GetAuthOpts() = nil, want the session from context")
	}

	if got := opts.GetAuthOpts().GetUserId(); got != testUserID {
		t.Errorf("GetAuthOpts() carries user %d, want the context session %d", got, testUserID)
	}
}

func TestUpdateOptionsFieldsAndID(t *testing.T) {
	opts, err := NewUpdateOptions(authorizedContext(),
		WithUpdateFields(stubRequest{fields: []string{"id,name"}}),
		WithUpdateID(5),
	)
	if err != nil {
		t.Fatalf("NewUpdateOptions: %v", err)
	}
	if got := opts.GetFields(); !slices.Equal(got, []string{"id", "name"}) {
		t.Errorf("GetFields() = %v, want [id name]", got)
	}
	if got := opts.GetID(); got != 5 {
		t.Errorf("GetID() = %d, want 5", got)
	}
}

func TestDeleteOptionsFieldsAndID(t *testing.T) {
	opts, err := NewDeleteOptions(authorizedContext(),
		WithDeleteFields(stubRequest{fields: []string{"id"}}),
		WithDeleteID(9),
	)
	if err != nil {
		t.Fatalf("NewDeleteOptions: %v", err)
	}
	if got := opts.GetFields(); !slices.Equal(got, []string{"id"}) {
		t.Errorf("GetFields() = %v, want [id]", got)
	}
	if got := opts.GetID(); got != 9 {
		t.Errorf("GetID() = %d, want 9", got)
	}
}
