package options

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
	storeutil "github.com/webitel/webitel-kb/internal/store/util"
)

var _ options.Updator = (*UpdateOptions)(nil)

// UpdateOption configures UpdateOptions.
type UpdateOption func(*UpdateOptions) error

// UpdateOptions is the update-request options implementation.
type UpdateOptions struct {
	auth auth.Auther

	fields []string
	id     int64
}

// NewUpdateOptions builds write options for an update request. The target id
// must be selected.
func NewUpdateOptions(ctx context.Context, opts ...UpdateOption) (*UpdateOptions, error) {
	session, err := authFromContext(ctx)
	if err != nil {
		return nil, err
	}

	update := &UpdateOptions{auth: session}

	for _, opt := range opts {
		if err := opt(update); err != nil {
			return nil, err
		}
	}

	if update.id == 0 {
		return nil, errors.InvalidArgument(
			"id is required to update an entity",
			errors.WithID("options.update.id_required"),
		)
	}

	return update, nil
}

// WithUpdateFields applies the fields to return for the updated entity.
func WithUpdateFields(fielder Fielder) UpdateOption {
	return func(u *UpdateOptions) error {
		u.fields = storeutil.DeduplicateFields(storeutil.InlineFields(fielder.GetFields()))

		return nil
	}
}

// WithUpdateID applies the id of the entity to update.
func WithUpdateID(id int64) UpdateOption {
	return func(u *UpdateOptions) error {
		u.id = id

		return nil
	}
}

func (u *UpdateOptions) GetAuthOpts() auth.Auther { return u.auth }
func (u *UpdateOptions) GetFields() []string      { return u.fields }
func (u *UpdateOptions) GetID() int64             { return u.id }
