package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

func TestSpaceListRendersScopedQuery(t *testing.T) {
	f := &fakeQuerier{}
	s := &spaceStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, search: "docs", size: 10, page: 2}

	if _, _, err := s.List(context.Background(), opts); err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, want := range []string{
		"m.domain_id=$1",
		"m.name ILIKE $2",
		"ORDER BY m.name ASC,m.id ASC",
		"LIMIT 11",
		"OFFSET 10",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[0] != int64(5) {
		t.Errorf("args[0] = %v, want the domain id", f.gotArgs[0])
	}
}

func TestSpaceCreateRendersCTE(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &spaceStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.Space{Name: "docs", Language: "uk", EmbeddingModelID: 3, VectorSearchEnabled: true, HomeArticleID: 11}

	created, err := s.Create(context.Background(), opts, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID != 1 {
		t.Fatalf("created = %+v", created)
	}

	for _, want := range []string{
		"WITH m AS (INSERT INTO kb.space",
		// An empty chunking strategy defers to the database default.
		"DEFAULT",
		"RETURNING *",
		"SELECT m.id AS id FROM m",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// Column order: domain_id, name, description, language, embedding_model_id,
	// reranker_model_id, vector_search_enabled, rerank_enabled,
	// [chunking_strategy = DEFAULT expr, no arg], home_article_id,
	// created_by, updated_by.
	wantArgs := []any{int64(5), "docs", (*string)(nil), "uk"}
	for i, want := range wantArgs {
		if want == (*string)(nil) {
			if got, ok := f.gotArgs[i].(*string); !ok || got != nil {
				t.Errorf("args[%d] = %v, want NULL", i, f.gotArgs[i])
			}

			continue
		}

		if f.gotArgs[i] != want {
			t.Errorf("args[%d] = %v, want %v", i, f.gotArgs[i], want)
		}
	}

	// Remaining columns: embedding, reranker(nil), vector, rerank,
	// [chunking = DEFAULT, no arg], home(nil), created_by, updated_by.
	assertPinned(t, f.gotArgs, 4, ptrTo(int64(3)))
	assertPinned(t, f.gotArgs, 5, (*int64)(nil))
	assertPinned(t, f.gotArgs, 6, true)
	assertPinned(t, f.gotArgs, 7, false)
	assertPinned(t, f.gotArgs, 8, ptrTo(int64(11)))
	assertPinned(t, f.gotArgs, 9, ptrTo(int64(9)))
	assertPinned(t, f.gotArgs, 10, ptrTo(int64(9)))

	if len(f.gotArgs) != 11 {
		t.Fatalf("args = %d, want 11: %v", len(f.gotArgs), f.gotArgs)
	}
}

func TestSpaceUpdateNeverTouchesImmutableColumns(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &spaceStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, id: 1, fields: []string{"id"}}
	in := &model.Space{Name: "docs", Language: "en", EmbeddingModelID: 3, ChunkingStrategy: "recursive_markdown"}

	if _, err := s.Update(context.Background(), opts, in); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The immutable columns must be absent from the SET list entirely; the
	// input language is ignored at this layer.
	setList, _, _ := strings.Cut(f.gotSQL, " WHERE ")
	for _, absent := range []string{"language", "target_embedding_model_id"} {
		if strings.Contains(setList, absent) {
			t.Errorf("SET list %q touches immutable column %s", setList, absent)
		}
	}

	for _, want := range []string{
		"updated_at = now()",
		"WHERE id = $",
		"AND domain_id = $",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}
}

func TestSpaceDeleteRendersScopedCTE(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &spaceStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 1, fields: []string{"id"}}

	if _, err := s.Delete(context.Background(), opts); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !strings.Contains(f.gotSQL, "WITH m AS (DELETE FROM kb.space WHERE id = $1 AND domain_id = $2 RETURNING *)") {
		t.Fatalf("SQL %q is not a domain-scoped delete CTE", f.gotSQL)
	}
}

