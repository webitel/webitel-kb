package queryobject

import (
	"strings"
	"testing"
)

func TestArticleVersionDomainScope(t *testing.T) {
	sql, args := mustSQLArgs(t, NewArticleVersionQuery(ArticleVersionFrom).
		WithFields([]string{"id"}).
		WithDomainScope(5))

	for _, want := range []string{
		"EXISTS(SELECT 1 FROM kb.article a JOIN kb.space s ON s.id=a.space_id",
		"a.id=m.article_id AND s.domain_id=$1 AND a.deleted_at IS NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 1 || args[0] != int64(5) {
		t.Fatalf("args = %v, want [5]", args)
	}
}

func TestArticleVersionFilters(t *testing.T) {
	sql, args := mustSQLArgs(t, NewArticleVersionQuery(ArticleVersionFrom).
		WithFields([]string{"id"}).
		WithArticle(7).
		WithNumber(3))

	for _, want := range []string{"m.article_id=$1", "m.version_number=$2"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 2 {
		t.Fatalf("args = %v, want two of them", args)
	}
}

func TestArticleVersionHidesSearchVector(t *testing.T) {
	q := NewArticleVersionQuery(ArticleVersionFrom)

	if _, ok := q.FieldsMetadata()["tsv"]; ok {
		t.Fatal("the search vector must not be selectable")
	}

	sql, _ := mustSQLArgs(t, q)

	if strings.Contains(sql, "tsv") {
		t.Fatalf("SQL %q selects the search vector", sql)
	}
}

func TestArticleVersionDefaultsCoverReadModel(t *testing.T) {
	q := NewArticleVersionQuery(ArticleVersionFrom)

	if got, want := len(q.DefaultFields()), len(q.FieldsMetadata()); got != want {
		t.Fatalf("defaults name %d fields, metadata has %d", got, want)
	}

	sql, _ := mustSQLArgs(t, q)

	for _, want := range []string{
		"m.version_number AS version_number", "m.body_rich_text AS body_rich_text",
		"m.body_markdown AS body_markdown", "m.body_plain AS body_plain",
		"m.restored_from AS restored_from", "m.notes AS notes", "created_by_name",
		"LEFT JOIN directory.wbt_user cb",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestArticleVersionNewestFirstSortable(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewArticleVersionQuery(ArticleVersionFrom).
		WithFields([]string{"id"}).
		WithSort("-version_number"))

	if !strings.Contains(sql, "ORDER BY m.version_number DESC") {
		t.Fatalf("SQL %q misses the newest-first order", sql)
	}
}

func TestArticleVersionCTEReadBack(t *testing.T) {
	sql, args := mustSQLArgs(t, NewArticleVersionQuery("m").WithFields([]string{"id", "created_by"}))

	if len(args) != 0 {
		t.Fatalf("read-back rendered arguments: %v", args)
	}

	if !strings.Contains(sql, "FROM m LEFT JOIN directory.wbt_user") {
		t.Fatalf("SQL %q does not select from the CTE", sql)
	}
}

func TestVersionProjectionAlwaysCarriesTheIdentity(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewArticleVersionQuery(ArticleVersionFrom).WithFields([]string{"subject"}))

	for _, want := range []string{"m.subject AS subject", "m.id AS id"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}
