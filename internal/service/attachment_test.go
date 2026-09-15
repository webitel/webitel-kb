package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// fakeAttachmentStore plays back a page and records the article asked for.
type fakeAttachmentStore struct {
	articleID int64
	items     []*model.Attachment
	next      bool
	err       error
	deleted   *model.Attachment
}

func (f *fakeAttachmentStore) List(
	_ context.Context, _ options.Searcher, articleID int64,
) ([]*model.Attachment, bool, error) {
	f.articleID = articleID

	return f.items, f.next, f.err
}

func (f *fakeAttachmentStore) Delete(
	_ context.Context, _ options.Deleter, articleID int64,
) (*model.Attachment, error) {
	f.articleID = articleID

	return f.deleted, f.err
}

// attachmentUow hands out the attachment fake only.
type attachmentUow struct {
	files *fakeAttachmentStore
}

func (u *attachmentUow) WithinTransaction(
	ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error,
) error {
	return fn(ctx, u)
}

func (u *attachmentUow) EmbeddingModelStore() store.EmbeddingModelStore { return nil }
func (u *attachmentUow) SpaceStore() store.SpaceStore                   { return nil }
func (u *attachmentUow) ArticleStore() store.ArticleStore               { return nil }
func (u *attachmentUow) ArticleVersionStore() store.ArticleVersionStore { return nil }
func (u *attachmentUow) RetrievalStore() store.RetrievalStore           { return nil }
func (u *attachmentUow) OutboxStore() store.OutboxStore                 { return nil }
func (u *attachmentUow) AttachmentStore() store.AttachmentStore         { return u.files }

// fakeLinks records the signing request and plays back links.
type fakeLinks struct {
	calls    int
	domainID int64
	ids      []int64
	links    map[int64]string
	err      error
}

func (f *fakeLinks) Download(_ context.Context, domainID int64, ids []int64) (map[int64]string, error) {
	f.calls++
	f.domainID, f.ids = domainID, ids

	return f.links, f.err
}

// attachmentReadOpts is the read request of a listing.
type attachmentReadOpts struct {
	auth   stubAuther
	fields []string
}

func (o *attachmentReadOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *attachmentReadOpts) GetFields() []string      { return o.fields }
func (o *attachmentReadOpts) GetSearch() string        { return "" }
func (o *attachmentReadOpts) GetPage() int             { return 1 }
func (o *attachmentReadOpts) GetSize() int             { return 10 }
func (o *attachmentReadOpts) GetSort() string          { return "" }
func (o *attachmentReadOpts) GetIDs() []int64          { return nil }

func attachmentFixture(items ...*model.Attachment) (*AttachmentService, *fakeAttachmentStore, *fakeLinks, *bytes.Buffer) {
	files := &fakeAttachmentStore{items: items, next: true}
	links := &fakeLinks{links: map[int64]string{}}
	logged := &bytes.Buffer{}
	svc := NewAttachmentService(&attachmentUow{files: files}, links, slog.New(slog.NewTextHandler(logged, nil)))

	return svc, files, links, logged
}

func TestAttachmentListSignsTheLinksOfThePage(t *testing.T) {
	svc, files, links, _ := attachmentFixture(&model.Attachment{ID: 4}, &model.Attachment{ID: 6})
	links.links = map[int64]string{4: "https://kb.example/any/file/4"}

	items, next, err := svc.List(context.Background(), &attachmentReadOpts{auth: stubAuther{domainID: 5}}, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if files.articleID != 7 || !next {
		t.Fatalf("store asked for article %d, next = %v", files.articleID, next)
	}

	if links.calls != 1 || links.domainID != 5 || len(links.ids) != 2 || links.ids[0] != 4 || links.ids[1] != 6 {
		t.Fatalf("signing request = %+v, want one call for both files of domain 5", links)
	}

	if items[0].URL != "https://kb.example/any/file/4" || items[1].URL != "" {
		t.Fatalf("urls = %q / %q, want the signed link and an empty one", items[0].URL, items[1].URL)
	}
}

func TestAttachmentListSkipsSigningWhenNotNeeded(t *testing.T) {
	tests := []struct {
		name   string
		items  []*model.Attachment
		fields []string
	}{
		{name: "an empty page", fields: nil},
		{name: "fields without the link", items: []*model.Attachment{{ID: 4}}, fields: []string{"id", "name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, links, _ := attachmentFixture(tt.items...)

			if _, _, err := svc.List(context.Background(), &attachmentReadOpts{fields: tt.fields}, 7); err != nil {
				t.Fatalf("List: %v", err)
			}

			if links.calls != 0 {
				t.Fatalf("Storage was asked to sign %d times", links.calls)
			}
		})
	}
}

func TestAttachmentListSignsWhenTheLinkIsAmongTheFields(t *testing.T) {
	svc, _, links, _ := attachmentFixture(&model.Attachment{ID: 4})

	if _, _, err := svc.List(context.Background(), &attachmentReadOpts{fields: []string{"id", "url"}}, 7); err != nil {
		t.Fatalf("List: %v", err)
	}

	if links.calls != 1 {
		t.Fatalf("Storage was asked to sign %d times, want once", links.calls)
	}
}

func TestAttachmentListSurvivesAStorageOutage(t *testing.T) {
	svc, _, links, logged := attachmentFixture(&model.Attachment{ID: 4})
	links.err = stderrors.New("storage: unavailable")

	items, _, err := svc.List(context.Background(), &attachmentReadOpts{}, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(items) != 1 || items[0].URL != "" {
		t.Fatalf("items = %+v, want the metadata without a link", items)
	}

	if !strings.Contains(logged.String(), "attachment links are not signed") {
		t.Fatalf("log = %q, want the outage reported", logged.String())
	}
}

func TestAttachmentListPassesStoreErrors(t *testing.T) {
	svc, files, links, _ := attachmentFixture()
	files.err = stderrors.New("boom")

	_, _, err := svc.List(context.Background(), &attachmentReadOpts{}, 7)

	if !stderrors.Is(err, files.err) || links.calls != 0 {
		t.Fatalf("error = %v, signing calls = %d", err, links.calls)
	}
}

func TestAttachmentDeleteDelegatesWithoutSigning(t *testing.T) {
	svc, files, links, _ := attachmentFixture()
	files.deleted = &model.Attachment{ID: 4}

	deleted, err := svc.Delete(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 5}, id: 4}, 7)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if deleted.ID != 4 || deleted.URL != "" || files.articleID != 7 || links.calls != 0 {
		t.Fatalf("deleted = %+v, article = %d, signing calls = %d", deleted, files.articleID, links.calls)
	}
}
