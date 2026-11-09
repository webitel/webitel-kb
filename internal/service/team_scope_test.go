package service

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestTeamSpaces(t *testing.T) {
	tests := []struct {
		name      string
		teamID    int64
		asked     []int64
		bound     []int64
		unknown   bool
		want      []int64
		wantReach bool
		wantAsked int64
		wantCode  codes.Code
	}{
		{name: "no team narrows nothing", asked: []int64{2}, want: []int64{2}, wantReach: true},
		{name: "no team and no spaces reaches every space", wantReach: true},
		{name: "a team without a binding narrows nothing", teamID: 7, asked: []int64{2}, bound: []int64{}, want: []int64{2}, wantReach: true, wantAsked: 7},
		{name: "the binding picks the spaces", teamID: 7, bound: []int64{1, 2}, want: []int64{1, 2}, wantReach: true, wantAsked: 7},
		{name: "asked spaces meet the binding", teamID: 7, asked: []int64{3, 2}, bound: []int64{1, 2}, want: []int64{2}, wantReach: true, wantAsked: 7},
		{name: "asked spaces outside the binding reach nothing", teamID: 7, asked: []int64{3}, bound: []int64{1, 2}, want: []int64{}, wantAsked: 7},
		{name: "an unknown team is refused", teamID: 7, unknown: true, wantAsked: 7, wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spaces := &fakeSpaceStore{teamSpaces: tt.bound, teamUnknown: tt.unknown}

			got, reach, err := teamSpaces(context.Background(), spaces, readOptions{auth: fakeAuther{domainID: 5}}, tt.teamID, tt.asked)

			if errors.Code(err) != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", errors.Code(err), tt.wantCode, err)
			}

			if tt.wantCode == codes.InvalidArgument && errors.ID(err) != "kb.space.team_unknown" {
				t.Errorf("id = %q", errors.ID(err))
			}

			if !slices.Equal(got, tt.want) || reach != tt.wantReach || spaces.teamAsked != tt.wantAsked {
				t.Errorf("spaces = %v reach %v asked team %d, want %v reach %v asked %d",
					got, reach, spaces.teamAsked, tt.want, tt.wantReach, tt.wantAsked)
			}
		})
	}
}

func TestTreeKeepsToTheTeamBinding(t *testing.T) {
	tests := []struct {
		name      string
		teamID    int64
		bound     []int64
		unknown   bool
		wantNodes int
		wantCode  codes.Code
	}{
		{name: "no team reads the tree", wantNodes: 1},
		{name: "a bound space reads the tree", teamID: 7, bound: []int64{3}, wantNodes: 1},
		{name: "a team without a binding reads the tree", teamID: 7, bound: []int64{}, wantNodes: 1},
		{name: "a space outside the binding reads nothing", teamID: 7, bound: []int64{4}},
		{name: "an unknown team is refused", teamID: 7, unknown: true, wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.articles.tree = []*model.TreeNode{{ID: 1}}
			uow.spaces.teamSpaces, uow.spaces.teamUnknown = tt.bound, tt.unknown

			nodes, err := svc.Tree(context.Background(), readOptions{auth: fakeAuther{domainID: 5}}, 3, tt.teamID)
			if errors.Code(err) != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", errors.Code(err), tt.wantCode, err)
			}

			if len(nodes) != tt.wantNodes {
				t.Errorf("nodes = %d, want %d", len(nodes), tt.wantNodes)
			}
		})
	}
}
