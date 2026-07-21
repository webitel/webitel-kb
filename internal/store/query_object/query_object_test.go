package queryobject

import (
	"strings"
	"testing"
)

// Join bitmask of the test entity.
const testLinkOwner = 1 << iota

// testQueryObject is a minimal entity exercising the base: three fields, one
// join-requiring field, one unsortable field, memoized metadata.
type testQueryObject struct {
	*baseQueryObject[*testQueryObject]

	meta  map[string]fieldMetadata
	joins int

	// defaultsWithOwner switches DefaultFields to include the join-backed field.
	defaultsWithOwner bool
}

func newTestQueryObject() *testQueryObject {
	q := new(testQueryObject)
	q.baseQueryObject = newBaseQueryObject("kb.thing t", q)

	return q
}

func (q *testQueryObject) DefaultFields() []string {
	if q.defaultsWithOwner {
		return []string{"id", "owner"}
	}

	return []string{"id", "name"}
}

func (q *testQueryObject) FieldsMetadata() map[string]fieldMetadata {
	if q.meta == nil {
		q.meta = map[string]fieldMetadata{
			"id": {
				sqlExpr:     "t.id",
				aliasedExpr: "t.id AS id",
				sortable:    true,
			},
			"name": {
				sqlExpr:     "t.name",
				aliasedExpr: "t.name AS name",
				sortable:    true,
			},
			// Unsortable payload column.
			"config": {
				sqlExpr:     "t.config",
				aliasedExpr: "t.config AS config",
			},
			// Field served by a join.
			"owner": {
				sqlExpr:      "o.name",
				aliasedExpr:  "o.name AS owner",
				requiresJoin: testLinkOwner,
				sortable:     true,
			},
		}
	}

	return q.meta
}

func (q *testQueryObject) EnsureJoins(requiredJoin int) {
	q.joins |= requiredJoin
}

func mustSQL(t *testing.T, q QueryObject) string {
	t.Helper()

	sql, _, err := q.ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}

	return sql
}

func TestWithFields(t *testing.T) {
	tests := []struct {
		name       string
		fields     []string
		wantSelect []string // aliased exprs expected in SELECT, in order
		wantJoins  int
	}{
		{
			name:       "known fields selected in order",
			fields:     []string{"name", "id"},
			wantSelect: []string{"t.name AS name", "t.id AS id"},
		},
		{
			name:       "unknown fields dropped silently",
			fields:     []string{"id", "nonexistent", "name"},
			wantSelect: []string{"t.id AS id", "t.name AS name"},
		},
		{
			name:       "join field activates its join",
			fields:     []string{"id", "owner"},
			wantSelect: []string{"t.id AS id", "o.name AS owner"},
			wantJoins:  testLinkOwner,
		},
		{
			name:       "empty selection falls back to defaults",
			fields:     nil,
			wantSelect: []string{"t.id AS id", "t.name AS name"},
		},
		{
			name:       "only unknown fields also falls back to defaults",
			fields:     []string{"bogus"},
			wantSelect: []string{"t.id AS id", "t.name AS name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newTestQueryObject()
			q.WithFields(tt.fields)

			sql := mustSQL(t, q)

			wantList := strings.Join(tt.wantSelect, ",") // CompactSQL drops the space after a comma
			if !strings.Contains(sql, wantList) {
				t.Errorf("SQL %q does not contain select list %q", sql, wantList)
			}

			if q.joins != tt.wantJoins {
				t.Errorf("joins = %b, want %b", q.joins, tt.wantJoins)
			}
		})
	}
}

func TestWithSort(t *testing.T) {
	tests := []struct {
		name      string
		sorts     []string
		wantOrder string // expected ORDER BY clause; empty = no ORDER BY
	}{
		{"ascending", []string{"+name"}, "ORDER BY t.name ASC"},
		{"descending", []string{"-name"}, "ORDER BY t.name DESC"},
		{"multiple criteria keep order", []string{"-name", "+id"}, "ORDER BY t.name DESC,t.id ASC"},
		{"unknown field dropped", []string{"-bogus"}, ""},
		{"unsortable field dropped", []string{"-config"}, ""},
		{"missing direction prefix dropped", []string{"name"}, ""},
		{"too short criterion dropped", []string{"-"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newTestQueryObject()
			q.WithSort(tt.sorts...)

			sql := mustSQL(t, q)

			if tt.wantOrder == "" {
				if strings.Contains(sql, "ORDER BY") {
					t.Fatalf("SQL %q must not contain ORDER BY", sql)
				}

				return
			}

			if !strings.Contains(sql, tt.wantOrder) {
				t.Fatalf("SQL %q does not contain %q", sql, tt.wantOrder)
			}
		})
	}
}

