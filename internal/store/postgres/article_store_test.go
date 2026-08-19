package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestArticleListRendersScopedQuery(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, search: "vpn", size: 10, page: 2}
	filter := model.ArticleFilter{SpaceID: 7, Type: model.ArticleTypeFAQ, State: model.ArticleStateActive}

	if _, _, err := s.List(context.Background(), opts, filter); err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, want := range []string{
		"JOIN kb.space s ON s.id=m.space_id",
		"m.deleted_at IS NULL",
		"s.domain_id=$1",
		"m.subject ILIKE $2",
		"m.space_id=$3",
		"m.type=$4",
		"m.state=$5",
		"ORDER BY m.subject ASC,m.id ASC",
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

func TestArticleLocateRequiresSingleID(t *testing.T) {
	s := &articleStore{db: &fakeQuerier{}}

	for _, ids := range [][]int64{nil, {1, 2}} {
		if _, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: ids}); errors.Code(err) != codes.InvalidArgument {
			t.Fatalf("ids %v: error = %v, want InvalidArgument", ids, err)
		}
	}
}

func TestArticleCreateRootRendersCTE(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.Article{SpaceID: 7, Subject: "VPN setup", Tags: []string{"vpn"}}

	created, err := s.Create(context.Background(), opts, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID != 1 {
		t.Fatalf("created = %+v", created)
	}

	for _, want := range []string{
		"WITH m AS (INSERT INTO kb.article",
		"SELECT s.id, NULL, 1,",
		"FROM kb.space s WHERE s.id = $1 AND s.domain_id = $2",
		"RETURNING *",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// Unset type and state fall back to the default codes.
	if f.gotArgs[2] != model.ArticleTypeArticle || f.gotArgs[5] != model.ArticleStateDraft {
		t.Errorf("args = %v, want default type and state codes", f.gotArgs)
	}
}

func TestArticleCreateChildDerivesDepth(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(2)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.Article{SpaceID: 7, ParentID: 1, Type: model.ArticleTypeFAQ, Subject: "child", State: model.ArticleStateActive}

	if _, err := s.Create(context.Background(), opts, in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, want := range []string{
		"p.depth + 1",
		"JOIN kb.article p ON p.space_id = s.id AND p.id = $3 AND p.deleted_at IS NULL",
		"WHERE s.id = $1 AND s.domain_id = $2",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[2] != int64(1) {
		t.Errorf("args[2] = %v, want the parent id", f.gotArgs[2])
	}
}

func TestArticleUpdateRendersGuardedCAS(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}, id: 1}
	in := &model.Article{Subject: "renamed", Type: model.ArticleTypeArticle, State: model.ArticleStateActive}

	if _, err := s.Update(context.Background(), opts, in, 3); err != nil {
		t.Fatalf("Update: %v", err)
	}

	for _, want := range []string{
		"AND m.ver = $",
		"ver = m.ver + 1",
		"m.deleted_at IS NULL",
		"EXISTS (SELECT 1 FROM kb.space s WHERE s.id = m.space_id AND s.domain_id = $",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// The immutable columns stay out of the SET list entirely.
	setList := f.gotSQL[strings.Index(f.gotSQL, "SET "):strings.Index(f.gotSQL, " WHERE")]
	for _, absent := range []string{"space_id", "parent_id", "depth", "index_state", "published_version_id"} {
		if strings.Contains(setList, absent) {
			t.Errorf("SET list %q must not touch %q", setList, absent)
		}
	}
}

func TestArticleUpdateVersionConflict(t *testing.T) {
	// The guarded write matches nothing while the row exists: version conflict.
	f := &fakeQuerier{row: fakeRow{vals: []any{int32(4)}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}, id: 1}

	_, err := s.Update(context.Background(), opts, &model.Article{Subject: "x"}, 3)
	if errors.Code(err) != codes.Aborted {
		t.Fatalf("error = %v, want Aborted", err)
	}

	if len(f.sqls) != 2 || !strings.Contains(f.sqls[1], "SELECT m.ver FROM kb.article m") {
		t.Fatalf("statements = %q, want the write and the version read", f.sqls)
	}
}

func TestArticleUpdateNotFoundStands(t *testing.T) {
	// The guarded write matches nothing and the row is absent: not found.
	f := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}, id: 1}

	_, err := s.Update(context.Background(), opts, &model.Article{Subject: "x"}, 3)
	if errors.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want NotFound", err)
	}
}