func TestSpaceReplaceTeams(t *testing.T) {
	t.Run("full replace runs scoped delete then insert", func(t *testing.T) {
		f := &fakeQuerier{}
		s := &spaceStore{db: f}

		if err := s.ReplaceTeams(context.Background(), 7, 5, 9, []int64{1, 2}); err != nil {
			t.Fatalf("ReplaceTeams: %v", err)
		}

		if len(f.sqls) != 2 {
			t.Fatalf("statements = %d, want delete + insert", len(f.sqls))
		}

		// Both statements scope through kb.space to the domain.
		if !strings.Contains(f.sqls[0], "DELETE FROM kb.team_space") || !strings.Contains(f.sqls[0], "s.domain_id = $2") {
			t.Errorf("delete not domain-scoped: %s", f.sqls[0])
		}

		if !strings.Contains(f.sqls[1], "INSERT INTO kb.team_space") || !strings.Contains(f.sqls[1], "s.domain_id = $4") {
			t.Errorf("insert not domain-scoped: %s", f.sqls[1])
		}

		if got, ok := f.argsList[1][0].([]int64); !ok || len(got) != 2 {
			t.Errorf("insert args[0] = %v, want the team ids", f.argsList[1][0])
		}
	})

	t.Run("empty set only removes the binding", func(t *testing.T) {
		f := &fakeQuerier{}
		s := &spaceStore{db: f}

		if err := s.ReplaceTeams(context.Background(), 7, 5, 9, nil); err != nil {
			t.Fatalf("ReplaceTeams: %v", err)
		}

		if len(f.sqls) != 1 || !strings.Contains(f.sqls[0], "DELETE FROM kb.team_space") {
			t.Fatalf("statements = %v, want a single delete", f.sqls)
		}
	})
}

func TestSpaceHasArticles(t *testing.T) {
	f := &fakeQuerier{row: fakeRow{vals: []any{true}}}
	s := &spaceStore{db: f}

	has, err := s.HasArticles(context.Background(), 7, 5)
	if err != nil || !has {
		t.Fatalf("has = %v, err %v", has, err)
	}

	// ANY referencing article blocks — the same condition the schema RESTRICT
	// enforces; a narrower filter here would surface raw FK errors.
	for _, absent := range []string{"state", "deleted_at"} {
		if strings.Contains(f.gotSQL, absent) {
			t.Errorf("SQL %q must not filter by %s", f.gotSQL, absent)
		}
	}

	if !strings.Contains(f.gotSQL, "s.domain_id = $2") {
		t.Errorf("SQL %q is not domain-scoped", f.gotSQL)
	}

	if f.gotArgs[0] != int64(7) || f.gotArgs[1] != int64(5) {
		t.Fatalf("args = %v, want [space domain]", f.gotArgs)
	}
}

func TestSpaceLocateForUpdateLocksTheRow(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(7)}}}}
	s := &spaceStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: []int64{7}, fields: []string{"id"}}

	if _, err := s.LocateForUpdate(context.Background(), opts); err != nil {
		t.Fatalf("LocateForUpdate: %v", err)
	}

	// Locks only the space relation: the user joins are outer and unlockable.
	if !strings.Contains(f.gotSQL, "FOR UPDATE OF m") {
		t.Fatalf("SQL %q does not lock the row", f.gotSQL)
	}

	f2 := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(7)}}}}
	s2 := &spaceStore{db: f2}

	if _, err := s2.Locate(context.Background(), opts); err != nil {
		t.Fatalf("Locate: %v", err)
	}

	if strings.Contains(f2.gotSQL, "FOR UPDATE") {
		t.Fatalf("plain Locate must not lock: %s", f2.gotSQL)
	}
}

