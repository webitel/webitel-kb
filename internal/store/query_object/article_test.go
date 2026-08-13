package queryobject

import (
	"strings"
	"testing"
)

func TestArticleDomainScopeJoinsSpace(t *testing.T) {
	sql, args := mustSQLArgs(t, NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).WithDomainScope(5))

	for _, want := range []string{
		"JOIN kb.space s ON s.id=m.space_id",
		"s.domain_id=$1",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 1 || args[0] != int64(5) {
		t.Fatalf("args = %v, want [5]", args)
	}
}

func TestArticleSpaceJoinRendersOnce(t *testing.T) {
	// The scope and the space lookup share one join.
	sql, _ := mustSQLArgs(t, NewArticleQuery(ArticleFrom).
		WithDomainScope(5).
		WithFields([]string{"id", "domain_id", "space"}))

	if got := strings.Count(sql, "JOIN kb.space s ON s.id=m.space_id"); got != 1 {
		t.Fatalf("space join rendered %d times, want exactly once: %s", got, sql)
	}
}

func TestArticleVisibleHidesDeleted(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).WithVisible())

	if !strings.Contains(sql, "m.deleted_at IS NULL") {
		t.Fatalf("SQL %q does not hide deleted rows", sql)
	}
}

func TestArticleFilters(t *testing.T) {
	tests := []struct {
		name    string
		build   func() *ArticleQuery
		want    []string
		absent  []string
		numArgs int
	}{
		{
			name: "space type state",
			build: func() *ArticleQuery {
				return NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).
					WithSpace(7).WithType(1).WithState(2)
			},
			want:    []string{"m.space_id=$1", "m.type=$2", "m.state=$3"},
			numArgs: 3,
		},
		{
			name: "zero values disable criteria",
			build: func() *ArticleQuery {
				return NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).
					WithSpace(0).WithType(0).WithState(0)
			},
			absent:  []string{"m.space_id", "m.type=", "m.state="},
			numArgs: 0,
		},
		{
			name: "search escapes the term",
			build: func() *ArticleQuery {
				return NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).WithSearch("50%_a")
			},
			want:    []string{"m.subject ILIKE $1"},
			numArgs: 1,
		},
		{
			name: "ids render as ANY",
			build: func() *ArticleQuery {
				return NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).WithIDs([]int64{1, 2})
			},
			want:    []string{"m.id=ANY($1)"},
			numArgs: 1,
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
					t.Errorf("SQL %q must not contain %q", sql, absent)
				}
			}

			if len(args) != tt.numArgs {
				t.Errorf("args = %v, want %d of them", args, tt.numArgs)
			}
		})
	}
}

func TestArticleSearchEscapesWildcards(t *testing.T) {
	_, args := mustSQLArgs(t, NewArticleQuery(ArticleFrom).WithFields([]string{"id"}).WithSearch("50%_a"))

	if args[0] != `%50\%\_a%` {
		t.Fatalf("args[0] = %q, want the escaped pattern", args[0])
	}
}

func TestArticleUserJoinsRenderOncePerAlias(t *testing.T) {
	q := NewArticleQuery(ArticleFrom).
		WithFields([]string{"id", "created_by", "updated_by"}).
		WithSort("+created_by", "-updated_by")

	sql, _ := mustSQLArgs(t, q)

	for _, join := range []string{
		"LEFT JOIN directory.wbt_user cb ON cb.id=m.created_by",
		"LEFT JOIN directory.wbt_user ub ON ub.id=m.updated_by",
	} {
		if got := strings.Count(sql, join); got != 1 {
			t.Fatalf("join %q rendered %d times, want exactly once: %s", join, got, sql)
		}
	}
}

func TestArticleDefaultsCoverReadModel(t *testing.T) {
	q := NewArticleQuery(ArticleFrom)

	if got, want := len(q.DefaultFields()), len(q.FieldsMetadata()); got != want {
		t.Fatalf("defaults name %d fields, metadata has %d", got, want)
	}

	sql, _ := mustSQLArgs(t, q)

	for _, want := range []string{
		"m.id AS id", "s.domain_id AS domain_id", "space_name", "m.parent_id AS parent_id",
		"m.depth AS depth", "m.tags AS tags", "m.ver AS ver", "m.published_version_id AS published_version_id",
		"created_by_name", "updated_by_name", "JOIN kb.space s", "LEFT JOIN directory.wbt_user cb",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestArticleSortValidation(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewArticleQuery(ArticleFrom).
		WithFields([]string{"id"}).
		WithSort("+subject", "-created_at", "+tags", "subject", "+bogus"))

	if !strings.Contains(sql, "ORDER BY m.subject ASC,m.created_at DESC") {
		t.Fatalf("SQL %q misses the validated sort", sql)
	}

	for _, absent := range []string{"m.tags ASC", "bogus"} {
		if strings.Contains(sql, absent) {
			t.Fatalf("SQL %q contains a dropped criterion %q", sql, absent)
		}
	}
}

func TestArticleCTEReadBack(t *testing.T) {
	sql, args := mustSQLArgs(t, NewArticleQuery("m").WithFields([]string{"id", "space", "created_by"}))

	if len(args) != 0 {
		t.Fatalf("read-back rendered arguments: %v", args)
	}

	if !strings.Contains(sql, "FROM m JOIN kb.space s") {
		t.Fatalf("SQL %q does not select from the CTE", sql)
	}
}
