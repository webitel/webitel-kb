package postgres

import (
	"context"
	stderrors "errors"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model/options"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

var (
	errArticleAccessDenied = errors.Forbidden(
		"the article rules do not allow this change",
		errors.WithID("kb.article.access_denied"),
	)
	errSpaceAccessDenied = errors.Forbidden(
		"the space rules do not allow this change",
		errors.WithID("kb.space.access_denied"),
	)
)

// readerOf asks for the same row as opts, for reading.
type readerOf struct{ options.Searcher }

func (readerOf) GetAccess() auth.AccessMode { return auth.Read }

// isChange reports whether opts locate a row to change.
func isChange(opts options.Searcher) bool {
	mode := opts.GetAccess()

	return mode != auth.NONE && mode != auth.Read
}

// readPlan plans the checks of an article read.
func readPlan(opts options.Searcher) queryobject.AccessPlan {
	return queryobject.PlanArticleAccess(opts.GetAuthOpts(), auth.Read)
}

// changeMiss explains a row a change did not find: one the caller may read is
// refused, any other stays not found.
func changeMiss[M any](
	ctx context.Context, opts options.Searcher, err, denied error,
	read func(context.Context, options.Searcher) (*M, error),
) error {
	if !stderrors.Is(err, pgx.ErrNoRows) || !isChange(opts) {
		return ParseError(err)
	}

	if _, readErr := read(ctx, readerOf{opts}); readErr != nil {
		return readErr
	}

	return denied
}
