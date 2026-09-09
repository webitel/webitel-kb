package queryobject

import (
	"strings"
	"testing"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestSummaryProjection(t *testing.T) {
	sql, args := mustSQLArgs(t, NewSummaryQuery(SummaryFrom))

	for _, want := range []string{
		"m.id AS id",
		"m.space_id AS space_id",
		"COALESCE(m.parent_id,0)AS parent_id",
		"m.depth AS depth",
		"m.type AS type",
		"m.subject AS subject",
		"m.tags AS tags",
		"m.state AS state",
		"m.created_at AS created_at",
		"m.updated_at AS updated_at",
		"FROM kb.article m",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 0 {
		t.Fatalf("args = %v, want none", args)
	}
}

func TestSummaryScope(t *testing.T) {
	sql, args := mustSQLArgs(t, NewSummaryQuery(SummaryFrom).WithDomainScope(5).WithRetrievable())

	for _, want := range []string{
		"JOIN kb.space s ON s.id=m.space_id",
		"s.domain_id=$1",
		"m.deleted_at IS NULL",
		"m.state=$2",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 2 || args[0] != int64(5) || args[1] != model.ArticleStateActive {
		t.Fatalf("args = %v, want the domain id and the active state code", args)
	}
}

func TestSummaryFilters(t *testing.T) {
	tests := []struct {
		name    string
		build   func() *SummaryQuery
		want    []string
		absent  []string
		numArgs int
	}{
		{
			name:    "spaces render as ANY",
			build:   func() *SummaryQuery { return NewSummaryQuery(SummaryFrom).WithSpaces([]int64{1, 2}) },
			want:    []string{"m.space_id=ANY($1)"},
			numArgs: 1,
		},
		{
			name:    "no spaces means any space",
			build:   func() *SummaryQuery { return NewSummaryQuery(SummaryFrom).WithSpaces(nil) },
			absent:  []string{"m.space_id=ANY"},
			numArgs: 0,
		},
		{
			name:    "ids keep the asked order",
			build:   func() *SummaryQuery { return NewSummaryQuery(SummaryFrom).WithIDs([]int64{3, 1}) },
			want:    []string{"m.id=ANY($1)", "ORDER BY array_position($2::bigint[],m.id)"},
			numArgs: 2,
		},
		{
			name:    "no ids disables the filter",
			build:   func() *SummaryQuery { return NewSummaryQuery(SummaryFrom).WithIDs(nil) },
			absent:  []string{"m.id=ANY", "array_position"},
			numArgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := mustSQLArgs(t, tt.build())

			for _, want := range tt.want {
				if !strings.Contains(sql, want) {
					t.Errorf("SQL %q does not contain %q", sql, want)
				}
			}

			for _, absent := range tt.absent {
				if strings.Contains(sql, absent) {
					t.Errorf("SQL %q contains %q", sql, absent)
				}
			}

			if len(args) != tt.numArgs {
				t.Errorf("args = %v, want %d of them", args, tt.numArgs)
			}
		})
	}
}

func TestSummaryBodyColumns(t *testing.T) {
	sql, args := mustSQLArgs(t, NewSummaryQuery(SummaryFrom).WithExcerpt().WithFAQBody())

	for _, want := range []string{
		"COALESCE(left(v.body_plain,$1),'')AS snippet",
		"CASE WHEN m.type=$2 THEN COALESCE(v.body_plain,'')ELSE''END AS body",
		// Left: an article without a published version still answers.
		"LEFT JOIN kb.article_version v ON v.id=m.published_version_id",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 2 || args[0] != ExcerptLength || args[1] != model.ArticleTypeFAQ {
		t.Fatalf("args = %v, want the excerpt length and the FAQ type code", args)
	}
}

func TestSummaryHeadlineCarriesNoMarkup(t *testing.T) {
	// The contract carries the snippet as plain text.
	sql, args := mustSQLArgs(t, NewSummaryQuery(HitsFrom).WithHeadline("vpn"))

	for _, want := range []string{
		"ts_headline('" + model.TextSearchDefault + "', v.body_plain, websearch_to_tsquery('" + model.TextSearchDefault + "', $1)",
		`StartSel=""`,
		`StopSel=""`,
		"MaxFragments=1",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 1 || args[0] != "vpn" {
		t.Fatalf("args = %v, want the term", args)
	}
}

func TestSummaryVersionJoinRendersOnce(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewSummaryQuery(SummaryFrom).WithHeadline("vpn").WithExcerpt().WithFAQBody())

	if got := strings.Count(sql, "LEFT JOIN kb.article_version v"); got != 1 {
		t.Fatalf("version join rendered %d times, want exactly once: %s", got, sql)
	}
}

func TestSummaryPrefixOrderAndLimit(t *testing.T) {
	sql, args := mustSQLArgs(t, NewSummaryQuery("menu JOIN kb.article m ON m.id=menu.id").
		WithPrefix("WITH menu AS (SELECT id FROM kb.article WHERE space_id = ?)", int64(7)).
		WithOrder("menu.level", "m.subject").
		WithLimit(100))

	for _, want := range []string{
		"WITH menu AS(SELECT id FROM kb.article WHERE space_id=$1)SELECT",
		"FROM menu JOIN kb.article m ON m.id=menu.id",
		"ORDER BY menu.level,m.subject",
		"LIMIT 100",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 1 || args[0] != int64(7) {
		t.Fatalf("args = %v, want the prefix argument first", args)
	}
}

func TestSearchHitsRanksBothLexicalSidesSeparately(t *testing.T) {
	// One branch per side, so each uses its own full-text index.
	sql, args, err := NewSearchHits("reset password").
		WithScope(5, model.SearchFilter{SpaceIDs: []int64{7}, Tags: []string{"net"}, TagsMatchAll: true}).
		WithPaging(10, 2).
		ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}

	for _, want := range []string{
		"WITH h AS (SELECT id, sum(rank) AS rank FROM (",
		"ts_rank_cd(m.search_tsv, websearch_to_tsquery('simple', ?)) AS rank",
		"WHERE m.search_tsv @@ websearch_to_tsquery('simple', ?)",
		"UNION ALL",
		"JOIN kb.article_version v ON v.id = m.published_version_id",
		"ts_rank_cd(v.tsv, websearch_to_tsquery('simple', ?)) AS rank",
		"WHERE v.tsv @@ websearch_to_tsquery('simple', ?)",
		"GROUP BY id ORDER BY rank DESC, id",
		// One row past the page answers whether a next page exists.
		"LIMIT 11 OFFSET 10",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}

	// Both branches filter identically.
	for _, want := range []string{"s.domain_id = ?", "m.deleted_at IS NULL", "m.state = ?", "m.space_id = ANY(?)", "m.tags @> ?"} {
		if got := strings.Count(sql, want); got != 2 {
			t.Errorf("SQL %q applies %q %d times, want once per branch", sql, want, got)
		}
	}

	if len(args) != 12 {
		t.Fatalf("args = %v, want six per branch", args)
	}
}

func TestSearchHitsTagMatch(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		matchAll bool
		want     string
		absent   string
	}{
		{name: "all tags", tags: []string{"a", "b"}, matchAll: true, want: "m.tags @> ?", absent: "m.tags && ?"},
		{name: "any tag", tags: []string{"a"}, want: "m.tags && ?", absent: "m.tags @> ?"},
		{name: "no tags", want: "", absent: "m.tags"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, err := NewSearchHits("vpn").
				WithScope(5, model.SearchFilter{Tags: tt.tags, TagsMatchAll: tt.matchAll}).
				ToSQL()
			if err != nil {
				t.Fatalf("ToSQL: %v", err)
			}

			if tt.want != "" && !strings.Contains(sql, tt.want) {
				t.Errorf("SQL %q does not contain %q", sql, tt.want)
			}

			if strings.Contains(sql, tt.absent) {
				t.Errorf("SQL %q contains %q", sql, tt.absent)
			}
		})
	}
}

func TestSearchHitsPaging(t *testing.T) {
	tests := []struct {
		name   string
		size   int
		page   int
		want   string
		absent string
	}{
		{name: "first page", size: 10, page: 1, want: "LIMIT 11", absent: "OFFSET"},
		{name: "second page", size: 10, page: 2, want: "LIMIT 11 OFFSET 10"},
		{name: "unlimited size disables paging", size: -1, page: 1, absent: "LIMIT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _, err := NewSearchHits("vpn").WithPaging(tt.size, tt.page).ToSQL()
			if err != nil {
				t.Fatalf("ToSQL: %v", err)
			}

			if tt.want != "" && !strings.Contains(sql, tt.want) {
				t.Errorf("SQL %q does not contain %q", sql, tt.want)
			}

			if tt.absent != "" && strings.Contains(sql, tt.absent) {
				t.Errorf("SQL %q contains %q", sql, tt.absent)
			}
		})
	}
}

func TestSearchParsesWithTheStoredConfiguration(t *testing.T) {
	// The stored vector of a version is built with this configuration.
	sql, _, err := NewSearchHits("vpn").ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}

	if strings.Count(sql, "websearch_to_tsquery('"+model.TextSearchDefault+"'") != 4 {
		t.Fatalf("SQL %q does not parse both branches with %q", sql, model.TextSearchDefault)
	}
}
