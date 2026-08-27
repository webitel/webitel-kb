package grpc

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/auth"
	kbetag "github.com/webitel/webitel-kb/internal/etag"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/service"
	"github.com/webitel/webitel-kb/internal/store"
)

// articleStoreFake records what the handler path produced and plays back
// preset articles.
type articleStoreFake struct {
	filter    model.ArticleFilter
	search    string
	size      int
	fields    []string
	listCalls int

	items   []*model.Article
	written *model.Article

	movedTo   int64
	suggested struct {
		spaceID int64
		prefix  string
		size    int
	}
}

func (f *articleStoreFake) List(
	_ context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	f.listCalls++
	f.filter = filter
	f.search = opts.GetSearch()
	f.size = opts.GetSize()
	f.fields = opts.GetFields()

	return f.items, false, nil
}

func (f *articleStoreFake) Locate(context.Context, options.Searcher) (*model.Article, error) {
	if len(f.items) == 0 {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.items[0], nil
}

func (f *articleStoreFake) LocateForUpdate(context.Context, options.Searcher) (*model.Article, error) {
	return f.written, nil
}

func (f *articleStoreFake) Create(context.Context, options.Creator, *model.Article) (*model.Article, error) {
	return f.written, nil
}

func (f *articleStoreFake) Update(
	context.Context, options.Updator, *model.Article, int32,
) (*model.Article, error) {
	return f.written, nil
}

func (f *articleStoreFake) Delete(context.Context, options.Deleter, int32) (*model.Article, error) {
	return f.written, nil
}

func (f *articleStoreFake) Move(
	_ context.Context, _ options.Updator, newParentID int64, _ int32,
) (*model.Article, error) {
	f.movedTo = newParentID

	return f.written, nil
}

func (f *articleStoreFake) Ancestors(context.Context, options.Searcher, int64) ([]*model.Article, error) {
	return f.items, nil
}

func (f *articleStoreFake) Tree(context.Context, options.Searcher, int64) ([]*model.TreeNode, error) {
	return []*model.TreeNode{
		{ID: 1, Subject: "root", Type: model.ArticleTypeArticle, Depth: 1, Children: []*model.TreeNode{
			{ID: 2, Subject: "child", Type: model.ArticleTypeFAQ, Depth: 2},
		}},
	}, nil
}

func (f *articleStoreFake) Subtree(context.Context, options.Searcher, int64) ([]model.SubtreeNode, error) {
	return []model.SubtreeNode{{ID: 7, Depth: 1}}, nil
}

func (f *articleStoreFake) SuggestTags(
	_ context.Context, _ options.Searcher, spaceID int64, prefix string, size int,
) ([]string, error) {
	f.suggested.spaceID, f.suggested.prefix, f.suggested.size = spaceID, prefix, size

	return []string{"vpn"}, nil
}

func (f *articleStoreFake) AcquireSpaceMoveLock(context.Context, int64) error { return nil }

// articleVersionStoreFake plays back the version history.
type articleVersionStoreFake struct {
	items    []*model.ArticleVersion
	created  *model.ArticleVersion
	createIn *model.ArticleVersion
	size     int
}

func (f *articleVersionStoreFake) List(
	_ context.Context, opts options.Searcher, _ int64,
) ([]*model.ArticleVersion, bool, error) {
	f.size = opts.GetSize()

	return f.items, false, nil
}

func (f *articleVersionStoreFake) Locate(
	context.Context, options.Searcher, int64, int32,
) (*model.ArticleVersion, error) {
	if len(f.items) == 0 {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.items[0], nil
}

func (f *articleVersionStoreFake) Create(
	_ context.Context, _ options.Creator, in *model.ArticleVersion, _ string,
) (*model.ArticleVersion, error) {
	f.createIn = in

	return f.created, nil
}

// articleUoWFake hands out the article fakes.
type articleUoWFake struct {
	articles *articleStoreFake
	versions *articleVersionStoreFake
}

func (f *articleUoWFake) WithinTransaction(
	ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error,
) error {
	return fn(ctx, f)
}

func (f *articleUoWFake) EmbeddingModelStore() store.EmbeddingModelStore { return nil }
func (f *articleUoWFake) SpaceStore() store.SpaceStore                   { return nil }
func (f *articleUoWFake) ArticleStore() store.ArticleStore               { return f.articles }
func (f *articleUoWFake) ArticleVersionStore() store.ArticleVersionStore { return f.versions }

func newArticleServers(uow *articleUoWFake) (*ArticlesServer, *VersionsServer, *TagsServer) {
	svc := service.NewArticleService(uow, slog.New(slog.DiscardHandler))

	return NewArticlesServer(svc), NewVersionsServer(svc), NewTagsServer(svc)
}

func articleContext() context.Context {
	return auth.WithSession(context.Background(), modelSession{})
}

func TestArticleEnumParity(t *testing.T) {
	// The model codes are the wire codes; a drift would silently reinterpret
	// stored rows.
	tests := []struct {
		name  string
		proto int32
		code  int32
	}{
		{name: "type article", proto: int32(kb.ArticleType_ARTICLE), code: model.ArticleTypeArticle},
		{name: "type faq", proto: int32(kb.ArticleType_FAQ), code: model.ArticleTypeFAQ},
		{name: "state draft", proto: int32(kb.ArticleState_DRAFT), code: model.ArticleStateDraft},
		{name: "state active", proto: int32(kb.ArticleState_ACTIVE), code: model.ArticleStateActive},
		{name: "state inactive", proto: int32(kb.ArticleState_INACTIVE), code: model.ArticleStateInactive},
		{name: "index pending", proto: int32(kb.IndexState_PENDING), code: model.IndexStatePending},
		{name: "index indexing", proto: int32(kb.IndexState_INDEXING), code: model.IndexStateIndexing},
		{name: "index indexed", proto: int32(kb.IndexState_INDEXED), code: model.IndexStateIndexed},
		{name: "index failed", proto: int32(kb.IndexState_FAILED), code: model.IndexStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.proto != tt.code {
				t.Fatalf("proto = %d, model = %d", tt.proto, tt.code)
			}
		})
	}
}

func TestArticleToProto(t *testing.T) {
	now := time.Now()

	got, err := articleToProto(&model.Article{
		ID: 7, DomainID: 1, Space: &model.Lookup{ID: 3, Name: "Sales"},
		ParentID: 2, Depth: 2, Type: model.ArticleTypeFAQ, Subject: "VPN",
		Tags: []string{"vpn"}, State: model.ArticleStateActive,
		IndexState: model.IndexStateIndexed, PublishedVersionID: 40, Ver: 4,
		CreatedAt: now, CreatedBy: &model.Lookup{ID: 9, Name: "Admin"},
	})
	if err != nil {
		t.Fatalf("articleToProto: %v", err)
	}

	if got.GetType() != kb.ArticleType_FAQ || got.GetState() != kb.ArticleState_ACTIVE ||
		got.GetIndexState() != kb.IndexState_INDEXED {
		t.Fatalf("enums = %v/%v/%v", got.GetType(), got.GetState(), got.GetIndexState())
	}

	if got.GetSpace().GetName() != "Sales" || got.GetCreatedBy().GetId() != 9 {
		t.Fatalf("lookups = %+v / %+v", got.GetSpace(), got.GetCreatedBy())
	}

	if got.GetCreatedAt() != now.UnixMilli() || got.GetUpdatedAt() != 0 {
		t.Fatalf("timestamps = %d / %d", got.GetCreatedAt(), got.GetUpdatedAt())
	}

	// The etag must decode back to the pair it was built from.
	id, ver, err := kbetag.Parse(kbetag.TypeArticle, got.GetEtag())
	if err != nil {
		t.Fatalf("parse etag: %v", err)
	}

	if id != 7 || ver != 4 {
		t.Fatalf("etag decodes to (%d, %d), want (7, 4)", id, ver)
	}
}

func TestVersionToProtoBody(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantDoc bool
		wantErr codes.Code
	}{
		{name: "document is decoded", body: []byte(`{"type":"doc"}`), wantDoc: true},
		{name: "a narrow field set leaves it out", body: nil},
		{name: "broken json is internal", body: []byte(`{`), wantErr: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := versionToProto(&model.ArticleVersion{ID: 40, BodyRichText: tt.body})

			if tt.wantErr != 0 {
				if errors.Code(err) != tt.wantErr {
					t.Fatalf("err = %v, want %s", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("versionToProto: %v", err)
			}

			if hasDoc := got.GetBodyRichText() != nil; hasDoc != tt.wantDoc {
				t.Fatalf("body present = %v, want %v", hasDoc, tt.wantDoc)
			}
		})
	}
}

func TestListArticlesFullPath(t *testing.T) {
	articles := &articleStoreFake{items: []*model.Article{{ID: 7, Ver: 4, Subject: "VPN"}}}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles})

	resp, err := server.ListArticles(articleContext(), &kb.ListArticlesRequest{
		SpaceId: 3, Q: "vpn", Tags: []string{"vpn"},
		State: kb.ArticleState_ACTIVE, Type: kb.ArticleType_FAQ,
	})
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}

	if len(resp.GetItems()) != 1 || resp.GetItems()[0].GetEtag() == "" {
		t.Fatalf("items = %+v", resp.GetItems())
	}

	// Every filter of the request reaches the store.
	if articles.filter.SpaceID != 3 || articles.filter.State != model.ArticleStateActive ||
		articles.filter.Type != model.ArticleTypeFAQ || len(articles.filter.Tags) != 1 {
		t.Fatalf("filter = %+v", articles.filter)
	}

	if articles.search != "vpn" {
		t.Fatalf("search = %q", articles.search)
	}
}

