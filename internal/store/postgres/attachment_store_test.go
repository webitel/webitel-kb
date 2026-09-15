package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

func TestAttachmentListRendersScopedQuery(t *testing.T) {
	f := &fakeQuerier{}
	s := &attachmentStore{db: f}

	opts := &fakeSearchOpts{
		auth: fakeAuther{domainID: 5}, search: "vpn", ids: []int64{3}, size: 10, page: 2,
	}

	if _, _, err := s.List(context.Background(), opts, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, want := range []string{
		"FROM storage.files m",
		"m.domain_id=$1",
		"m.uuid=$2",
		"a.id=$3 AND s.domain_id=m.domain_id AND a.deleted_at IS NULL",
		"m.channel=$4",
		"m.removed IS NOT TRUE",
		"COALESCE(m.view_name,m.name)ILIKE $5",
		"m.id=ANY($6)",
		"ORDER BY m.uploaded_at DESC,m.id ASC",
		"LIMIT 11",
		"OFFSET 10",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[0] != int64(5) || f.gotArgs[1] != "7" || f.gotArgs[2] != int64(7) || f.gotArgs[3] != "knowledgebase" {
		t.Errorf("args = %v, want the domain, the article as text and as id, and the channel", f.gotArgs)
	}
}

func TestAttachmentListKeepsTiebreaker(t *testing.T) {
	f := &fakeQuerier{}
	s := &attachmentStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, sort: "+name"}

	if _, _, err := s.List(context.Background(), opts, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	if !strings.Contains(f.gotSQL, "ORDER BY COALESCE(m.view_name,m.name)ASC,m.id ASC") {
		t.Fatalf("SQL %q does not fall back to the id", f.gotSQL)
	}
}

func TestAttachmentListReportsNextPage(t *testing.T) {
	rows := &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	s := &attachmentStore{db: &fakeQuerier{rows: rows}}

	items, next, err := s.List(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 2, page: 1}, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(items) != 2 || !next {
		t.Fatalf("items = %d, next = %v; want the page and a next flag", len(items), next)
	}
}

func TestAttachmentListScanMapsRecord(t *testing.T) {
	now := time.Now()

	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{"id", "name", "size", "mime", "created_at", "created_by_id", "created_by_name", "source"},
		vals: [][]any{
			{int64(4), "guide.pdf", int64(2048), ptrTo("application/pdf"), ptrTo(now), ptrTo(int64(9)), ptrTo("Admin"), ptrTo("knowledgebase")},
			{int64(5), "raw.bin", int64(1), nil, nil, nil, nil, nil},
		},
	}}
	s := &attachmentStore{db: f}

	items, _, err := s.List(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}

	full := items[0]
	if full.ID != 4 || full.Name != "guide.pdf" || full.Size != 2048 || full.Mime != "application/pdf" || full.Source != "knowledgebase" {
		t.Fatalf("attachment = %+v", full)
	}

	if !full.CreatedAt.Equal(now) || full.CreatedBy == nil || full.CreatedBy.ID != 9 || full.CreatedBy.Name != "Admin" {
		t.Fatalf("created = %v by %+v", full.CreatedAt, full.CreatedBy)
	}

	bare := items[1]
	if bare.Mime != "" || !bare.CreatedAt.IsZero() || bare.CreatedBy != nil || bare.Source != "" || bare.URL != "" {
		t.Fatalf("nullable columns leaked into %+v", bare)
	}
}

func TestAttachmentDeleteFlagsTheRowOfTheArticle(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(4)}}}}
	s := &attachmentStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, id: 4, fields: []string{"id"}}

	deleted, err := s.Delete(context.Background(), opts, 7)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if deleted.ID != 4 {
		t.Fatalf("deleted = %+v", deleted)
	}

	for _, want := range []string{
		"WITH m AS (UPDATE storage.files f SET removed = true",
		"WHERE f.id = $1 AND f.domain_id = $2 AND f.uuid = $3 AND f.channel = $4 AND f.removed IS NOT TRUE",
		"AND EXISTS (SELECT 1 FROM kb.article a JOIN kb.space s ON s.id = a.space_id",
		"WHERE a.id = $5 AND s.domain_id = f.domain_id AND a.deleted_at IS NULL)",
		"RETURNING f.*) SELECT m.id AS id FROM m",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	want := []any{int64(4), int64(5), "7", "knowledgebase", int64(7)}
	for i, arg := range want {
		if f.gotArgs[i] != arg {
			t.Errorf("args[%d] = %v, want %v", i, f.gotArgs[i], arg)
		}
	}
}

func TestAttachmentDeleteOfAnotherFileIsNotFound(t *testing.T) {
	// A file of another article, domain or channel, or one already removed,
	// matches nothing: the read-back is empty.
	s := &attachmentStore{db: &fakeQuerier{err: pgx.ErrNoRows}}

	_, err := s.Delete(context.Background(), &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 4}, 7)

	if errors.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want not found", err)
	}
}
