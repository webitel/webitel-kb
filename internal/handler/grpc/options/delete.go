package options

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
	storeutil "github.com/webitel/webitel-kb/internal/store/util"
)

var _ options.Deleter = (*DeleteOptions)(nil)

// DeleteOption configures DeleteOptions.
type DeleteOption func(*DeleteOptions) error

// DeleteOptions is the delete-request options implementation.
type DeleteOptions struct {
	auth auth.Auther

	fields []string
	id     int64
}

// NewDeleteOptions builds write options for a delete request.
func NewDeleteOptions(ctx context.Context, opts ...DeleteOption) (*DeleteOptions, error) {
	session, err := authFromContext(ctx)
	if err != nil {
		return nil, err
	}

	del := &DeleteOptions{auth: session}

	for _, opt := range opts {
		if err := opt(del); err != nil {
			return nil, err
		}
	}

	if del.id == 0 {
		return nil, errors.InvalidArgument(
			"id is required to delete an entity",
			errors.WithID("options.delete.id_required"),
		)
	}

	return del, nil
}

// WithDeleteFields applies the fields to return for the deleted entity.
func WithDeleteFields(fielder Fielder) DeleteOption {
	return func(d *DeleteOptions) error {
		d.fields = storeutil.DeduplicateFields(storeutil.InlineFields(fielder.GetFields()))

		return nil
	}
}

// WithDeleteID applies the id of the entity to delete.
func WithDeleteID(id int64) DeleteOption {
	return func(d *DeleteOptions) error {
		d.id = id

		return nil
	}
}

func (d *DeleteOptions) GetAuthOpts() auth.Auther { return d.auth }
func (d *DeleteOptions) GetFields() []string      { return d.fields }
func (d *DeleteOptions) GetID() int64             { return d.id }