func TestNarrowProjectionStillRendersTheEtag(t *testing.T) {
	articles := &articleStoreFake{items: []*model.Article{{ID: 7, Ver: 4, Subject: "VPN"}}}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles})

	resp, err := server.ListArticles(articleContext(), &kb.ListArticlesRequest{
		SpaceId: 3, Fields: []string{"subject"},
	})
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}

	if resp.GetItems()[0].GetEtag() == "" {
		t.Fatal("the response carries no etag")
	}

	if !slices.Contains(articles.fields, "subject") {
		t.Fatalf("fields = %v, want the caller selection", articles.fields)
	}
}

func TestListArticlesWithoutSession(t *testing.T) {
	server, _, _ := newArticleServers(&articleUoWFake{articles: &articleStoreFake{}})

	_, err := server.ListArticles(context.Background(), &kb.ListArticlesRequest{SpaceId: 3})
	if errors.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

func TestListChildrenIsNotPaged(t *testing.T) {
	// A parent may hold more children than a default page, and the contract
	// gives no pager here.
	articles := &articleStoreFake{}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles})

	if _, err := server.ListChildren(articleContext(), &kb.ListChildrenRequest{Id: 7}); err != nil {
		t.Fatalf("ListChildren: %v", err)
	}

	if articles.size != options.UnlimitedSize {
		t.Fatalf("size = %d, want paging disabled", articles.size)
	}

	if articles.filter.ParentID == nil || *articles.filter.ParentID != 7 {
		t.Fatalf("parent filter = %v", articles.filter.ParentID)
	}
}

