package options

import (
	"github.com/webitel/webitel-kb/internal/auth"
)

// Creator carries the criteria of a write of a new entity.
type Creator interface {
	// GetAuthOpts returns the authorized caller session. Never nil.
	GetAuthOpts() auth.Auther

	// GetFields returns the fields to return for the created entity.
	GetFields() []string
}