func TestSpaceUpdateBindsValuesInOrder(t *testing.T) {
	// Pin every SET value to its position: a swapped pair corrupts rows while
	// staying invisible to shape-only assertions.
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &spaceStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, id: 1, fields: []string{"id"}}
	in := &model.Space{
		Name: "n1", Description: "d1", EmbeddingModelID: 3, RerankerModelID: 4,
		VectorSearchEnabled: true, RerankEnabled: false,
		ChunkingStrategy: "cs1", HomeArticleID: 11,
	}

	if _, err := s.Update(context.Background(), opts, in); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// SET order: name, description, embedding_model_id, reranker_model_id,
	// vector_search_enabled, rerank_enabled, chunking_strategy,
	// home_article_id, updated_at=now() (no arg), updated_by;
	// WHERE: id, domain_id.
	if len(f.gotArgs) != 11 {
		t.Fatalf("args = %d, want 11: %v", len(f.gotArgs), f.gotArgs)
	}

	assertPinned(t, f.gotArgs, 0, "n1")
	assertPinned(t, f.gotArgs, 1, ptrTo("d1"))
	assertPinned(t, f.gotArgs, 2, ptrTo(int64(3)))
	assertPinned(t, f.gotArgs, 3, ptrTo(int64(4)))
	assertPinned(t, f.gotArgs, 4, true)
	assertPinned(t, f.gotArgs, 5, false)
	assertPinned(t, f.gotArgs, 6, "cs1")
	assertPinned(t, f.gotArgs, 7, ptrTo(int64(11)))
	assertPinned(t, f.gotArgs, 8, ptrTo(int64(9))) // updated_by = caller
	assertPinned(t, f.gotArgs, 9, int64(1))        // WHERE id
	assertPinned(t, f.gotArgs, 10, int64(5))       // WHERE domain_id
}

func TestSpaceLocateRequiresExactlyOneID(t *testing.T) {
	s := &spaceStore{db: &fakeQuerier{}}

	_, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}})
	if errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestSpaceFullDefaultSelectionScans(t *testing.T) {
	// Drive the complete default column set through real pgx scanning, teams
	// arriving as a JSON aggregate.
	now := time.Now()

	values := map[string]any{
		"id": int64(7), "domain_id": int64(5), "name": "docs",
		"description": ptrTo("main kb"), "language": "uk",
		"embedding_model_id": ptrTo(int64(3)), "target_embedding_model_id": (*int64)(nil),
		"reranker_model_id": (*int64)(nil), "vector_search_enabled": true,
		"rerank_enabled": false, "chunking_strategy": "recursive_markdown",
		"home_article_id": (*int64)(nil),
		"teams":           []byte(`[{"id": 4, "name": "Sales"}]`),
		"created_at":      now, "updated_at": now,
		"created_by_id": ptrTo(int64(9)), "created_by_name": ptrTo("Admin"),
		"updated_by_id": (*int64)(nil), "updated_by_name": (*string)(nil),
	}

	sql, _, err := queryobject.NewSpaceQuery(queryobject.SpaceFrom).ToSQL()
	if err != nil {
		t.Fatal(err)
	}

	cols := selectAliases(t, sql)
	if len(cols) != len(values) {
		t.Fatalf("rendered %d aliases, fixture has %d — update both together: %v", len(cols), len(values), cols)
	}

	row := make([]any, 0, len(cols))
	for _, col := range cols {
		v, ok := values[col]
		if !ok {
			t.Fatalf("no fixture value for rendered alias %q", col)
		}

		switch typed := v.(type) {
		case *int64:
			if typed == nil {
				v = nil
			}
		case *string:
			if typed == nil {
				v = nil
			}
		}

		row = append(row, v)
	}

	f := &fakeQuerier{rows: &fakeRows{cols: cols, vals: [][]any{row}}}
	s := &spaceStore{db: f}

	items, _, err := s.List(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 10, page: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	got := items[0]
	if got.ID != 7 || got.Name != "docs" || got.Description != "main kb" || got.EmbeddingModelID != 3 {
		t.Fatalf("space = %+v", got)
	}

	if len(got.Teams) != 1 || got.Teams[0].ID != 4 || got.Teams[0].Name != "Sales" {
		t.Fatalf("teams = %+v, want [4/Sales]", got.Teams)
	}

	if got.CreatedBy == nil || got.CreatedBy.Name != "Admin" || got.UpdatedBy != nil {
		t.Fatalf("lookups = %+v / %+v", got.CreatedBy, got.UpdatedBy)
	}
}

// assertPinned checks one recorded argument against its expected value,
// dereferencing pointer expectations.
func assertPinned(t *testing.T, args []any, i int, want any) {
	t.Helper()

	got := args[i]

	switch w := want.(type) {
	case *string:
		g, ok := got.(*string)
		if !ok || (w == nil) != (g == nil) || (w != nil && *g != *w) {
			t.Errorf("args[%d] = %v, want %v", i, got, want)
		}
	case *int64:
		g, ok := got.(*int64)
		if !ok || (w == nil) != (g == nil) || (w != nil && *g != *w) {
			t.Errorf("args[%d] = %v, want %v", i, got, want)
		}
	default:
		if got != want {
			t.Errorf("args[%d] = %v, want %v", i, got, want)
		}
	}
}

func TestSpaceReplaceTeamsBindsArgs(t *testing.T) {
	t.Run("scope and creator pinned", func(t *testing.T) {
		f := &fakeQuerier{}
		s := &spaceStore{db: f}

		if err := s.ReplaceTeams(context.Background(), 7, 5, 9, []int64{1, 2}); err != nil {
			t.Fatalf("ReplaceTeams: %v", err)
		}

		// delete: [spaceID, domainID]
		assertPinned(t, f.argsList[0], 0, int64(7))
		assertPinned(t, f.argsList[0], 1, int64(5))

		// insert: [teamIDs, spaceID, created_by, domainID]
		assertPinned(t, f.argsList[1], 1, int64(7))
		assertPinned(t, f.argsList[1], 2, ptrTo(int64(9)))
		assertPinned(t, f.argsList[1], 3, int64(5))

		// The store guards itself against duplicate ids.
		if !strings.Contains(f.sqls[1], "SELECT DISTINCT unnest") {
			t.Errorf("insert does not deduplicate: %s", f.sqls[1])
		}
	})

	t.Run("zero creator becomes NULL", func(t *testing.T) {
		f := &fakeQuerier{}
		s := &spaceStore{db: f}

		if err := s.ReplaceTeams(context.Background(), 7, 5, 0, []int64{1}); err != nil {
			t.Fatalf("ReplaceTeams: %v", err)
		}

		assertPinned(t, f.argsList[1], 2, (*int64)(nil))
	})
}

func TestSpaceListIDsFilter(t *testing.T) {
	f := &fakeQuerier{}
	s := &spaceStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 10, page: 1, ids: []int64{7, 8}}

	if _, _, err := s.List(context.Background(), opts); err != nil {
		t.Fatalf("List: %v", err)
	}

	if !strings.Contains(f.gotSQL, "m.id=ANY($2)") {
		t.Fatalf("SQL %q does not filter by ids", f.gotSQL)
	}

	if got, ok := f.gotArgs[1].([]int64); !ok || len(got) != 2 {
		t.Fatalf("args[1] = %v, want the ids", f.gotArgs[1])
	}
}

