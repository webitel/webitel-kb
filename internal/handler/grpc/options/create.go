package options

import (
	"context"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
	storeutil "github.com/webitel/webitel-kb/internal/store/util"
)

var _ options.Creator = (*CreateOptions)(nil)

// CreateOption configures CreateOptions.
type CreateOption func(*CreateOptions) error

// CreateOptions is the create-request options implementation.
type CreateOptions struct {
	auth auth.Auther

	fields []string
}

// NewCreateOptions builds write options for a create request.
func NewCreateOptions(ctx context.Context, opts ...CreateOption) (*CreateOptions, error) {
	session, err := authFromContext(ctx)
	if err != nil {
		return nil, err
	}

	create := &CreateOptions{auth: session}

	for _, opt := range opts {
		if err := opt(create); err != nil {
			return nil, err
		}
	}

	return create, nil
}

// WithCreateFields applies the fields to return for the created entity.
func WithCreateFields(fielder Fielder) CreateOption {
	return func(c *CreateOptions) error {
		c.fields = storeutil.DeduplicateFields(storeutil.InlineFields(fielder.GetFields()))

		return nil
	}
}

func (c *CreateOptions) GetAuthOpts() auth.Auther { return c.auth }
func (c *CreateOptions) GetFields() []string      { return c.fields }
