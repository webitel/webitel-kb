// Package options builds the store-layer request options from an incoming gRPC
// request and the caller session the auth interceptor put on the context.
//
// Every constructor follows the same order: resolve the caller, apply the
// functional options, then post-validate.
package options

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
)

// Pager describes a request carrying paging criteria.
type Pager interface {
	GetPage() int32
	GetSize() int32
}

// Sorter describes a request carrying sort criteria.
type Sorter interface {
	GetSort() string
}

// Searcher describes a request carrying a free-text search term.
type Searcher interface {
	GetQ() string
}

// Fielder describes a request carrying an output field selection.
type Fielder interface {
	GetFields() []string
}

// authFromContext resolves the caller session the auth interceptor stored on the
// context.
func authFromContext(ctx context.Context) (auth.Auther, error) {
	session, ok := auth.FromContext(ctx)
	if !ok {
		return nil, errors.Unauthenticated(
			"unauthorized",
			errors.WithID("options.auth.session_missing"),
		)
	}

	return session, nil
}
