package grpc

import (
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/auth"
	kbetag "github.com/webitel/webitel-kb/internal/etag"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/service"
	"github.com/webitel/webitel-kb/internal/store"
)

// attachmentStoreFake records what the handler path produced.
type attachmentStoreFake struct {
	articleID int64
	search    string
	fields    []string
	sort      string
	ids       []int64
	size      int
	page      int
	deleteID  int64

	items   []*model.Attachment
	deleted *model.Attachment
}

func (f *attachmentStoreFake) List(
	_ context.Context, opts options.Searcher, articleID int64,
) ([]*model.Attachment, bool, error) {
	f.articleID = articleID
	f.search, f.fields, f.sort, f.ids = opts.GetSearch(), opts.GetFields(), opts.GetSort(), opts.GetIDs()
	f.size, f.page = opts.GetSize(), opts.GetPage()

	return f.items, true, nil
}

func (f *attachmentStoreFake) Delete(
	_ context.Context, opts options.Deleter, articleID int64,
) (*model.Attachment, error) {
	f.articleID, f.deleteID = articleID, opts.GetID()

	return f.deleted, nil
}

// attachmentUoWFake hands out the attachment fake only.
type attachmentUoWFake struct {
	files *attachmentStoreFake
}

func (f *attachmentUoWFake) WithinTransaction(
	ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error,
) error {
	return fn(ctx, f)
}

func (f *attachmentUoWFake) EmbeddingModelStore() store.EmbeddingModelStore { return nil }
func (f *attachmentUoWFake) SpaceStore() store.SpaceStore                   { return nil }
func (f *attachmentUoWFake) ArticleStore() store.ArticleStore               { return nil }
func (f *attachmentUoWFake) ArticleVersionStore() store.ArticleVersionStore { return nil }
func (f *attachmentUoWFake) OutboxStore() store.OutboxStore                 { return nil }
func (f *attachmentUoWFake) RetrievalStore() store.RetrievalStore           { return nil }
func (f *attachmentUoWFake) AttachmentStore() store.AttachmentStore         { return f.files }

// linksFake signs every file it is given with a predictable link.
type linksFake struct {
	calls int
}

func (f *linksFake) Download(_ context.Context, _ int64, ids []int64) (map[int64]string, error) {
	f.calls++

	links := make(map[int64]string, len(ids))
	for _, id := range ids {
		links[id] = "https://kb.example/any/file/" + strconv.FormatInt(id, 10)
	}

	return links, nil
}

func attachmentServerWithFakes(files *attachmentStoreFake) (*AttachmentsServer, *linksFake) {
	links := &linksFake{}
	svc := service.NewAttachmentService(&attachmentUoWFake{files: files}, links, slog.New(slog.DiscardHandler))

	return NewAttachmentsServer(svc), links
}

func attachmentContext() context.Context {
	return auth.WithSession(context.Background(), modelSession{})
}

func TestListFilesFullPath(t *testing.T) {
	now := time.Now()
	files := &attachmentStoreFake{items: []*model.Attachment{{
		ID: 4, Name: "guide.pdf", Size: 2048, Mime: "application/pdf", Source: "knowledgebase",
		CreatedAt: now, CreatedBy: &model.Lookup{ID: 9, Name: "Admin"},
	}}}
	server, links := attachmentServerWithFakes(files)

	tag, err := kbetag.Encode(kbetag.TypeArticle, 7, 3)
	if err != nil {
		t.Fatalf("etag: %v", err)
	}

	resp, err := server.ListFiles(attachmentContext(), &kb.ListFilesRequest{
		ArticleEtag: tag, Q: "guide", Fields: []string{"id", "name", "url"}, Sort: "-created_at",
		Ids: []int64{4}, Page: 2, Size: 5,
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}

	if files.articleID != 7 {
		t.Fatalf("article = %d, want the one the etag names", files.articleID)
	}

	if files.search != "guide" || files.sort != "-created_at" || files.size != 5 || files.page != 2 {
		t.Fatalf("options = %+v, want the request criteria", files)
	}

	if len(files.fields) != 3 || len(files.ids) != 1 || files.ids[0] != 4 {
		t.Fatalf("fields = %v, ids = %v", files.fields, files.ids)
	}

	if links.calls != 1 {
		t.Fatalf("signing calls = %d, want one for the page", links.calls)
	}

	if !resp.GetNext() || len(resp.GetItems()) != 1 {
		t.Fatalf("response = %+v", resp)
	}

	got := resp.GetItems()[0]
	if got.GetId() != 4 || got.GetName() != "guide.pdf" || got.GetSize() != 2048 || got.GetMime() != "application/pdf" {
		t.Fatalf("file = %+v", got)
	}

	if got.GetSource() != "knowledgebase" || got.GetUrl() != "https://kb.example/any/file/4" || got.GetCreatedAt() != now.UnixMilli() {
		t.Fatalf("file = %+v, want the source, the signed link and epoch ms", got)
	}

	if got.GetCreatedBy().GetId() != 9 || got.GetCreatedBy().GetName() != "Admin" || got.GetCreatedBy().GetType() != "webitel" {
		t.Fatalf("created_by = %+v, want the uploader as a webitel user", got.GetCreatedBy())
	}
}

func TestListFilesAcceptsABareArticleID(t *testing.T) {
	files := &attachmentStoreFake{}
	server, _ := attachmentServerWithFakes(files)

	resp, err := server.ListFiles(attachmentContext(), &kb.ListFilesRequest{ArticleEtag: "7"})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}

	if files.articleID != 7 {
		t.Fatalf("article = %d, want 7", files.articleID)
	}

	if resp.GetItems() == nil || len(resp.GetItems()) != 0 {
		t.Fatalf("items = %v, want an empty list, not nil", resp.GetItems())
	}
}

