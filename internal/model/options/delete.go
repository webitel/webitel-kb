package options

import (
	"github.com/webitel/webitel-kb/internal/auth"
)

// Deleter carries the criteria of a delete. Every delete in the API targets
// exactly one entity, so the target is a single id; a delete without one is
// rejected by the constructor rather than removing rows the caller did not name.
type Deleter interface {
	// GetAuthOpts returns the authorized caller session. Never nil.
	GetAuthOpts() auth.Auther

	// GetFields returns the fields to return for the deleted entity.
	GetFields() []string

	// GetID returns the entity to delete; never zero.
	GetID() int64
}
