package options

import (
	"github.com/webitel/webitel-kb/internal/auth"
)

// Updator carries the criteria of a write to an existing entity.
type Updator interface {
	// GetAuthOpts returns the authorized caller session. Never nil.
	GetAuthOpts() auth.Auther

	// GetFields returns the fields to return for the updated entity.
	GetFields() []string

	// GetID returns the entity to update; never zero.
	GetID() int64
}