func TestListFilesRejectsABrokenLocator(t *testing.T) {
	server, _ := attachmentServerWithFakes(&attachmentStoreFake{})

	_, err := server.ListFiles(attachmentContext(), &kb.ListFilesRequest{ArticleEtag: "not-an-etag"})

	if errors.Code(err) != codes.InvalidArgument || errors.ID(err) != "kb.etag.invalid" {
		t.Fatalf("error = %v, want an invalid etag", err)
	}
}

func TestListFilesRequiresSession(t *testing.T) {
	server, _ := attachmentServerWithFakes(&attachmentStoreFake{})

	_, err := server.ListFiles(context.Background(), &kb.ListFilesRequest{ArticleEtag: "7"})

	if errors.Code(err) != codes.Unauthenticated {
		t.Fatalf("error = %v, want unauthenticated", err)
	}
}

func TestDeleteFileFullPath(t *testing.T) {
	files := &attachmentStoreFake{deleted: &model.Attachment{ID: 4, Name: "guide.pdf"}}
	server, links := attachmentServerWithFakes(files)

	tag, err := kbetag.Encode(kbetag.TypeArticle, 7, 3)
	if err != nil {
		t.Fatalf("etag: %v", err)
	}

	got, err := server.DeleteFile(attachmentContext(), &kb.DeleteFileRequest{ArticleEtag: tag, Id: 4})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	if files.articleID != 7 || files.deleteID != 4 {
		t.Fatalf("delete addressed article %d file %d", files.articleID, files.deleteID)
	}

	if got.GetId() != 4 || got.GetName() != "guide.pdf" || got.GetUrl() != "" || links.calls != 0 {
		t.Fatalf("file = %+v, signing calls = %d; a removed file carries no link", got, links.calls)
	}
}

func TestDeleteFileGuards(t *testing.T) {
	tests := []struct {
		name     string
		req      *kb.DeleteFileRequest
		wantCode codes.Code
		wantID   string
	}{
		{
			name:     "the file id is required",
			req:      &kb.DeleteFileRequest{ArticleEtag: "7"},
			wantCode: codes.InvalidArgument, wantID: "options.delete.id_required",
		},
		{
			name:     "the article locator must parse",
			req:      &kb.DeleteFileRequest{ArticleEtag: "nope", Id: 4},
			wantCode: codes.InvalidArgument, wantID: "kb.etag.invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := &attachmentStoreFake{}
			server, _ := attachmentServerWithFakes(files)

			_, err := server.DeleteFile(attachmentContext(), tt.req)

			if errors.Code(err) != tt.wantCode || errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v (id %q), want %v %q", err, errors.ID(err), tt.wantCode, tt.wantID)
			}

			if files.deleteID != 0 {
				t.Fatal("the store was reached despite the rejected request")
			}
		})
	}
}

func TestFileToProtoWithoutUploader(t *testing.T) {
	got := fileToProto(&model.Attachment{ID: 4})

	if got.GetCreatedBy() != nil || got.GetCreatedAt() != 0 {
		t.Fatalf("file = %+v, want no uploader and a zero time", got)
	}
}
