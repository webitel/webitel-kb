package model

import "testing"

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
