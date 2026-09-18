package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
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
		"FROM kb.attachment m",
		"a.id=m.article_id AND s.domain_id=$1 AND a.deleted_at IS NULL",
		"m.article_id=$2",
		"m.name ILIKE $3",
		"m.file_id=ANY($4)",
		"ORDER BY m.created_at DESC,m.file_id ASC",
		"LIMIT 11",
		"OFFSET 10",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[0] != int64(5) || f.gotArgs[1] != int64(7) {
		t.Errorf("args = %v, want the domain and the article", f.gotArgs)
	}
}

func TestAttachmentListKeepsTiebreaker(t *testing.T) {
	f := &fakeQuerier{}
	s := &attachmentStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, sort: "+name"}

	if _, _, err := s.List(context.Background(), opts, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	if !strings.Contains(f.gotSQL, "ORDER BY m.name ASC,m.file_id ASC") {
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
		cols: []string{"id", "name", "size", "mime", "created_at", "created_by_id", "created_by_name"},
		vals: [][]any{
			{int64(4), "guide.pdf", int64(2048), ptrTo("application/pdf"), ptrTo(now), ptrTo(int64(9)), ptrTo("Admin")},
			{int64(5), "raw.bin", int64(1), nil, nil, nil, nil},
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
	if full.ID != 4 || full.Name != "guide.pdf" || full.Size != 2048 || full.Mime != "application/pdf" {
		t.Fatalf("attachment = %+v", full)
	}

	if !full.CreatedAt.Equal(now) || full.CreatedBy == nil || full.CreatedBy.ID != 9 || full.CreatedBy.Name != "Admin" {
		t.Fatalf("created = %v by %+v", full.CreatedAt, full.CreatedBy)
	}

	bare := items[1]
	if bare.Mime != "" || !bare.CreatedAt.IsZero() || bare.CreatedBy != nil || bare.URL != "" {
		t.Fatalf("nullable columns leaked into %+v", bare)
	}
}

func TestAttachmentAttachBindsTheFileToTheArticle(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(4)}}}}
	s := &attachmentStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.Attachment{ID: 4, Name: "guide.pdf", Mime: "application/pdf", Size: 2048}

	attached, err := s.Attach(context.Background(), opts, 7, in)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	if attached.ID != 4 {
		t.Fatalf("attached = %+v", attached)
	}

	for _, want := range []string{
		"WITH m AS (INSERT INTO kb.attachment (article_id, file_id, name, mime, size, created_by)",
		"FROM kb.article a JOIN kb.space s ON s.id = a.space_id",
		"WHERE a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL",
		"ON CONFLICT (article_id, file_id) DO UPDATE",
		"RETURNING *) SELECT m.file_id AS id FROM m",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	mime, _ := f.gotArgs[4].(*string)
	user, _ := f.gotArgs[6].(*int64)

	if f.gotArgs[0] != int64(7) || f.gotArgs[1] != int64(5) || f.gotArgs[2] != int64(4) ||
		f.gotArgs[3] != "guide.pdf" || mime == nil || *mime != "application/pdf" ||
		f.gotArgs[5] != int64(2048) || user == nil || *user != int64(9) {
		t.Errorf("args = %v, want the article, the domain, the file and its metadata", f.gotArgs)
	}
}

func TestAttachmentAttachToAnotherDomainIsNotFound(t *testing.T) {
	s := &attachmentStore{db: &fakeQuerier{rows: &fakeRows{cols: []string{"id"}}}}

	_, err := s.Attach(context.Background(), &fakeWriteOpts{auth: fakeAuther{domainID: 5}}, 7, &model.Attachment{ID: 4})

	if errors.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestAttachmentDeleteUnbindsTheRowOfTheArticle(t *testing.T) {
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
		"WITH m AS (DELETE FROM kb.attachment m",
		"USING kb.article a JOIN kb.space s ON s.id = a.space_id",
		"WHERE m.article_id = $1 AND m.file_id = $2",
		"AND a.id = m.article_id AND s.domain_id = $3 AND a.deleted_at IS NULL",
		"RETURNING m.*) SELECT m.file_id AS id FROM m",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	want := []any{int64(7), int64(4), int64(5)}
	for i, arg := range want {
		if f.gotArgs[i] != arg {
			t.Errorf("args[%d] = %v, want %v", i, f.gotArgs[i], arg)
		}
	}
}

func TestAttachmentBoundAsksForAnyBinding(t *testing.T) {
	f := &fakeQuerier{row: fakeRow{vals: []any{true}}}
	s := &attachmentStore{db: f}

	bound, err := s.Bound(context.Background(), 4)
	if err != nil {
		t.Fatalf("Bound: %v", err)
	}

	if !bound {
		t.Fatal("bound = false, want the binding reported")
	}

	if !strings.Contains(f.gotSQL, "SELECT EXISTS (SELECT 1 FROM kb.attachment WHERE file_id = $1)") {
		t.Fatalf("SQL = %q", f.gotSQL)
	}

	if len(f.gotArgs) != 1 || f.gotArgs[0] != int64(4) {
		t.Fatalf("args = %v, want the file", f.gotArgs)
	}
}

func TestAttachmentDeleteOfAnotherFileIsNotFound(t *testing.T) {
	// A file of another article or domain matches nothing: the read-back is
	// empty.
	s := &attachmentStore{db: &fakeQuerier{rows: &fakeRows{cols: []string{"id"}}}}

	_, err := s.Delete(context.Background(), &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 4}, 7)

	if errors.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want not found", err)
	}
}