func TestSortInjectionImpossibleByConstruction(t *testing.T) {
	// A hostile sort value never reaches SQL: it fails metadata lookup.
	q := newTestQueryObject()
	q.WithSort("-name; DROP TABLE kb.thing --")

	sql := mustSQL(t, q)

	if strings.Contains(sql, "DROP") || strings.Contains(sql, "ORDER BY") {
		t.Fatalf("hostile sort leaked into SQL: %q", sql)
	}
}

func TestWithPaging(t *testing.T) {
	tests := []struct {
		name       string
		size, page int
		wantLimit  string // empty = no LIMIT expected
		wantOffset string // empty = no OFFSET expected
	}{
		// size+1 lookahead so the store can report a next page.
		{"first page", 25, 1, "LIMIT 26", ""},
		{"third page offset uses raw size", 25, 3, "LIMIT 26", "OFFSET 50"},
		{"page zero treated as first", 10, 0, "LIMIT 11", ""},
		{"unlimited disables paging entirely", -1, 3, "", ""},
		{"zero size disables paging entirely", 0, 2, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newTestQueryObject()
			q.WithPaging(tt.size, tt.page)

			sql := mustSQL(t, q)

			for clause, want := range map[string]string{"LIMIT": tt.wantLimit, "OFFSET": tt.wantOffset} {
				if want == "" {
					if strings.Contains(sql, clause) {
						t.Errorf("SQL %q must not contain %s", sql, clause)
					}

					continue
				}

				if !strings.Contains(sql, want) {
					t.Errorf("SQL %q does not contain %q", sql, want)
				}
			}
		})
	}
}

func TestChainingReturnsEntity(t *testing.T) {
	// The CRTP base must return the concrete entity so calls chain fluently.
	q := newTestQueryObject().WithFields([]string{"id"}).WithSort("-id").WithPaging(5, 2)

	sql := mustSQL(t, q)

	for _, want := range []string{"t.id AS id", "ORDER BY t.id DESC", "LIMIT 6", "OFFSET 5"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestDefaultFieldsActivateJoins(t *testing.T) {
	// Default fields never pass through WithFields, so ToSQL itself must
	// activate their joins — an entity whose defaults include a join-backed
	// field would otherwise render a broken query.
	q := newTestQueryObject()
	q.defaultsWithOwner = true

	sql := mustSQL(t, q) // no WithFields: defaults path

	if !strings.Contains(sql, "o.name AS owner") {
		t.Fatalf("SQL %q does not select the default join-backed field", sql)
	}

	if q.joins != testLinkOwner {
		t.Fatalf("joins = %b, want %b — ToSQL must ensure joins of default fields", q.joins, testLinkOwner)
	}
}

func TestToSQLIsIdempotent(t *testing.T) {
	// Rendering must not mutate the query object: a store may log the SQL and
	// then execute it, or re-render in a retry loop.
	q := newTestQueryObject().WithFields([]string{"id", "name"}).WithSort("-id").WithPaging(5, 2)

	first := mustSQL(t, q)
	second := mustSQL(t, q)

	if first != second {
		t.Fatalf("second ToSQL differs:\n first: %s\nsecond: %s", first, second)
	}

	if c := strings.Count(second, "t.id AS id"); c != 1 {
		t.Fatalf("column duplicated %d times after re-render: %s", c, second)
	}

	if c := strings.Count(second, "ORDER BY"); c != 1 {
		t.Fatalf("ORDER BY duplicated after re-render: %s", second)
	}
}

func TestToSQLSkipsSortWithVanishedMetadata(t *testing.T) {
	// WithSort validated against one metadata snapshot; if the entity rebuilds
	// the map without the field, ToSQL must skip it rather than render an empty
	// ORDER BY expression.
	q := newTestQueryObject()
	q.WithSort("-name")

	// Simulate a non-memoized/state-dependent entity: drop the field after
	// WithSort accepted it.
	delete(q.FieldsMetadata(), "name")

	sql := mustSQL(t, q)

	if strings.Contains(sql, "ORDER BY") {
		t.Fatalf("SQL %q must not contain ORDER BY for vanished field", sql)
	}
}

func TestFieldsMetadataMemoized(t *testing.T) {
	q := newTestQueryObject()

	first := q.FieldsMetadata()
	second := q.FieldsMetadata()

	// Same map instance, not a rebuild per call.
	first["probe"] = fieldMetadata{}

	if _, ok := second["probe"]; !ok {
		t.Fatal("FieldsMetadata rebuilt the map; must be memoized")
	}
}
