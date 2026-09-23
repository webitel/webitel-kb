package model

import (
	"slices"
	"testing"
)

func TestBuildTree(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*TreeNode
		want  func(t *testing.T, roots []*TreeNode)
	}{
		{
			name:  "empty input",
			nodes: nil,
			want: func(t *testing.T, roots []*TreeNode) {
				t.Helper()

				if len(roots) != 0 {
					t.Fatalf("roots = %d, want none", len(roots))
				}
			},
		},
		{
			name: "children nest under their parent",
			nodes: []*TreeNode{
				{ID: 1, Subject: "A"},
				{ID: 2, ParentID: 1, Subject: "A1"},
				{ID: 3, ParentID: 2, Subject: "A11"},
				{ID: 4, Subject: "B"},
			},
			want: func(t *testing.T, roots []*TreeNode) {
				t.Helper()

				if len(roots) != 2 || roots[0].ID != 1 || roots[1].ID != 4 {
					t.Fatalf("roots = %+v, want 1 and 4", roots)
				}

				if len(roots[0].Children) != 1 || roots[0].Children[0].ID != 2 {
					t.Fatalf("children of 1 = %+v", roots[0].Children)
				}

				if len(roots[0].Children[0].Children) != 1 || roots[0].Children[0].Children[0].ID != 3 {
					t.Fatalf("grandchildren = %+v", roots[0].Children[0].Children)
				}
			},
		},
		{
			name: "sibling order follows the input",
			nodes: []*TreeNode{
				{ID: 1, Subject: "A"},
				{ID: 3, ParentID: 1, Subject: "second"},
				{ID: 2, ParentID: 1, Subject: "first"},
			},
			want: func(t *testing.T, roots []*TreeNode) {
				t.Helper()

				kids := roots[0].Children
				if len(kids) != 2 || kids[0].ID != 3 || kids[1].ID != 2 {
					t.Fatalf("children = %+v, want input order", kids)
				}
			},
		},
		{
			name: "node whose parent is absent is dropped",
			nodes: []*TreeNode{
				{ID: 1, Subject: "A"},
				{ID: 9, ParentID: 99, Subject: "orphan"},
			},
			want: func(t *testing.T, roots []*TreeNode) {
				t.Helper()

				if len(roots) != 1 || roots[0].ID != 1 {
					t.Fatalf("roots = %+v, want only 1", roots)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, BuildTree(tt.nodes))
		})
	}
}

func TestArticleMerge(t *testing.T) {
	stored := Article{
		ID: 1, Subject: "vpn", Type: ArticleTypeArticle, State: ArticleStateActive,
		Tags: []string{"net"},
	}

	tests := []struct {
		name     string
		in       *Article
		mask     []string
		wantSubj string
		wantTags []string
	}{
		{"unset fields keep stored", &Article{}, nil, "vpn", []string{"net"}},
		{"set fields override", &Article{Subject: "new", Tags: []string{"hr"}}, nil, "new", []string{"hr"}},
		{"unmasked nil tags keep stored", &Article{Subject: "new"}, []string{"subject"}, "new", []string{"net"}},
		{"masked nil tags clear", &Article{}, []string{"tags"}, "vpn", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stored.Merge(tt.in, tt.mask)

			if got.Subject != tt.wantSubj || !slices.Equal(got.Tags, tt.wantTags) {
				t.Fatalf("merged subject %q tags %v, want %q %v", got.Subject, got.Tags, tt.wantSubj, tt.wantTags)
			}

			if got.Type != stored.Type || got.State != stored.State {
				t.Fatalf("merged type %d state %d, want the stored ones", got.Type, got.State)
			}
		})
	}
}
