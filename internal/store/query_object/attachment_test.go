package queryobject

import (
	"strings"
	"testing"
)

func TestAttachmentScopesToDomainArticleAndChannel(t *testing.T) {
	sql, args := mustSQLArgs(t, NewAttachmentQuery(AttachmentFrom).
		WithFields([]string{"id"}).
		WithDomainScope(5).
		WithArticle(7).
		WithAttached())

	for _, want := range []string{
		"FROM storage.files m",
		"m.domain_id=$1",
		"m.uuid=$2",
		"EXISTS(SELECT 1 FROM kb.article a JOIN kb.space s ON s.id=a.space_id",
		"a.id=$3 AND s.domain_id=m.domain_id AND a.deleted_at IS NULL",
		"m.channel=$4",
		"m.removed IS NOT TRUE",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if len(args) != 4 || args[0] != int64(5) || args[1] != "7" || args[2] != int64(7) || args[3] != AttachmentChannel {
		t.Fatalf("args = %v, want [5 \"7\" 7 %q]", args, AttachmentChannel)
	}
}

func TestAttachmentFilters(t *testing.T) {
	tests := []struct {
		name     string
		build    func(q *AttachmentQuery) *AttachmentQuery
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "search matches the display name literally",
			build:    func(q *AttachmentQuery) *AttachmentQuery { return q.WithSearch("50%_off") },
			wantSQL:  "COALESCE(m.view_name,m.name)ILIKE $1",
			wantArgs: []any{`%50\%\_off%`},
		},
		{
			name:     "an empty search adds nothing",
			build:    func(q *AttachmentQuery) *AttachmentQuery { return q.WithSearch("") },
			wantSQL:  "FROM storage.files m",
			wantArgs: nil,
		},
		{
			name:     "ids narrow the page",
			build:    func(q *AttachmentQuery) *AttachmentQuery { return q.WithIDs([]int64{3, 4}) },
			wantSQL:  "m.id=ANY($1)",
			wantArgs: []any{[]int64{3, 4}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := mustSQLArgs(t, tt.build(NewAttachmentQuery(AttachmentFrom).WithFields([]string{"id"})))

			if !strings.Contains(sql, tt.wantSQL) {
				t.Fatalf("SQL %q does not contain %q", sql, tt.wantSQL)
			}

			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}

			for i, want := range tt.wantArgs {
				if ids, ok := want.([]int64); ok {
					got, _ := args[i].([]int64)
					if len(got) != len(ids) || got[0] != ids[0] {
						t.Fatalf("args[%d] = %v, want %v", i, args[i], want)
					}

					continue
				}

				if args[i] != want {
					t.Fatalf("args[%d] = %v, want %v", i, args[i], want)
				}
			}
		})
	}
}

func TestAttachmentDefaultsCoverReadModel(t *testing.T) {
	q := NewAttachmentQuery(AttachmentFrom)

	if got, want := len(q.DefaultFields()), len(q.FieldsMetadata()); got != want {
		t.Fatalf("defaults name %d fields, metadata has %d", got, want)
	}

	sql, _ := mustSQLArgs(t, q)

	for _, want := range []string{
		"m.id AS id",
		"COALESCE(m.view_name,m.name)AS name",
		"m.size AS size",
		"m.mime_type AS mime",
		"m.uploaded_at AS created_at",
		"cb.id AS created_by_id,COALESCE(cb.name,cb.username)AS created_by_name",
		"m.channel AS source",
		"LEFT JOIN directory.wbt_user cb ON cb.id=m.uploaded_by",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if _, ok := q.FieldsMetadata()["url"]; ok {
		t.Fatal("url must not be a selectable column")
	}
}

func TestAttachmentSortableFields(t *testing.T) {
	tests := []struct {
		name string
		sort string
		want string
	}{
		{name: "newest first", sort: "-created_at", want: "ORDER BY m.uploaded_at DESC"},
		{name: "by display name", sort: "+name", want: "ORDER BY COALESCE(m.view_name,m.name)ASC"},
		{name: "by size", sort: "-size", want: "ORDER BY m.size DESC"},
		{name: "by uploader through the join", sort: "+created_by", want: "ORDER BY COALESCE(cb.name,cb.username)ASC"},
		{name: "the mime type is not sortable", sort: "+mime", want: "FROM storage.files m LIMIT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, _ := mustSQLArgs(t, NewAttachmentQuery(AttachmentFrom).
				WithFields([]string{"id"}).
				WithSort(tt.sort).
				WithPaging(10, 1))

			if !strings.Contains(sql, tt.want) {
				t.Fatalf("SQL %q does not contain %q", sql, tt.want)
			}
		})
	}
}

func TestAttachmentJoinsTheUploaderOnce(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewAttachmentQuery(AttachmentFrom).
		WithFields([]string{"id", "created_by"}).
		WithSort("+created_by"))

	if strings.Count(sql, "LEFT JOIN directory.wbt_user") != 1 {
		t.Fatalf("SQL %q joins the uploader more than once", sql)
	}
}

func TestAttachmentCTEReadBack(t *testing.T) {
	sql, args := mustSQLArgs(t, NewAttachmentQuery("m").WithFields([]string{"id", "created_by"}))

	if len(args) != 0 {
		t.Fatalf("read-back rendered arguments: %v", args)
	}

	if !strings.Contains(sql, "FROM m LEFT JOIN directory.wbt_user") {
		t.Fatalf("SQL %q does not select from the CTE", sql)
	}
}

func TestAttachmentProjectionAlwaysCarriesTheIdentity(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewAttachmentQuery(AttachmentFrom).WithFields([]string{"name"}))

	for _, want := range []string{"AS name", "m.id AS id"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}
