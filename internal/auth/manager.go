package auth

import (
	"context"
)

// Manager authorizes an incoming request context into a caller session.
type Manager interface {
	AuthorizeFromContext(ctx context.Context, mainObjClassName string, mainAccessMode AccessMode) (Auther, error)
}
