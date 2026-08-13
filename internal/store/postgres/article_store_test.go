package postgres

import (
	"context"
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