func TestArticleDeleteRendersSubtreeCascade(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}, id: 1}

	deleted, err := s.Delete(context.Background(), opts, 3)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if deleted.ID != 1 {
		t.Fatalf("deleted = %+v", deleted)
	}

	for _, want := range []string{
		"WITH RECURSIVE root AS (",
		// The guards sit inside the root UPDATE, where a concurrent writer
		// cannot slip past them.
		"UPDATE kb.article a SET deleted_at = now(), state = $4,",
		"WHERE a.id = $1 AND s.id = a.space_id AND s.domain_id = $2",
		"AND a.ver = $3 AND a.deleted_at IS NULL",
		// The walk starts from the written root, so a guard miss deletes nothing.
		"SELECT root.id FROM root",
		"UNION ALL",
		"FROM tree WHERE a.id = tree.id AND a.id <> $1",
		"SELECT * FROM root",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if strings.Contains(f.gotSQL, "DELETE FROM kb.article") {
		t.Errorf("delete must be soft: %q", f.gotSQL)
	}

	if f.gotArgs[3] != model.ArticleStateInactive {
		t.Errorf("args[3] = %v, want the inactive state code", f.gotArgs[3])
	}
}

func TestArticleDeleteVersionConflict(t *testing.T) {
	f := &fakeQuerier{row: fakeRow{vals: []any{int32(4)}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}, id: 1}

	if _, err := s.Delete(context.Background(), opts, 3); errors.Code(err) != codes.Aborted {
		t.Fatalf("error = %v, want Aborted", err)
	}
}

func TestArticleScanMapsRecord(t *testing.T) {
	now := time.Now()

	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{
			"id", "domain_id", "space_id", "space_name", "parent_id", "depth", "type",
			"subject", "tags", "state", "index_state", "published_version_id", "ver",
			"created_at", "updated_at", "created_by_id", "created_by_name",
		},
		vals: [][]any{{
			int64(1), int64(5), ptrTo(int64(7)), ptrTo("Sales"), ptrTo(int64(3)), int32(2), int32(1),
			"VPN setup",
			[]string{"vpn", "howto"},
			int32(2), int32(3), ptrTo(int64(11)), int32(4),
			now, now, ptrTo(int64(9)), ptrTo("Admin"),
		}},
	}}
	s := &articleStore{db: f}

	got, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: []int64{1}})
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	want := &model.Article{
		ID: 1, DomainID: 5,
		Space: &model.Lookup{ID: 7, Name: "Sales"}, SpaceID: 7,
		ParentID: 3, Depth: 2, Type: model.ArticleTypeArticle,
		Subject: "VPN setup", Tags: []string{"vpn", "howto"},
		State: model.ArticleStateActive, IndexState: model.IndexStateIndexed,
		PublishedVersionID: 11, Ver: 4,
		CreatedAt: now, UpdatedAt: now,
		CreatedBy: &model.Lookup{ID: 9, Name: "Admin"},
	}

	if got.ID != want.ID || got.DomainID != want.DomainID || got.SpaceID != want.SpaceID ||
		got.ParentID != want.ParentID || got.Depth != want.Depth || got.Type != want.Type ||
		got.Subject != want.Subject || got.State != want.State || got.IndexState != want.IndexState ||
		got.PublishedVersionID != want.PublishedVersionID || got.Ver != want.Ver {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if len(got.Tags) != 2 || got.Tags[0] != "vpn" {
		t.Fatalf("tags = %v", got.Tags)
	}

	if got.Space == nil || got.Space.Name != "Sales" || got.CreatedBy == nil || got.CreatedBy.Name != "Admin" {
		t.Fatalf("lookups = %+v, %+v", got.Space, got.CreatedBy)
	}
}

func TestArticleMoveUnderParent(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(11)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}, id: 11}

	moved, err := s.Move(context.Background(), opts, 21, 3)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}

	if moved.ID != 11 {
		t.Fatalf("moved = %+v", moved)
	}

	for _, want := range []string{
		"WITH RECURSIVE subtree AS (",
		// The guards live inside the root UPDATE, where a concurrent writer
		// cannot slip past them.
		"WHERE a.id = $1 AND a.ver = $3 AND a.deleted_at IS NULL",
		"s.id = a.space_id AND s.domain_id = $2",
		"p.id = $5 AND p.space_id = a.space_id AND p.deleted_at IS NULL",
		"p.id NOT IN (SELECT id FROM subtree)",
		fmt.Sprintf("p.depth + 1 + (SELECT value FROM height) <= %d", model.MaxArticleDepth),
		// Descendants take an absolute depth from their level in the walk, so a
		// concurrent move of an ancestor cannot skew them; they join the root,
		// so a guard miss moves nothing, and the root itself is excluded,
		// because one row may not be written twice per statement.
		"SET depth = root.depth + subtree.level",
		"FROM subtree, root WHERE a.id = subtree.id AND a.id <> $1",
		"SELECT c.id, t.level + 1 FROM kb.article c JOIN subtree t ON c.parent_id = t.id",
		"ver = a.ver + 1",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[4] != int64(21) {
		t.Errorf("args[4] = %v, want the new parent", f.gotArgs[4])
	}
}

