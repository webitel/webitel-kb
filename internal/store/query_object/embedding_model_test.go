package queryobject

import (
	"strings"
	"testing"
)

func mustEmbeddingModelSQL(t *testing.T, q *EmbeddingModelQuery) (string, []any) {
	t.Helper()

	sql, args, err := q.ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}

	return sql, args
}

func TestEmbeddingModelCredentialUnreachable(t *testing.T) {
	q := NewEmbeddingModelQuery(EmbeddingModelFrom)

	// The credential column must be absent from the metadata entirely: no field
	// selection, sort or default may ever render it.
	if _, ok := q.FieldsMetadata()["config"]; ok {
		t.Fatal("config is present in the field metadata")
	}

	for field, meta := range q.FieldsMetadata() {
		if strings.Contains(meta.sqlExpr, "config") || strings.Contains(meta.aliasedExpr, "config") {
			t.Fatalf("field %q expression reaches the config column", field)
		}
	}

	sql, _ := mustEmbeddingModelSQL(t, q.WithFields([]string{"config"}))
	if strings.Contains(sql, "config") {
		t.Fatalf("selecting config rendered it: %s", sql)
	}
}

func TestEmbeddingModelDomainScope(t *testing.T) {
	q := NewEmbeddingModelQuery(EmbeddingModelFrom).WithDomainScope(5)

	sql, args := mustEmbeddingModelSQL(t, q)

	if !strings.Contains(sql, "(m.domain_id=$1 OR m.domain_id IS NULL)") {
		t.Fatalf("SQL %q misses the domain scope", sql)
	}

	if len(args) != 1 || args[0] != int64(5) {
		t.Fatalf("args = %v, want [5]", args)
	}
}

func TestEmbeddingModelFilters(t *testing.T) {
	tests := []struct {
		name     string
		build    func(*EmbeddingModelQuery) *EmbeddingModelQuery
		wantSQL  string // fragment expected in the rendered SQL; empty = no WHERE at all
		wantArgs []any
	}{
		{
			name:     "type filter",
			build:    func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithType("reranker") },
			wantSQL:  "m.type=$1",
			wantArgs: []any{"reranker"},
		},
		{
			name:    "empty type ignored",
			build:   func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithType("") },
			wantSQL: "",
		},
		{
			name:     "search is escaped ilike",
			build:    func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithSearch("bge_m3 100%") },
			wantSQL:  "m.name ILIKE $1",
			wantArgs: []any{`%bge\_m3 100\%%`},
		},
		{
			name:    "empty search ignored",
			build:   func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithSearch("") },
			wantSQL: "",
		},
		{
			name:     "ids filter",
			build:    func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithIDs([]int64{1, 2}) },
			wantSQL:  "m.id=ANY($1)",
			wantArgs: []any{[]int64{1, 2}},
		},
		{
			name:    "empty ids ignored",
			build:   func(q *EmbeddingModelQuery) *EmbeddingModelQuery { return q.WithIDs(nil) },
			wantSQL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.build(NewEmbeddingModelQuery(EmbeddingModelFrom))

			sql, args := mustEmbeddingModelSQL(t, q)

			if tt.wantSQL == "" {
				if strings.Contains(sql, "WHERE") {
					t.Fatalf("SQL %q must not filter", sql)
				}

				return
			}

			if !strings.Contains(sql, tt.wantSQL) {
				t.Fatalf("SQL %q does not contain %q", sql, tt.wantSQL)
			}

			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}

			for i, want := range tt.wantArgs {
				switch v := want.(type) {
				case []int64:
					got, ok := args[i].([]int64)
					if !ok || len(got) != len(v) {
						t.Fatalf("args[%d] = %v, want %v", i, args[i], want)
					}
				default:
					if args[i] != want {
						t.Fatalf("args[%d] = %v, want %v", i, args[i], want)
					}
				}
			}
		})
	}
}

func TestEmbeddingModelCreatedByJoin(t *testing.T) {
	q := NewEmbeddingModelQuery(EmbeddingModelFrom).
		WithFields([]string{"id", "created_by"}).
		WithSort("+created_by")

	sql, _ := mustEmbeddingModelSQL(t, q)

	const join = "LEFT JOIN directory.wbt_user cb ON cb.id=m.created_by"
	if got := strings.Count(sql, join); got != 1 {
		t.Fatalf("join rendered %d times, want exactly once: %s", got, sql)
	}

	for _, want := range []string{
		"cb.id AS created_by_id",
		"COALESCE(cb.name,cb.username)AS created_by_name",
		"ORDER BY COALESCE(cb.name,cb.username)ASC",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestEmbeddingModelDefaultsCoverReadModel(t *testing.T) {
	q := NewEmbeddingModelQuery(EmbeddingModelFrom)

	// Defaults select the full read model, so they must name every metadata
	// field — and activate the created_by join without WithFields running.
	if got, want := len(q.DefaultFields()), len(q.FieldsMetadata()); got != want {
		t.Fatalf("defaults name %d fields, metadata has %d", got, want)
	}

	sql, _ := mustEmbeddingModelSQL(t, q)

	for _, want := range []string{
		"m.id AS id", "m.domain_id AS domain_id", "m.validated_at AS validated_at",
		"created_by_name", "LEFT JOIN directory.wbt_user",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestEmbeddingModelCTEReadBack(t *testing.T) {
	// Writes read the row back from a CTE named m; the render must reference
	// only that relation and carry no arguments of its own.
	sql, args := mustEmbeddingModelSQL(t, NewEmbeddingModelQuery("m").WithFields([]string{"id", "created_by"}))

	if len(args) != 0 {
		t.Fatalf("read-back rendered arguments: %v", args)
	}

	if !strings.Contains(sql, "FROM m LEFT JOIN directory.wbt_user") {
		t.Fatalf("SQL %q does not select from the CTE", sql)
	}
}
