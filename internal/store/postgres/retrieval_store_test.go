package postgres

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestRetrievalSearchRendersRankedPage(t *testing.T) {
	f := &fakeQuerier{}
	s := &retrievalStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, search: "vpn", size: 10, page: 2}
	filter := model.SearchFilter{SpaceIDs: []int64{7}, Tags: []string{"net"}, TagsMatchAll: true}

	if _, _, err := s.Search(context.Background(), opts, filter); err != nil {
		t.Fatalf("Search: %v", err)
	}

	for _, want := range []string{
		"WITH h AS (SELECT id, sum(rank) AS rank FROM (",
		"UNION ALL",
		"s.domain_id = $3",
		"m.deleted_at IS NULL",
		"m.state = $4",
		"m.space_id = ANY($5)",
		"m.tags @> $6",
		"GROUP BY id ORDER BY rank DESC, id LIMIT 11 OFFSET 10",
		// The fragment is built outside the ranked query.
		"ts_headline('simple', v.body_plain, websearch_to_tsquery('simple', $13)",
		"FROM h JOIN kb.article m ON m.id = h.id",
		"ORDER BY h.rank DESC, m.id",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL does not contain %q", want)
		}
	}

	if f.gotArgs[0] != "vpn" || f.gotArgs[2] != int64(5) || f.gotArgs[len(f.gotArgs)-1] != "vpn" {
		t.Fatalf("args = %v, want the term around the scope of each branch", f.gotArgs)
	}
}

func TestRetrievalSearchReportsNextPage(t *testing.T) {
	// The row past the page answers whether more exists.
	f := &fakeQuerier{rows: summaryRows(3)}
	s := &retrievalStore{db: f}

	items, next, err := s.Search(
		context.Background(),
		&fakeSearchOpts{auth: fakeAuther{domainID: 5}, search: "vpn", size: 2, page: 1},
		model.SearchFilter{},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(items) != 2 || !next {
		t.Fatalf("items = %d, next = %v, want 2 and true", len(items), next)
	}
}

func TestRetrievalResolveKeepsTheAskedOrder(t *testing.T) {
	f := &fakeQuerier{}
	s := &retrievalStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: -1, page: 1}

	if _, err := s.Resolve(context.Background(), opts, []int64{9, 4}, []int64{7}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, want := range []string{
		"s.domain_id=$2",
		"m.deleted_at IS NULL",
		"m.state=$3",
		"m.id=ANY($4)",
		"m.space_id=ANY($5)",
		"ORDER BY array_position($6::bigint[],m.id)",
		"COALESCE(left(v.body_plain,$1),'')AS snippet",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// A batch lookup ranks nothing.
	if strings.Contains(f.gotSQL, "ts_headline") || strings.Contains(f.gotSQL, "rank") {
		t.Errorf("SQL %q ranks a batch lookup", f.gotSQL)
	}
}

func TestRetrievalMenuWalksTheRequestedDepth(t *testing.T) {
	tests := []struct {
		name     string
		parentID int64
		want     []string
		absent   []string
	}{
		{
			name:     "children of an article",
			parentID: 3,
			want:     []string{"c.parent_id=$3", "AND c.deleted_at IS NULL AND c.state=$4", "parent.level<$5"},
		},
		{
			name:     "top level of a space",
			parentID: 0,
			want:     []string{"c.parent_id IS NULL", "parent.level<$4"},
			absent:   []string{"c.parent_id=$"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{}
			s := &retrievalStore{db: f}

			opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: -1, page: 1}

			if _, err := s.Menu(context.Background(), opts, 7, tt.parentID, 2); err != nil {
				t.Fatalf("Menu: %v", err)
			}

			want := append([]string{
				"WITH RECURSIVE menu AS(",
				"s.domain_id=$1 AND c.space_id=$2",
				"UNION ALL",
				"FROM menu JOIN kb.article m ON m.id=menu.id",
				"ORDER BY menu.level,m.subject,m.id",
				fmt.Sprintf("LIMIT %d", maxMenuItems),
				"CASE WHEN m.type=",
			}, tt.want...)

			for _, w := range want {
				if !strings.Contains(f.gotSQL, w) {
					t.Errorf("SQL %q does not contain %q", f.gotSQL, w)
				}
			}

			for _, absent := range tt.absent {
				if strings.Contains(f.gotSQL, absent) {
					t.Errorf("SQL %q contains %q", f.gotSQL, absent)
				}
			}

			if f.gotArgs[0] != int64(5) || f.gotArgs[1] != int64(7) {
				t.Fatalf("args = %v, want the domain and the space first", f.gotArgs)
			}
		})
	}
}

func TestRetrievalScansTheSummary(t *testing.T) {
	moment := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	cols := []string{
		"id", "space_id", "parent_id", "depth", "type", "subject", "tags",
		"state", "snippet", "body", "created_at", "updated_at",
	}
	row := []any{
		int64(11), int64(7), int64(3), int32(2), model.ArticleTypeFAQ, "VPN",
		[]string{"net"},
		model.ArticleStateActive, "how to", "full text", moment, moment,
	}

	f := &fakeQuerier{rows: &fakeRows{cols: cols, vals: [][]any{row}}}
	s := &retrievalStore{db: f}

	items, err := s.Resolve(
		context.Background(),
		&fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: -1, page: 1},
		[]int64{11},
		nil,
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := &model.ArticleSummary{
		ID: 11, SpaceID: 7, ParentID: 3, Depth: 2, Type: model.ArticleTypeFAQ,
		Subject: "VPN", Snippet: "how to", Tags: []string{"net"},
		State: model.ArticleStateActive, Body: "full text",
		CreatedAt: moment, UpdatedAt: moment,
	}

	if len(items) != 1 || !reflect.DeepEqual(items[0], want) {
		t.Fatalf("item = %+v, want %+v", items[0], want)
	}
}

// summaryRows plays back n minimal summary rows.
func summaryRows(n int) *fakeRows {
	vals := make([][]any, 0, n)
	for i := range n {
		vals = append(vals, []any{int64(i + 1)})
	}

	return &fakeRows{cols: []string{"id"}, vals: vals}
}

func TestRetrievalSemanticSearchRendersTheFusedQuery(t *testing.T) {
	f := &fakeQuerier{}
	s := &retrievalStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}}
	q := model.HybridQuery{
		Term:    "vpn",
		Filter:  model.SearchFilter{SpaceIDs: []int64{7}},
		Vectors: []model.ModelVector{{ModelID: 9, SpaceIDs: []int64{7}, Vector: []float32{0.5}}},
		TopK:    10,
	}

	if _, err := s.SemanticSearch(context.Background(), opts, q); err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	if len(f.sqls) != 2 || f.sqls[0] != "SET LOCAL diskann.query_rescore = 200" {
		t.Fatalf("statements = %q, want the rescore setting then the query", f.sqls)
	}

	for _, want := range []string{
		"WITH lex AS (",
		", vec AS (",
		"s.domain_id = $",
		"m.state = $",
		"e.model_id = $",
		"LIMIT 10)",
		"ORDER BY h.score DESC, c.id",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL does not contain %q", want)
		}
	}
}

func TestRetrievalResolveWithoutIDsResolvesNothing(t *testing.T) {
	f := &fakeQuerier{}
	s := &retrievalStore{db: f}

	items, err := s.Resolve(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: -1, page: 1}, nil, []int64{7})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if items == nil || len(items) != 0 || f.gotSQL != "" {
		t.Fatalf("items = %v, sql = %q, want an empty answer and no query", items, f.gotSQL)
	}
}