func TestArticleMoveToTopLevel(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(11)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}, id: 11}

	if _, err := s.Move(context.Background(), opts, 0, 3); err != nil {
		t.Fatalf("Move: %v", err)
	}

	for _, want := range []string{
		"SET parent_id = NULL, depth = 1, ver = a.ver + 1",
		fmt.Sprintf("1 + (SELECT value FROM height) <= %d", model.MaxArticleDepth),
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// No parent argument: the top level has none to join.
	if len(f.gotArgs) != 4 {
		t.Errorf("args = %v, want four of them", f.gotArgs)
	}
}

func TestArticleMoveRejectionReasons(t *testing.T) {
	// A move can miss for two very different reasons, and only one of them is
	// worth retrying: a stale version is a conflict, while a destination the
	// article may not take is a permanent rejection.
	tests := []struct {
		name      string
		storedVer int32
		scanErr   error
		expectVer int32
		wantCode  codes.Code
		wantID    string
	}{
		{
			name: "stale version conflicts", storedVer: 4, expectVer: 3,
			wantCode: codes.Aborted, wantID: "kb.article.version_conflict",
		},
		{
			name: "current version means the destination refused", storedVer: 3, expectVer: 3,
			wantCode: codes.InvalidArgument, wantID: "kb.article.move_rejected",
		},
		{
			name: "missing article stays not found", scanErr: pgx.ErrNoRows, expectVer: 3,
			wantCode: codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{row: fakeRow{vals: []any{tt.storedVer}, err: tt.scanErr}}
			s := &articleStore{db: f}

			opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}, id: 11}

			_, err := s.Move(context.Background(), opts, 21, tt.expectVer)

			if errors.Code(err) != tt.wantCode {
				t.Fatalf("error = %v, want %v", err, tt.wantCode)
			}

			if tt.wantID != "" && errors.ID(err) != tt.wantID {
				t.Fatalf("error id = %q, want %q", errors.ID(err), tt.wantID)
			}
		})
	}
}

func TestArticleMovePinsPositionalArgs(t *testing.T) {
	// The arguments are positional: swapping the guard version with the author
	// would compare the optimistic lock against a user id.
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(11)}}}}
	s := &articleStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}, id: 11}

	if _, err := s.Move(context.Background(), opts, 21, 3); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if f.gotArgs[0] != int64(11) || f.gotArgs[1] != int64(5) || f.gotArgs[2] != int32(3) {
		t.Fatalf("args = %v, want the article, the domain and the expected version", f.gotArgs)
	}

	author, ok := f.gotArgs[3].(*int64)
	if !ok || author == nil || *author != 9 {
		t.Fatalf("args[3] = %v, want the author", f.gotArgs[3])
	}
}

func TestArticleAncestorsWalkUp(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}}

	if _, err := s.Ancestors(context.Background(), opts, 12); err != nil {
		t.Fatalf("Ancestors: %v", err)
	}

	for _, want := range []string{
		"WITH RECURSIVE anc AS (",
		"JOIN kb.article p ON p.id = a.parent_id",
		"WHERE a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL AND p.deleted_at IS NULL",
		"SELECT g.* FROM kb.article g JOIN anc ON anc.parent_id = g.id",
		"FROM anc m",
		"ORDER BY m.depth ASC",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[0] != int64(12) || f.gotArgs[1] != int64(5) {
		t.Errorf("args = %v, want the article and the domain", f.gotArgs)
	}
}

func TestArticleTreeScopesAndNests(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{"id", "parent_id", "subject", "type", "depth"},
		vals: [][]any{
			{int64(1), int64(0), "A", int32(1), int32(1)},
			{int64(2), int64(1), "A1", int32(1), int32(2)},
		},
	}}
	s := &articleStore{db: f}

	roots, err := s.Tree(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7)
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}

	if len(roots) != 1 || len(roots[0].Children) != 1 || roots[0].Children[0].ID != 2 {
		t.Fatalf("tree = %+v", roots)
	}

	for _, want := range []string{
		"s.domain_id = $1 AND m.space_id = $2 AND m.deleted_at IS NULL",
		"ORDER BY m.parent_id NULLS FIRST, m.subject, m.id",
		"LIMIT $3",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[2] != maxTreeNodes+1 {
		t.Errorf("args[2] = %v, want one past the ceiling", f.gotArgs[2])
	}
}

