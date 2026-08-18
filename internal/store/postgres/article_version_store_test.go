package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestArticleVersionListNewestFirst(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleVersionStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 10, page: 2}

	if _, _, err := s.List(context.Background(), opts, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	for _, want := range []string{
		"EXISTS(SELECT 1 FROM kb.article a JOIN kb.space s ON s.id=a.space_id",
		"s.domain_id=$1 AND a.deleted_at IS NULL",
		"m.article_id=$2",
		"ORDER BY m.version_number DESC",
		"LIMIT 11",
		"OFFSET 10",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}
}

func TestArticleVersionListKeepsTiebreaker(t *testing.T) {
	f := &fakeQuerier{}
	s := &articleVersionStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, sort: "+created_at"}

	if _, _, err := s.List(context.Background(), opts, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	if !strings.Contains(f.gotSQL, "ORDER BY m.created_at ASC,m.version_number DESC") {
		t.Fatalf("SQL %q does not fall back to the version number", f.gotSQL)
	}
}

func TestArticleVersionRestoreStaysWithinTheArticle(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(5)}}}}
	s := &articleVersionStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.ArticleVersion{ArticleID: 7, BodyRichText: []byte(`{}`), RestoredFrom: 3}

	if _, err := s.Create(context.Background(), opts, in, model.TextSearchDefault); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !strings.Contains(f.gotSQL, "SELECT 1 FROM kb.article_version src WHERE src.id = $8::bigint AND src.article_id = a.id") {
		t.Fatalf("SQL %q does not bind the restore source to the article", f.gotSQL)
	}
}

func TestArticleVersionLocateByNumber(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(3)}}}}
	s := &articleVersionStore{db: f}

	got, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7, 2)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	if got.ID != 3 {
		t.Fatalf("version = %+v", got)
	}

	for _, want := range []string{"m.article_id=$2", "m.version_number=$3"} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}
}

func TestArticleVersionCreateNumbersAndVectorizes(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(4)}}}}
	s := &articleVersionStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.ArticleVersion{
		ArticleID: 7, Subject: "VPN setup",
		BodyRichText: []byte(`{"type":"doc"}`), BodyMarkdown: "# VPN", BodyPlain: "VPN",
	}

	created, err := s.Create(context.Background(), opts, in, model.TextSearchDefault)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID != 4 {
		t.Fatalf("created = %+v", created)
	}

	for _, want := range []string{
		"WITH m AS (INSERT INTO kb.article_version",
		"(SELECT COALESCE(max(v.version_number), 0) + 1 FROM kb.article_version v WHERE v.article_id = a.id)",
		"to_tsvector($7::regconfig, $6)",
		"WHERE a.id = $1 AND s.domain_id = $2 AND a.deleted_at IS NULL",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[4] != "# VPN" || f.gotArgs[5] != "VPN" {
		t.Errorf("args = %v, want markdown then plain text", f.gotArgs)
	}

	if f.gotArgs[6] != model.TextSearchDefault {
		t.Errorf("args[6] = %v, want the text search configuration", f.gotArgs[6])
	}

	if f.gotArgs[7] != (*int64)(nil) {
		t.Errorf("args[7] = %v, want no restore reference", f.gotArgs[7])
	}
}

func TestArticleVersionCreateCarriesRestoreReference(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(5)}}}}
	s := &articleVersionStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.ArticleVersion{
		ArticleID: 7, Subject: "restored", BodyRichText: []byte(`{}`),
		BodyMarkdown: "m", BodyPlain: "p", RestoredFrom: 3, Notes: "from #1",
	}

	if _, err := s.Create(context.Background(), opts, in, model.TextSearchDefault); err != nil {
		t.Fatalf("Create: %v", err)
	}

	restoredFrom, ok := f.gotArgs[7].(*int64)
	if !ok || restoredFrom == nil || *restoredFrom != 3 {
		t.Errorf("args[7] = %v, want the restored version id", f.gotArgs[7])
	}

	if f.gotArgs[1] != int64(5) || f.gotArgs[9] != ptrTo(int64(9)) && f.gotArgs[9].(*int64) == nil {
		t.Errorf("args = %v, want the caller domain and author", f.gotArgs)
	}
}

func TestArticleVersionCreateLostRace(t *testing.T) {
	// Two savers derived the same next number: the unique index rejects one,
	// and that is a conflict to retry, not a duplicate the caller chose.
	f := &fakeQuerier{err: &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "article_version_article_id_version_number_key"}}
	s := &articleVersionStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}

	_, err := s.Create(context.Background(), opts, &model.ArticleVersion{ArticleID: 7}, model.TextSearchDefault)

	if errors.Code(err) != codes.Aborted || errors.ID(err) != "kb.article.version_race" {
		t.Fatalf("error = %v, want a version race", err)
	}
}

func TestArticleVersionScanMapsRecord(t *testing.T) {
	now := time.Now()

	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{
			"id", "article_id", "version_number", "subject", "body_rich_text",
			"body_markdown", "body_plain", "restored_from", "notes",
			"created_at", "created_by_id", "created_by_name",
		},
		vals: [][]any{{
			int64(4), int64(7), int32(2), "VPN setup", []byte(`{"type":"doc"}`),
			"# VPN", "VPN", ptrTo(int64(3)), ptrTo("restored"),
			now, ptrTo(int64(9)), ptrTo("Admin"),
		}},
	}}
	s := &articleVersionStore{db: f}

	got, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}}, 7, 2)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	if got.ID != 4 || got.ArticleID != 7 || got.VersionNumber != 2 || got.Subject != "VPN setup" {
		t.Fatalf("version = %+v", got)
	}

	if string(got.BodyRichText) != `{"type":"doc"}` || got.BodyMarkdown != "# VPN" || got.BodyPlain != "VPN" {
		t.Fatalf("bodies = %q / %q / %q", got.BodyRichText, got.BodyMarkdown, got.BodyPlain)
	}

	if got.RestoredFrom != 3 || got.Notes != "restored" {
		t.Fatalf("restore fields = %d / %q", got.RestoredFrom, got.Notes)
	}

	if got.CreatedBy == nil || got.CreatedBy.Name != "Admin" {
		t.Fatalf("created_by = %+v", got.CreatedBy)
	}
}

func TestArticleVersionCreatePassesOtherErrors(t *testing.T) {
	// Only a lost race for the derived number becomes a conflict; anything
	// else must reach the caller unchanged.
	tests := []struct {
		name     string
		queryErr error
		wantCode codes.Code
	}{
		{
			name:     "missing article stays not found",
			queryErr: pgx.ErrNoRows,
			wantCode: codes.NotFound,
		},
		{
			name:     "a foreign key violation stays aborted",
			queryErr: &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation},
			wantCode: codes.Aborted,
		},
		{
			name:     "a unique violation on another constraint stays a duplicate",
			queryErr: &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "article_version_pkey"},
			wantCode: codes.AlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{err: tt.queryErr}
			s := &articleVersionStore{db: f}

			opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, fields: []string{"id"}}

			_, err := s.Create(context.Background(), opts, &model.ArticleVersion{ArticleID: 7}, model.TextSearchDefault)

			if errors.Code(err) != tt.wantCode {
				t.Fatalf("error = %v, want %v", err, tt.wantCode)
			}

			if errors.ID(err) == "kb.article.version_race" {
				t.Fatalf("error was reported as a version race: %v", err)
			}
		})
	}
}