func TestMapSpaceBranches(t *testing.T) {
	t.Run("present optional columns are carried", func(t *testing.T) {
		got := mapSpace(&spaceRecord{
			ID: 7, DomainID: 5, Name: "docs", Language: "uk",
			TargetEmbeddingModelID: ptrTo(int64(21)),
			RerankerModelID:        ptrTo(int64(4)),
			HomeArticleID:          ptrTo(int64(11)),
			UpdatedByID:            ptrTo(int64(3)), // user row gone: id without name
		})

		if got.TargetEmbeddingModelID != 21 || got.RerankerModelID != 4 || got.HomeArticleID != 11 {
			t.Fatalf("optional columns lost: %+v", got)
		}

		if got.UpdatedBy == nil || got.UpdatedBy.ID != 3 || got.UpdatedBy.Name != "" {
			t.Fatalf("nameless lookup = %+v, want id-only", got.UpdatedBy)
		}
	})

	t.Run("empty and malformed teams stay empty without error", func(t *testing.T) {
		if got := mapSpace(&spaceRecord{ID: 7, Teams: []byte(`[]`)}); len(got.Teams) != 0 {
			t.Fatalf("empty aggregate mapped to %+v", got.Teams)
		}

		if got := mapSpace(&spaceRecord{ID: 7, Teams: []byte(`{broken`)}); len(got.Teams) != 0 {
			t.Fatalf("malformed aggregate mapped to %+v", got.Teams)
		}
	})
}