func TestListChildrenRequiresAParent(t *testing.T) {
	articles := &articleStoreFake{}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles})

	_, err := server.ListChildren(articleContext(), &kb.ListChildrenRequest{})

	if errors.ID(err) != "kb.article.id_required" {
		t.Fatalf("error = %v, want kb.article.id_required", err)
	}

	if articles.listCalls != 0 {
		t.Fatal("a rejected listing must not reach the store")
	}
}

func TestArticleEtagGuards(t *testing.T) {
	articles := &articleStoreFake{
		items:   []*model.Article{{ID: 7, Ver: 4}},
		written: &model.Article{ID: 7, Ver: 5},
	}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles})
	ctx := articleContext()

	full, err := kbetag.Encode(kbetag.TypeArticle, 7, 4)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	tests := []struct {
		name string
		call func(tag string) error
	}{
		{name: "update", call: func(tag string) error {
			_, err := server.UpdateArticle(ctx, &kb.UpdateArticleRequest{
				Etag: tag, Input: &kb.InputArticle{Subject: "VPN"},
			})

			return err
		}},
		{name: "delete", call: func(tag string) error {
			_, err := server.DeleteArticle(ctx, &kb.DeleteArticleRequest{Etag: tag})

			return err
		}},
		{name: "move", call: func(tag string) error {
			_, err := server.MoveArticle(ctx, &kb.MoveArticleRequest{Etag: tag, NewParentId: 0})

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(full); err != nil {
				t.Fatalf("%s with an etag: %v", tt.name, err)
			}

			// A bare id carries no version, so it cannot guard a mutation.
			if err := tt.call("7"); errors.Code(err) != codes.InvalidArgument {
				t.Fatalf("%s with a bare id: err = %v, want InvalidArgument", tt.name, err)
			}
		})
	}

	// Reads are lenient: a bare id is a valid locator.
	if _, err := server.LocateArticle(ctx, &kb.LocateArticleRequest{Etag: "7"}); err != nil {
		t.Fatalf("LocateArticle with a bare id: %v", err)
	}
}