func TestArticleTreeRefusesAnOversizedSpace(t *testing.T) {
	vals := make([][]any, 0, maxTreeNodes+1)
	for i := range maxTreeNodes + 1 {
		vals = append(vals, []any{int64(i + 1), int64(0), "A", int32(1), int32(1)})
	}

	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{"id", "parent_id", "subject", "type", "depth"},
		vals: vals,
	}}
	s := &articleStore{db: f}

	_, err := s.Tree(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7)

	if errors.Code(err) != codes.ResourceExhausted || errors.ID(err) != "kb.article.tree_too_large" {
		t.Fatalf("error = %v, want the tree ceiling", err)
	}
}

func TestArticleSubtreeCarriesDepth(t *testing.T) {
	// The caller validates a move against the height of the subtree, so the
	// depth has to travel with the ids.
	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{"id", "depth"},
		vals: [][]any{{int64(11), int32(2)}, {int64(12), int32(3)}},
	}}
	s := &articleStore{db: f}

	nodes, err := s.Subtree(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 11)
	if err != nil {
		t.Fatalf("Subtree: %v", err)
	}

	if len(nodes) != 2 || nodes[0].ID != 11 || nodes[0].Depth != 2 || nodes[1].Depth != 3 {
		t.Fatalf("nodes = %+v", nodes)
	}

	if !strings.Contains(f.gotSQL, "a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL") {
		t.Errorf("SQL %q misses the scope", f.gotSQL)
	}

	if f.gotArgs[0] != int64(11) || f.gotArgs[1] != int64(5) {
		t.Errorf("args = %v, want the article and the caller domain", f.gotArgs)
	}
}

func TestArticleSuggestTags(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"t"}, vals: [][]any{{"howto"}, {"hr"}}}}
	s := &articleStore{db: f}

	tags, err := s.SuggestTags(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7, "h%", 0)
	if err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}

	if len(tags) != 2 || tags[0] != "howto" {
		t.Fatalf("tags = %v", tags)
	}

	for _, want := range []string{
		"SELECT DISTINCT t FROM kb.article m",
		"CROSS JOIN LATERAL unnest(m.tags) AS t",
		"s.domain_id = $1 AND m.space_id = $2 AND m.deleted_at IS NULL AND t ILIKE $3",
		"ORDER BY t LIMIT $4",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// The prefix is escaped, and a non-positive size falls back to a bound.
	if f.gotArgs[2] != `h\%%` {
		t.Errorf("args[2] = %q, want the escaped prefix", f.gotArgs[2])
	}

	if f.gotArgs[3] != defaultSuggestSize {
		t.Errorf("args[3] = %v, want the default size", f.gotArgs[3])
	}

	if f.gotArgs[0] != int64(5) || f.gotArgs[1] != int64(7) {
		t.Errorf("args = %v, want the caller domain and the space", f.gotArgs)
	}
}

func TestArticleSuggestTagsHonoursSize(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"t"}}}
	s := &articleStore{db: f}

	if _, err := s.SuggestTags(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7, "h", 3); err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}

	if f.gotArgs[3] != 3 {
		t.Errorf("args[3] = %v, want the requested size", f.gotArgs[3])
	}
}

func TestArticleListAppliesParentAndTagFilters(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleStore{db: f}

	filter := model.ArticleFilter{ParentID: ptrTo(int64(0)), Tags: []string{"vpn"}, TagsMatchAll: true}

	if _, _, err := s.List(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, filter); err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, want := range []string{"m.parent_id IS NULL", "m.tags@>$"} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}
}

func TestArticleLocateForUpdateLocks(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &articleStore{db: f}

	if _, err := s.LocateForUpdate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: []int64{1}}); err != nil {
		t.Fatalf("LocateForUpdate: %v", err)
	}

	if !strings.Contains(f.gotSQL, "FOR UPDATE OF m") {
		t.Fatalf("SQL %q does not lock the row", f.gotSQL)
	}
}

func TestArticleAcquireSpaceMoveLock(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleStore{db: f}

	if err := s.AcquireSpaceMoveLock(context.Background(), 7); err != nil {
		t.Fatalf("AcquireSpaceMoveLock: %v", err)
	}

	if !strings.Contains(f.gotSQL, "pg_advisory_xact_lock") {
		t.Fatalf("SQL %q does not take a transaction advisory lock", f.gotSQL)
	}

	if f.gotArgs[0] != 27491 || f.gotArgs[1] != int64(7) {
		t.Fatalf("args = %v, want the move lock class and the space id", f.gotArgs)
	}
}
