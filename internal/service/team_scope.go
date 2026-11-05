package service

import (
	"context"
	"slices"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// teamSpaces narrows the asked spaces to the binding of the operator team.
// reach is false when nothing is left to read; ids empty with reach true
// means every space. A team without a binding narrows nothing.
func teamSpaces(
	ctx context.Context, spaces store.SpaceStore, opts options.Searcher, teamID int64, asked []int64,
) (ids []int64, reach bool, err error) {
	if teamID == 0 {
		return asked, true, nil
	}

	bound, found, err := spaces.TeamSpaces(ctx, opts.GetAuthOpts().GetDomainID(), teamID)
	if err != nil {
		return nil, false, err
	}

	if !found {
		return nil, false, errors.InvalidArgument(
			"team is unknown in this domain",
			errors.WithID("kb.retrieval.team_unknown"),
		)
	}

	switch {
	case len(bound) == 0:
		return asked, true, nil
	case len(asked) == 0:
		return bound, true, nil
	}

	met := make([]int64, 0, len(asked))
	for _, id := range asked {
		if slices.Contains(bound, id) {
			met = append(met, id)
		}
	}

	return met, len(met) > 0, nil
}