func TestCreateArticleCarriesTheBody(t *testing.T) {
	articles := &articleStoreFake{written: &model.Article{ID: 7, Ver: 0, Subject: "VPN"}}
	versions := &articleVersionStoreFake{created: &model.ArticleVersion{ID: 40}}
	server, _, _ := newArticleServers(&articleUoWFake{articles: articles, versions: versions})

	doc, err := structpb.NewStruct(map[string]any{
		"type": "doc",
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("build document: %v", err)
	}

	got, err := server.CreateArticle(articleContext(), &kb.CreateArticleRequest{
		Input: &kb.InputArticle{SpaceId: 3, Subject: "VPN", BodyRichText: doc},
	})
	if err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	if got.GetEtag() == "" {
		t.Fatal("the response carries no etag")
	}
}

func TestCreateArticleWithoutInput(t *testing.T) {
	server, _, _ := newArticleServers(&articleUoWFake{articles: &articleStoreFake{}})

	_, err := server.CreateArticle(articleContext(), &kb.CreateArticleRequest{})
	if errors.ID(err) != "kb.article.input_required" {
		t.Fatalf("err = %v", err)
	}
}

func TestGetTreeMapsRecursively(t *testing.T) {
	server, _, _ := newArticleServers(&articleUoWFake{articles: &articleStoreFake{}})

	resp, err := server.GetTree(articleContext(), &kb.GetTreeRequest{SpaceId: 3})
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}

	if len(resp.GetNodes()) != 1 {
		t.Fatalf("nodes = %+v", resp.GetNodes())
	}

	root := resp.GetNodes()[0]
	if len(root.GetChildren()) != 1 || root.GetChildren()[0].GetType() != kb.ArticleType_FAQ {
		t.Fatalf("children = %+v", root.GetChildren())
	}
}

func TestSuggestTagsFullPath(t *testing.T) {
	articles := &articleStoreFake{}
	_, _, tags := newArticleServers(&articleUoWFake{articles: articles})

	resp, err := tags.SuggestTags(articleContext(), &kb.SuggestTagsRequest{SpaceId: 3, Q: "v", Size: 5})
	if err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}

	if len(resp.GetTags()) != 1 {
		t.Fatalf("tags = %v", resp.GetTags())
	}

	if articles.suggested.spaceID != 3 || articles.suggested.prefix != "v" || articles.suggested.size != 5 {
		t.Fatalf("suggest args = %+v", articles.suggested)
	}
}

func TestListVersionsDefaultsToTheDisplayLimit(t *testing.T) {
	versions := &articleVersionStoreFake{items: []*model.ArticleVersion{{ID: 40, VersionNumber: 1}}}
	_, server, _ := newArticleServers(&articleUoWFake{articles: &articleStoreFake{}, versions: versions})

	resp, err := server.ListVersions(articleContext(), &kb.ListVersionsRequest{ArticleId: 7})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	if len(resp.GetItems()) != 1 {
		t.Fatalf("items = %+v", resp.GetItems())
	}

	if versions.size != options.DefaultSearchSize {
		t.Fatalf("size = %d, want the display limit", versions.size)
	}
}

func TestRestoreVersionRefusesALongNote(t *testing.T) {
	_, versions, _ := newArticleServers(&articleUoWFake{
		articles: &articleStoreFake{},
		versions: &articleVersionStoreFake{},
	})

	_, err := versions.RestoreVersion(articleContext(), &kb.RestoreVersionRequest{
		ArticleId: 7, VersionNumber: 1,
		Notes: strings.Repeat("a", model.MaxVersionNotes+1),
	})

	if errors.ID(err) != "kb.article.notes_too_long" {
		t.Fatalf("error = %v, want the note limit", err)
	}
}

func TestRestoreVersionTrimsTheNote(t *testing.T) {
	versionsFake := &articleVersionStoreFake{
		items:   []*model.ArticleVersion{{ID: 30, Subject: "old"}},
		created: &model.ArticleVersion{ID: 40},
	}
	_, versions, _ := newArticleServers(&articleUoWFake{
		articles: &articleStoreFake{written: &model.Article{ID: 7, Ver: 4}},
		versions: versionsFake,
	})

	if _, err := versions.RestoreVersion(articleContext(), &kb.RestoreVersionRequest{
		ArticleId: 7, VersionNumber: 1, Notes: "  a note  ",
	}); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}

	if versionsFake.createIn.Notes != "a note" {
		t.Fatalf("notes = %q, want them trimmed", versionsFake.createIn.Notes)
	}
}
