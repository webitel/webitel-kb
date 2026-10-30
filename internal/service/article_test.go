package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/event"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// fakeArticleStore records calls and plays back preset articles.
type fakeArticleStore struct {
	current  *model.Article // LocateForUpdate result
	located  *model.Article // Locate result
	written  *model.Article // Create/Update/Move/Delete result
	subtree  []model.SubtreeNode
	tags     []string
	tree     []*model.TreeNode
	chain    []*model.Article
	list     []*model.Article
	listNext bool

	locateErr error
	stale     *model.Article

	createIn  *model.Article
	updateIn  *model.Article
	updateVer int32
	moveTo    int64
	moveSpace int64
	moveVer   int32

	lockedSpaceIDs    []int64 // tree locks
	keyLockedSpaceIDs []int64
	locateFields      [][]string
	writeFields       [][]string
	listFilter        model.ArticleFilter
	listFields        []string
	suggestSize       int
	suggestPrefix     string

	createCalls, updateCalls, deleteCalls, moveCalls, subtreeCalls int
	lockedLocates, plainLocates                                    int

	// callOrder records the flow steps a test wants to assert on.
	callOrder []string
}

func (f *fakeArticleStore) step(name string) { f.callOrder = append(f.callOrder, name) }

func (f *fakeArticleStore) List(_ context.Context, opts options.Searcher, filter model.ArticleFilter) ([]*model.Article, bool, error) {
	f.listFilter = filter
	f.listFields = opts.GetFields()

	return f.list, f.listNext, nil
}

func (f *fakeArticleStore) Locate(_ context.Context, opts options.Searcher) (*model.Article, error) {
	f.plainLocates++
	f.locateFields = append(f.locateFields, opts.GetFields())
	f.step("locate")

	if f.locateErr != nil {
		return nil, f.locateErr
	}

	ids := opts.GetIDs()
	if f.current != nil && len(ids) == 1 && ids[0] == f.current.ID {
		if stale := f.stale; stale != nil {
			f.stale = nil

			return stale, nil
		}

		return f.current, nil
	}

	if f.located == nil {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.located, nil
}

func (f *fakeArticleStore) LocateForUpdate(_ context.Context, opts options.Searcher) (*model.Article, error) {
	f.lockedLocates++
	f.locateFields = append(f.locateFields, opts.GetFields())
	f.step("locate-for-update")

	ids := opts.GetIDs()
	if f.current != nil && (len(ids) != 1 || ids[0] == f.current.ID) {
		return f.current, nil
	}

	if f.located == nil {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.located, nil
}

func (f *fakeArticleStore) Create(_ context.Context, opts options.Creator, in *model.Article) (*model.Article, error) {
	f.createCalls++
	f.writeFields = append(f.writeFields, opts.GetFields())
	f.createIn = in
	f.step("create-article")

	return f.written, nil
}

func (f *fakeArticleStore) Update(_ context.Context, opts options.Updator, in *model.Article, expectedVer int32) (*model.Article, error) {
	f.updateCalls++
	f.writeFields = append(f.writeFields, opts.GetFields())
	f.updateIn = in
	f.updateVer = expectedVer
	f.step("update-article")

	return f.written, nil
}

func (f *fakeArticleStore) Delete(_ context.Context, opts options.Deleter, expectedVer int32) (*model.Article, error) {
	f.deleteCalls++
	f.writeFields = append(f.writeFields, opts.GetFields())
	f.step("delete-article")

	return f.written, nil
}

func (f *fakeArticleStore) Move(
	_ context.Context, opts options.Updator, newParentID, spaceID int64, expectedVer int32,
) (*model.Article, error) {
	f.moveCalls++
	f.writeFields = append(f.writeFields, opts.GetFields())
	f.moveTo = newParentID
	f.moveSpace = spaceID
	f.moveVer = expectedVer
	f.step("move")

	return f.written, nil
}

func (f *fakeArticleStore) Ancestors(context.Context, options.Searcher, int64) ([]*model.Article, error) {
	return f.chain, nil
}

func (f *fakeArticleStore) Tree(context.Context, options.Searcher, int64) ([]*model.TreeNode, error) {
	return f.tree, nil
}

func (f *fakeArticleStore) Subtree(context.Context, options.Searcher, int64) ([]model.SubtreeNode, error) {
	f.subtreeCalls++
	f.step("subtree")

	return f.subtree, nil
}

func (f *fakeArticleStore) SuggestTags(_ context.Context, _ options.Searcher, _ int64, prefix string, size int) ([]string, error) {
	f.suggestPrefix = prefix
	f.suggestSize = size

	return f.tags, nil
}

func (f *fakeArticleStore) LockSpaceTree(_ context.Context, spaceID, _ int64) error {
	f.lockedSpaceIDs = append(f.lockedSpaceIDs, spaceID)
	f.step("tree-lock")

	return nil
}

func (f *fakeArticleStore) LockSpaceKey(_ context.Context, spaceID, _ int64) error {
	f.keyLockedSpaceIDs = append(f.keyLockedSpaceIDs, spaceID)
	f.step("space-key-lock")

	return nil
}

// fakeVersionStore records version writes.
type fakeVersionStore struct {
	created *model.ArticleVersion // Create result
	source  *model.ArticleVersion // Locate result
	history []*model.ArticleVersion

	locateErr error

	createIn    *model.ArticleVersion
	createCalls int
	listFields  []string

	order *fakeArticleStore // shared call-order recorder
}

func (f *fakeVersionStore) List(_ context.Context, opts options.Searcher, _ int64) ([]*model.ArticleVersion, bool, error) {
	f.listFields = opts.GetFields()

	return f.history, false, nil
}

func (f *fakeVersionStore) Locate(context.Context, options.Searcher, int64, int32) (*model.ArticleVersion, error) {
	if f.locateErr != nil {
		return nil, f.locateErr
	}

	return f.source, nil
}

func (f *fakeVersionStore) Create(_ context.Context, _ options.Creator, in *model.ArticleVersion) (*model.ArticleVersion, error) {
	f.createCalls++
	f.createIn = in

	if f.order != nil {
		f.order.step("create-version")
	}

	return f.created, nil
}

// fakeOutbox records the reindex events the save flow publishes.
type fakeOutbox struct {
	events []event.ArticleReindex
	err    error
	order  *fakeArticleStore
}

func (f *fakeOutbox) PublishReindex(_ context.Context, events ...event.ArticleReindex) error {
	f.events = append(f.events, events...)

	if f.order != nil {
		f.order.step("publish-reindex")
	}

	return f.err
}

// articleUow hands the fakes to transactional and direct callers.
type articleUow struct {
	articles *fakeArticleStore
	versions *fakeVersionStore
	outbox   *fakeOutbox
	spaces   *fakeSpaceStore

	transactions int
}

func (u *articleUow) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	u.transactions++

	return fn(ctx, u)
}

func (u *articleUow) EmbeddingModelStore() store.EmbeddingModelStore { return nil }
func (u *articleUow) SpaceStore() store.SpaceStore                   { return u.spaces }
func (u *articleUow) ArticleStore() store.ArticleStore               { return u.articles }
func (u *articleUow) ArticleVersionStore() store.ArticleVersionStore { return u.versions }
func (u *articleUow) OutboxStore() store.OutboxStore                 { return u.outbox }
func (u *articleUow) RetrievalStore() store.RetrievalStore           { return nil }
func (u *articleUow) AttachmentStore() store.AttachmentStore         { return nil }

func newArticleFixture() (*ArticleService, *articleUow) {
	uow := newArticleUow()

	return NewArticleService(uow, slog.New(slog.DiscardHandler)), uow
}

func newArticleUow() *articleUow {
	articles := &fakeArticleStore{
		current: &model.Article{
			ID: 7, SpaceID: 3, ParentID: 2, Depth: 2, Ver: 4,
			Subject: "stored", Type: model.ArticleTypeArticle,
			State: model.ArticleStateActive, Tags: []string{"vpn"},
		},
		located: &model.Article{ID: 2, SpaceID: 3, Depth: 2},
		written: &model.Article{ID: 7, Subject: "written"},
		subtree: []model.SubtreeNode{{ID: 7, Depth: 2}, {ID: 8, Depth: 3}},
	}
	uow := &articleUow{
		articles: articles,
		versions: &fakeVersionStore{
			created: &model.ArticleVersion{ID: 40, VersionNumber: 2},
			source: &model.ArticleVersion{
				ID: 30, ArticleID: 7, VersionNumber: 1, Subject: "old subject",
				BodyRichText: []byte(`{"type":"doc"}`), BodyMarkdown: "# old", BodyPlain: "old",
			},
			order: articles,
		},
		outbox: &fakeOutbox{order: articles},
		spaces: &fakeSpaceStore{order: articles},
	}

	return uow
}

const restoreNotes = "restored from version #1"

const validDoc = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`

func TestArticleCreateValidation(t *testing.T) {
	tests := []struct {
		name   string
		in     *model.Article
		body   string
		wantID string
	}{
		{name: "subject required", in: &model.Article{SpaceID: 3}, wantID: "kb.article.subject_required"},
		{name: "blank subject rejected", in: &model.Article{SpaceID: 3, Subject: "   "}, wantID: "kb.article.subject_required"},
		{name: "broken body json", in: &model.Article{SpaceID: 3, Subject: "a"}, body: "{", wantID: "kb.article.body_invalid"},
		{name: "body is not a document", in: &model.Article{SpaceID: 3, Subject: "a"}, body: `{"type":"paragraph"}`, wantID: "kb.article.body_invalid"},
		{
			name: "escaped NUL in body", in: &model.Article{SpaceID: 3, Subject: "a"},
			body: `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a\u0000b"}]}]}`, wantID: "kb.article.body_invalid",
		},
		{
			name: "NUL hiding in a dropped attribute", in: &model.Article{SpaceID: 3, Subject: "a"},
			body:   `{"type":"doc","content":[{"type":"paragraph","attrs":{"class":"a\u0000b"},"content":[{"type":"text","text":"hi"}]}]}`,
			wantID: "kb.article.body_invalid",
		},
		{name: "nested home page", in: &model.Article{SpaceID: 3, Subject: "a", ParentID: 2, Home: true}, wantID: "kb.article.home_nested"},
		{name: "FAQ home page", in: &model.Article{SpaceID: 3, Subject: "a", Type: model.ArticleTypeFAQ, Home: true}, wantID: "kb.article.home_type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()

			_, err := svc.Create(context.Background(), creatorOpts(), tt.in, []byte(tt.body))

			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %s", err, tt.wantID)
			}

			if uow.articles.createCalls != 0 || uow.versions.createCalls != 0 {
				t.Fatal("a rejected create must not reach the stores")
			}
		})
	}
}

// docOfSize is a valid document of exactly size bytes.
func docOfSize(size int) []byte {
	const (
		head = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"`
		tail = `"}]}]}`
	)

	return []byte(head + strings.Repeat("a", size-len(head)-len(tail)) + tail)
}

func TestArticleBodySizeLimit(t *testing.T) {
	tests := []struct {
		name   string
		size   int
		wantID string
	}{
		{name: "at the limit", size: model.MaxBodyBytes},
		{name: "over the limit", size: model.MaxBodyBytes + 1, wantID: "kb.article.body_too_large"},
	}

	for _, tt := range tests {
		saves := map[string]func(*ArticleService, []byte) error{
			"create": func(svc *ArticleService, body []byte) error {
				_, err := svc.Create(context.Background(), creatorOpts(), &model.Article{SpaceID: 3, Subject: "a"}, body)

				return err
			},
			"update": func(svc *ArticleService, body []byte) error {
				_, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Subject: "a"}, body, 4)

				return err
			},
		}

		for op, save := range saves {
			t.Run(tt.name+" "+op, func(t *testing.T) {
				svc, uow := newArticleFixture()

				err := save(svc, docOfSize(tt.size))

				if tt.wantID == "" {
					if err != nil {
						t.Fatalf("error = %v, want the body accepted", err)
					}

					return
				}

				if errors.ID(err) != tt.wantID {
					t.Fatalf("error = %v, want %s", err, tt.wantID)
				}

				if uow.versions.createCalls != 0 {
					t.Fatal("a rejected body must not reach the stores")
				}
			})
		}
	}
}

// A document may legitimately spell out the NUL escape as text; only a real
// escape is refused.
func TestArticleCreateAcceptsALiteralNulEscape(t *testing.T) {
	svc, uow := newArticleFixture()

	const doc = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"JSON writes NUL as \\u0000 here"}]}]}`

	if _, err := svc.Create(context.Background(), creatorOpts(), &model.Article{SpaceID: 3, Subject: "escapes"}, []byte(doc)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	v := uow.versions.createIn
	if v == nil {
		t.Fatal("the version was not written")
	}

	if !strings.Contains(v.BodyPlain, `\u0000`) {
		t.Fatalf("body plain = %q, want the literal escape", v.BodyPlain)
	}
}

func TestArticleCreateWithBodyVersionsInOneTransaction(t *testing.T) {
	svc, uow := newArticleFixture()

	in := &model.Article{SpaceID: 3, Subject: "VPN", Tags: []string{" vpn ", "", "vpn", "howto"}}

	created, err := svc.Create(context.Background(), creatorOpts(), in, []byte(validDoc))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID != 7 {
		t.Fatalf("created = %+v", created)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want one", uow.transactions)
	}

	// Tags are normalized before the write.
	if got := uow.articles.createIn.Tags; len(got) != 2 || got[0] != "vpn" || got[1] != "howto" {
		t.Fatalf("tags = %v, want normalized", got)
	}

	v := uow.versions.createIn
	if v == nil || v.ArticleID != 7 || v.Subject != "VPN" || v.BodyMarkdown == "" || v.BodyPlain != "hi" {
		t.Fatalf("version = %+v", v)
	}

	if string(v.BodyRichText) != validDoc {
		t.Fatalf("body = %s", v.BodyRichText)
	}
}

func TestArticleCreateWithoutBodySkipsVersion(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(), &model.Article{SpaceID: 3, Subject: "a"}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if uow.versions.createCalls != 0 {
		t.Fatal("a bodyless create must not produce a version")
	}
}

func TestArticleCreateDepthPreCheck(t *testing.T) {
	svc, uow := newArticleFixture()
	uow.articles.located = &model.Article{ID: 2, Depth: 5} // parent already at the floor

	_, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a", ParentID: 2}, nil)

	if errors.ID(err) != "kb.article.create_depth" {
		t.Fatalf("error = %v, want the honest depth error", err)
	}

	if uow.articles.createCalls != 0 {
		t.Fatal("the write must not run after the pre-check refused")
	}
}

func TestArticleCreateHome(t *testing.T) {
	tests := []struct {
		name      string
		home      bool
		taken     bool
		wantID    string
		wantAsked [3]int64
		wantOrder string
	}{
		{
			name: "space without a home", home: true, wantAsked: [3]int64{3, 7, 5},
			wantOrder: "space-key-lock,create-article,set-home,create-version,publish-reindex",
		},
		{
			name: "space with a home already", home: true, taken: true, wantID: "kb.article.home_exists",
			wantAsked: [3]int64{3, 7, 5}, wantOrder: "space-key-lock,create-article,set-home",
		},
		{
			name:      "plain article leaves the space alone",
			wantOrder: "space-key-lock,create-article,create-version,publish-reindex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.spaces.homeTaken = tt.taken

			created, err := svc.Create(context.Background(), creatorOpts(),
				&model.Article{SpaceID: 3, Subject: "about", Home: tt.home}, []byte(validDoc))
			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %q", err, tt.wantID)
			}

			if uow.spaces.homeAsked != tt.wantAsked {
				t.Fatalf("home set with %v, want %v", uow.spaces.homeAsked, tt.wantAsked)
			}

			if order := strings.Join(uow.articles.callOrder, ","); order != tt.wantOrder {
				t.Fatalf("flow order = %s, want %s", order, tt.wantOrder)
			}

			if tt.wantID == "" && created.Home != tt.home {
				t.Fatalf("home = %v, want %v", created.Home, tt.home)
			}
		})
	}
}

func TestArticleHomeStaysOutOfTheTree(t *testing.T) {
	tests := []struct {
		name   string
		run    func(svc *ArticleService) error
		home   bool // the article itself is the home page
		parent bool // the target parent is the home page
		wantID string
	}{
		{
			name: "child under the home page", parent: true, wantID: "kb.article.home_parent",
			run: func(svc *ArticleService) error {
				_, err := svc.Create(context.Background(), creatorOpts(),
					&model.Article{SpaceID: 3, Subject: "a", ParentID: 2}, nil)

				return err
			},
		},
		{
			name: "move under the home page", parent: true, wantID: "kb.article.home_parent",
			run: func(svc *ArticleService) error {
				_, err := svc.Move(context.Background(), updaterOpts(), 2, 0, 4)

				return err
			},
		},
		{
			name: "move of the home page", home: true, wantID: "kb.article.home_move",
			run: func(svc *ArticleService) error {
				_, err := svc.Move(context.Background(), updaterOpts(), 0, 0, 4)

				return err
			},
		},
		{
			name: "article turned into the home page", wantID: "kb.article.home_immutable",
			run: func(svc *ArticleService) error {
				_, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Home: true}, nil, 4)

				return err
			},
		},
		{
			name: "home page turned into a FAQ", home: true, wantID: "kb.article.type_immutable",
			run: func(svc *ArticleService) error {
				_, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Type: model.ArticleTypeFAQ}, nil, 4)

				return err
			},
		},
		{
			name: "home page echoed back by a PUT", home: true,
			run: func(svc *ArticleService) error {
				_, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Subject: "about", Home: true}, nil, 4)

				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.articles.current.Home = tt.home
			uow.articles.located = &model.Article{ID: 2, Depth: 1, Home: tt.parent}

			err := tt.run(svc)
			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %q", err, tt.wantID)
			}

			writes := uow.articles.createCalls + uow.articles.updateCalls + uow.articles.moveCalls
			if tt.wantID != "" && writes != 0 {
				t.Fatal("a refused change must not reach the store")
			}
		})
	}
}

func TestArticleDeleteLocksTheSpaceFirst(t *testing.T) {
	tests := []struct {
		name      string
		locateErr error
		stale     *model.Article
		wantOrder string
		wantCode  codes.Code
	}{
		{name: "the space before the subtree", wantOrder: "locate,tree-lock,locate,delete-article", wantCode: codes.OK},
		{
			name: "an unknown article deletes nothing", locateErr: errors.NotFound("entity does not exist"),
			wantOrder: "locate", wantCode: codes.NotFound,
		},
		{
			name: "an article moved out before the lock deletes nothing", stale: &model.Article{ID: 7, SpaceID: 4},
			wantOrder: "locate,tree-lock,locate", wantCode: codes.Aborted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.articles.located = &model.Article{ID: 7, SpaceID: 3}
			uow.articles.locateErr = tt.locateErr
			uow.articles.stale = tt.stale

			_, err := svc.Delete(context.Background(), updaterOpts(), 4)
			if errors.Code(err) != tt.wantCode {
				t.Fatalf("err = %v, want %v", err, tt.wantCode)
			}

			if order := strings.Join(uow.articles.callOrder, ","); order != tt.wantOrder {
				t.Fatalf("flow order = %s, want %s", order, tt.wantOrder)
			}

			if tt.locateErr == nil && tt.stale == nil &&
				(!slices.Equal(uow.articles.lockedSpaceIDs, []int64{3}) || uow.transactions != 1) {
				t.Fatalf("locked %v in %d transactions, want space 3 in one", uow.articles.lockedSpaceIDs, uow.transactions)
			}
		})
	}
}

func TestArticleCreateLocksTheParent(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a", ParentID: 2}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if order := strings.Join(uow.articles.callOrder, ","); order != "space-key-lock,locate-for-update,create-article" {
		t.Fatalf("flow order = %s", order)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want one", uow.transactions)
	}
}

func TestArticleUpdateMergesOverStored(t *testing.T) {
	tests := []struct {
		name string
		in   *model.Article
		want func(t *testing.T, merged *model.Article)
	}{
		{
			name: "empty fields keep stored values",
			in:   &model.Article{},
			want: func(t *testing.T, m *model.Article) {
				t.Helper()

				if m.Subject != "stored" || m.Type != model.ArticleTypeArticle ||
					m.State != model.ArticleStateActive {
					t.Fatalf("merged = %+v, want stored values kept", m)
				}

				// nil tags must not erase the stored ones.
				if len(m.Tags) != 1 || m.Tags[0] != "vpn" {
					t.Fatalf("tags = %v, want the stored ones", m.Tags)
				}
			},
		},
		{
			name: "set fields override",
			in:   &model.Article{Subject: "new", State: model.ArticleStateInactive, Tags: []string{"hr"}},
			want: func(t *testing.T, m *model.Article) {
				t.Helper()

				if m.Subject != "new" || m.State != model.ArticleStateInactive {
					t.Fatalf("merged = %+v", m)
				}

				if len(m.Tags) != 1 || m.Tags[0] != "hr" {
					t.Fatalf("tags = %v", m.Tags)
				}
			},
		},
		{
			name: "explicit empty tag list clears after normalization",
			in:   &model.Article{Tags: []string{}},
			want: func(t *testing.T, m *model.Article) {
				t.Helper()

				if len(m.Tags) != 0 {
					t.Fatalf("tags = %v, want cleared", m.Tags)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()

			if _, err := svc.Update(context.Background(), updaterOpts(), tt.in, nil, 4); err != nil {
				t.Fatalf("Update: %v", err)
			}

			tt.want(t, uow.articles.updateIn)
		})
	}
}

func TestArticleUpdateAppliesMask(t *testing.T) {
	tests := []struct {
		name     string
		in       *model.Article
		mask     []string
		wantID   string
		wantTags []string
	}{
		{name: "masked tags clear", in: &model.Article{}, mask: []string{"tags"}, wantTags: nil},
		{name: "unmasked tags stay", in: &model.Article{Subject: "new"}, mask: []string{"subject"}, wantTags: []string{"vpn"}},
		{name: "masked empty subject rejected", in: &model.Article{Subject: " "}, mask: []string{"subject"}, wantID: "kb.article.subject_required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			opts := updaterOpts()
			opts.mask = tt.mask

			_, err := svc.Update(context.Background(), opts, tt.in, nil, 4)
			if tt.wantID != "" {
				if errors.ID(err) != tt.wantID || uow.articles.updateCalls != 0 {
					t.Fatalf("err = %v, updates = %d; want %s and no write", err, uow.articles.updateCalls, tt.wantID)
				}

				return
			}

			if err != nil {
				t.Fatalf("Update: %v", err)
			}

			if !slices.Equal(uow.articles.updateIn.Tags, tt.wantTags) {
				t.Fatalf("tags = %v, want %v", uow.articles.updateIn.Tags, tt.wantTags)
			}
		})
	}
}

func TestArticleUpdateUsesClientVersion(t *testing.T) {
	// The optimistic lock compares against what the client saw, not what the
	// locked read returned: otherwise the guard would never fail.
	svc, uow := newArticleFixture()
	uow.articles.current.Ver = 9 // stored moved ahead of the client

	if _, err := svc.Update(context.Background(), updaterOpts(), &model.Article{}, nil, 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.articles.updateVer != 4 {
		t.Fatalf("guard version = %d, want the client's 4", uow.articles.updateVer)
	}
}

func TestArticleUpdateWithBodyAppendsVersion(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Subject: "new"}, []byte(validDoc), 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.versions.createCalls != 1 {
		t.Fatal("a body must produce a version")
	}

	// The version carries the post-merge subject, and follows the article
	// write, so a version conflict never leaves an orphan version.
	if uow.versions.createIn.Subject != "new" || uow.versions.createIn.ArticleID != 7 {
		t.Fatalf("version = %+v", uow.versions.createIn)
	}

	// The event is stored last: an article write that fails takes the version
	// and the event down with it.
	order := strings.Join(uow.articles.callOrder, ",")
	if order != "locate-for-update,update-article,create-version,publish-reindex" {
		t.Fatalf("flow order = %s", order)
	}
}

func TestArticleCreateRequestsReindex(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a"}, []byte(validDoc)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(uow.outbox.events) != 1 {
		t.Fatalf("events = %d, want one", len(uow.outbox.events))
	}

	got := uow.outbox.events[0]
	want := event.NewArticleReindex(got.OccurredAt, 7, 40, 3, 5)

	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}

	if got.OccurredAt.IsZero() {
		t.Fatal("the envelope needs a timestamp")
	}

	if uow.articles.createIn.IndexState != 0 {
		t.Fatalf("create index state = %d, want none", uow.articles.createIn.IndexState)
	}
}

func TestArticleSaveWithoutBodyRequestsNothing(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a"}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{Subject: "new"}, nil, 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(uow.outbox.events) != 0 {
		t.Fatalf("events = %d, want none", len(uow.outbox.events))
	}

	// Metadata alone leaves the indexed content valid.
	if uow.articles.updateIn.IndexState != 0 {
		t.Fatalf("update index state = %d, want none", uow.articles.updateIn.IndexState)
	}
}

func TestArticleUpdateWithBodyRequestsReindex(t *testing.T) {
	svc, uow := newArticleFixture()

	updated, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{Subject: "new"}, []byte(validDoc), 4)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.articles.updateIn.IndexState != model.IndexStatePending {
		t.Fatalf("update index state = %d, want pending", uow.articles.updateIn.IndexState)
	}

	if updated == nil {
		t.Fatal("Update returned no article")
	}

	if len(uow.outbox.events) != 1 {
		t.Fatalf("events = %d, want one", len(uow.outbox.events))
	}

	// The space comes from the locked read of the stored article.
	got := uow.outbox.events[0]
	if want := event.NewArticleReindex(got.OccurredAt, 7, 40, 3, 5); got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestRestoreVersionRequestsReindex(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.RestoreVersion(context.Background(), updaterOpts(), 7, 1, restoreNotes); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}

	if uow.articles.updateIn.IndexState != model.IndexStatePending {
		t.Fatalf("update index state = %d, want pending", uow.articles.updateIn.IndexState)
	}

	if len(uow.outbox.events) != 1 {
		t.Fatalf("events = %d, want one", len(uow.outbox.events))
	}

	// The restored version is the new content, not the one it was taken from.
	if got := uow.outbox.events[0]; got.VersionID != 40 || got.ArticleID != 7 || got.SpaceID != 3 {
		t.Fatalf("event = %+v", got)
	}
}

func TestArticleSaveFailsWhenTheEventCannotBeStored(t *testing.T) {
	svc, uow := newArticleFixture()
	uow.outbox.err = stderrors.New("outbox down")

	if _, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{Subject: "new"}, []byte(validDoc), 4); err == nil {
		t.Fatal("a failed event must fail the save")
	}
}

func TestArticleUpdateVersioning(t *testing.T) {
	latest := &model.ArticleVersion{
		ID: 31, VersionNumber: 2, Subject: "stored",
		BodyRichText: []byte(`{"type":"doc"}`), BodyMarkdown: "# latest", BodyPlain: "latest",
	}

	tests := []struct {
		name        string
		in          *model.Article
		mask        []string
		body        []byte
		history     []*model.ArticleVersion
		wantVersion bool
		wantBody    string
	}{
		{
			name: "new subject copies the latest body", in: &model.Article{Subject: "renamed"},
			mask: []string{"subject"}, history: []*model.ArticleVersion{latest},
			wantVersion: true, wantBody: "# latest",
		},
		{
			name: "new subject with a body takes the body", in: &model.Article{Subject: "renamed"},
			body: []byte(validDoc), history: []*model.ArticleVersion{latest},
			wantVersion: true, wantBody: "hi",
		},
		{
			name: "same subject records nothing", in: &model.Article{Subject: "stored"},
			mask: []string{"subject"}, history: []*model.ArticleVersion{latest},
		},
		{
			name: "tags record nothing", in: &model.Article{Tags: []string{"x"}},
			mask: []string{"tags"}, history: []*model.ArticleVersion{latest},
		},
		{
			name: "new subject without versions records nothing", in: &model.Article{Subject: "renamed"},
			mask: []string{"subject"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.versions.history = tt.history
			opts := updaterOpts()
			opts.mask = tt.mask

			if _, err := svc.Update(context.Background(), opts, tt.in, tt.body, 4); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if got := uow.versions.createCalls == 1; got != tt.wantVersion {
				t.Fatalf("version created = %v, want %v", got, tt.wantVersion)
			}

			if got := len(uow.outbox.events) == 1; got != tt.wantVersion {
				t.Fatalf("reindex requested = %v, want %v", got, tt.wantVersion)
			}

			if !tt.wantVersion {
				if uow.articles.updateIn.IndexState != 0 {
					t.Fatalf("index state = %d, want untouched", uow.articles.updateIn.IndexState)
				}

				return
			}

			if got := uow.versions.createIn; got.Subject != "renamed" || !strings.Contains(got.BodyMarkdown, tt.wantBody) {
				t.Fatalf("version = %q / %q, want subject renamed over %q", got.Subject, got.BodyMarkdown, tt.wantBody)
			}

			if uow.articles.updateIn.IndexState != model.IndexStatePending {
				t.Fatalf("index state = %d, want pending", uow.articles.updateIn.IndexState)
			}
		})
	}
}

func TestArticleUpdateRejectsImmutableInput(t *testing.T) {
	tests := []struct {
		name      string
		in        *model.Article
		wantID    string
		wantWrite int
	}{
		{name: "different parent", in: &model.Article{ParentID: 99}, wantID: "kb.article.parent_immutable"},
		{name: "different space", in: &model.Article{SpaceID: 99}, wantID: "kb.article.space_immutable"},
		{name: "article to faq", in: &model.Article{Type: model.ArticleTypeFAQ}, wantID: "kb.article.type_immutable"},
		{name: "same type", in: &model.Article{Type: model.ArticleTypeArticle}, wantWrite: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()

			_, err := svc.Update(context.Background(), updaterOpts(), tt.in, nil, 4)

			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %q", err, tt.wantID)
			}

			if uow.articles.updateCalls != tt.wantWrite {
				t.Fatalf("writes = %d, want %d", uow.articles.updateCalls, tt.wantWrite)
			}
		})
	}
}

func TestArticleUpdateStateTransitions(t *testing.T) {
	tests := []struct {
		name      string
		stored    int32
		in        int32
		wantErrID string
	}{
		{name: "active back to draft", stored: model.ArticleStateActive, in: model.ArticleStateDraft, wantErrID: "kb.article.state_transition"},
		{name: "inactive back to draft", stored: model.ArticleStateInactive, in: model.ArticleStateDraft, wantErrID: "kb.article.state_transition"},
		{name: "draft published", stored: model.ArticleStateDraft, in: model.ArticleStateActive},
		{name: "draft hidden", stored: model.ArticleStateDraft, in: model.ArticleStateInactive},
		{name: "active hidden", stored: model.ArticleStateActive, in: model.ArticleStateInactive},
		{name: "inactive shown", stored: model.ArticleStateInactive, in: model.ArticleStateActive},
		{name: "state left as is", stored: model.ArticleStateActive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.articles.current.State = tt.stored

			_, err := svc.Update(context.Background(), updaterOpts(), &model.Article{State: tt.in}, nil, 4)

			if errors.ID(err) != tt.wantErrID {
				t.Fatalf("error = %v, want %q", err, tt.wantErrID)
			}

			wantWrites := 1
			if tt.wantErrID != "" {
				wantWrites = 0
			}

			if uow.articles.updateCalls != wantWrites {
				t.Fatalf("update calls = %d, want %d", uow.articles.updateCalls, wantWrites)
			}
		})
	}
}

func TestArticleUpdateAcceptsMatchingHierarchyInput(t *testing.T) {
	// A PUT naturally echoes the current values back; that is not a change.
	svc, uow := newArticleFixture()

	if _, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{SpaceID: 3, ParentID: 2}, nil, 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.articles.updateCalls != 1 {
		t.Fatal("the write must run")
	}
}

func TestArticleMoveFlow(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Move(context.Background(), updaterOpts(), 50, 0, 4); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// The space lock comes before every row lock and before any validation
	// read, all inside one transaction: concurrent moves then serialize on a
	// consistent snapshot instead of deadlocking on inverted lock order.
	order := strings.Join(uow.articles.callOrder, ",")
	if order != "locate,tree-lock,locate-for-update,subtree,locate-for-update,move" {
		t.Fatalf("flow order = %s", order)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want one", uow.transactions)
	}

	if len(uow.articles.lockedSpaceIDs) != 1 || uow.articles.lockedSpaceIDs[0] != 3 {
		t.Fatalf("locked spaces = %v, want the article's", uow.articles.lockedSpaceIDs)
	}

	if uow.articles.moveTo != 50 || uow.articles.moveSpace != 3 || uow.articles.moveVer != 4 {
		t.Fatalf("move args = (%d, %d, %d)", uow.articles.moveTo, uow.articles.moveSpace, uow.articles.moveVer)
	}

	if len(uow.spaces.rescopedRoots) != 0 || len(uow.spaces.droppedIn) != 0 || len(uow.outbox.events) != 0 {
		t.Fatalf("index touched: rescoped %v, dropped %v, events %v",
			uow.spaces.rescopedRoots, uow.spaces.droppedIn, uow.outbox.events)
	}
}

func TestArticleMoveHonestErrors(t *testing.T) {
	tests := []struct {
		name        string
		newParentID int64
		targetDepth int32
		wantID      string
	}{
		{name: "own subtree is a cycle", newParentID: 8, wantID: "kb.article.move_cycle"},
		{name: "itself is a cycle", newParentID: 7, wantID: "kb.article.move_cycle"},
		{name: "too deep a target", newParentID: 50, targetDepth: 4, wantID: "kb.article.move_depth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			// subtree of 7: itself at depth 2, a child at depth 3 => height 1
			if tt.targetDepth != 0 {
				uow.articles.located = &model.Article{ID: tt.newParentID, Depth: tt.targetDepth}
			}

			_, err := svc.Move(context.Background(), updaterOpts(), tt.newParentID, 0, 4)

			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %s", err, tt.wantID)
			}

			if uow.articles.moveCalls != 0 {
				t.Fatal("the move must not run after the pre-check refused")
			}
		})
	}
}

func TestArticleMoveToTopSkipsTargetChecks(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Move(context.Background(), updaterOpts(), 0, 0, 4); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// Only the read that finds the space to lock; no target to validate.
	if uow.articles.plainLocates != 1 {
		t.Fatalf("plain locates = %d, want only the space read", uow.articles.plainLocates)
	}

	if uow.articles.moveCalls != 1 {
		t.Fatal("the move must run")
	}
}

func TestArticleMoveIntoAnotherSpace(t *testing.T) {
	source := &model.Space{ID: 3, Language: "uk", EmbeddingModelID: 8, VectorSearchEnabled: true}

	tests := []struct {
		name        string
		target      *model.Space
		wantLocks   []int64
		wantReindex bool
		wantKeep    int64
	}{
		{
			name:      "the same model keeps the vectors",
			target:    &model.Space{ID: 9, Language: "uk-UA", EmbeddingModelID: 8, VectorSearchEnabled: true},
			wantLocks: []int64{3, 9}, wantKeep: 8,
		},
		{
			name: "an inactive target takes the branch",
			target: &model.Space{
				ID: 9, Language: "uk", EmbeddingModelID: 8, VectorSearchEnabled: true, State: model.SpaceStateInactive,
			},
			wantLocks: []int64{3, 9}, wantKeep: 8,
		},
		{
			name:      "another model queues the branch",
			target:    &model.Space{ID: 9, Language: "uk", EmbeddingModelID: 10, VectorSearchEnabled: true},
			wantLocks: []int64{3, 9}, wantReindex: true, wantKeep: 10,
		},
		{
			name:      "a target without vectors queues the branch",
			target:    &model.Space{ID: 1, Language: "uk"},
			wantLocks: []int64{1, 3}, wantReindex: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.spaces.byID = map[int64]*model.Space{3: source, tt.target.ID: tt.target}
			uow.spaces.pending = []model.ReindexTarget{{ArticleID: 7, VersionID: 40}, {ArticleID: 8, VersionID: 41}}

			if _, err := svc.Move(context.Background(), updaterOpts(), 0, tt.target.ID, 4); err != nil {
				t.Fatalf("Move: %v", err)
			}

			if !slices.Equal(uow.articles.lockedSpaceIDs, tt.wantLocks) {
				t.Fatalf("locked spaces = %v, want %v", uow.articles.lockedSpaceIDs, tt.wantLocks)
			}

			if uow.articles.moveSpace != tt.target.ID {
				t.Fatalf("moved into %d, want %d", uow.articles.moveSpace, tt.target.ID)
			}

			if !slices.Equal(uow.spaces.rescopedRoots, []int64{7}) {
				t.Fatalf("rescoped from %v, want the moved root", uow.spaces.rescopedRoots)
			}

			branch := []int64{7, 8}
			if !slices.Equal(uow.spaces.keptModels, []int64{tt.wantKeep}) ||
				!slices.EqualFunc(uow.spaces.droppedArticles, [][]int64{branch}, slices.Equal) {
				t.Fatalf("dropped %v keeping %v, want the branch keeping %d",
					uow.spaces.droppedArticles, uow.spaces.keptModels, tt.wantKeep)
			}

			if !tt.wantReindex {
				if len(uow.spaces.pendingScope) != 0 || len(uow.outbox.events) != 0 {
					t.Fatalf("queued %v, events %v, want none", uow.spaces.pendingScope, uow.outbox.events)
				}

				return
			}

			if !slices.EqualFunc(uow.spaces.pendingArticles, [][]int64{branch}, slices.Equal) ||
				uow.spaces.pendingScope[0][0] != tt.target.ID {
				t.Fatalf("pending %v in %v, want the branch in the target", uow.spaces.pendingArticles, uow.spaces.pendingScope)
			}

			if len(uow.outbox.events) != 2 || uow.outbox.events[0].SpaceID != tt.target.ID || uow.outbox.events[1].VersionID != 41 {
				t.Fatalf("events = %+v, want the latest versions in the target", uow.outbox.events)
			}
		})
	}
}

func TestArticleMoveIntoAnotherSpaceRefusals(t *testing.T) {
	tests := []struct {
		name        string
		target      *model.Space
		newParentID int64
		stale       *model.Article
		wantID      string
		wantCode    codes.Code
	}{
		{
			name:   "another language",
			target: &model.Space{ID: 9, Language: "en"}, wantID: "kb.article.move_language", wantCode: codes.InvalidArgument,
		},
		{
			name:   "a parent of the source space",
			target: &model.Space{ID: 9, Language: "uk"}, newParentID: 50,
			wantID: "kb.article.move_parent_space", wantCode: codes.InvalidArgument,
		},
		{
			name:     "a space of another domain",
			wantCode: codes.NotFound,
		},
		{
			name:   "an article moved out meanwhile",
			target: &model.Space{ID: 9, Language: "uk"}, stale: &model.Article{ID: 7, SpaceID: 4},
			wantID: "kb.article.space_changed", wantCode: codes.Aborted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.spaces.byID = map[int64]*model.Space{3: {ID: 3, Language: "uk"}}

			if tt.target != nil {
				uow.spaces.byID[tt.target.ID] = tt.target
			}

			uow.articles.stale = tt.stale

			_, err := svc.Move(context.Background(), updaterOpts(), tt.newParentID, 9, 4)
			if errors.Code(err) != tt.wantCode || (tt.wantID != "" && errors.ID(err) != tt.wantID) {
				t.Fatalf("error = %v, want %v %s", err, tt.wantCode, tt.wantID)
			}

			if uow.articles.moveCalls != 0 || len(uow.spaces.rescopedRoots) != 0 {
				t.Fatal("a refused move must not write")
			}
		})
	}
}

func TestSuggestTagsCapsTheSize(t *testing.T) {
	svc, uow := newArticleFixture()
	uow.articles.tags = []string{"vpn"}

	if _, err := svc.SuggestTags(context.Background(), readOptions{auth: fakeAuther{domainID: 5}}, 3, "v", options.MaxSearchSize+1); err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}

	if uow.articles.suggestSize != options.MaxSearchSize {
		t.Fatalf("size = %d, want capped", uow.articles.suggestSize)
	}
}

func TestRestoreVersionFlow(t *testing.T) {
	svc, uow := newArticleFixture()

	restored, err := svc.RestoreVersion(context.Background(), updaterOpts(), 7, 1, restoreNotes)
	if err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}

	if restored.ID != 40 {
		t.Fatalf("restored = %+v", restored)
	}

	v := uow.versions.createIn
	if v.RestoredFrom != 30 || v.Subject != "old subject" || string(v.BodyRichText) != `{"type":"doc"}` {
		t.Fatalf("version = %+v", v)
	}

	if v.Notes != restoreNotes {
		t.Fatalf("notes = %q, want what the caller sent", v.Notes)
	}

	// The article subject follows the restored version, guarded by the stored
	// version: the contract carries no client etag here.
	if uow.articles.updateIn.Subject != "old subject" || uow.articles.updateVer != 4 {
		t.Fatalf("article update = (%q, %d)", uow.articles.updateIn.Subject, uow.articles.updateVer)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want one", uow.transactions)
	}
}

func TestRestoreVersionMissingSource(t *testing.T) {
	svc, uow := newArticleFixture()
	uow.versions.locateErr = errors.NotFound("entity does not exist or access is denied")

	_, err := svc.RestoreVersion(context.Background(), updaterOpts(), 7, 99, "")

	if errors.Code(err) != codes.NotFound {
		t.Fatalf("error = %v, want NotFound", err)
	}

	if uow.versions.createCalls != 0 || uow.articles.updateCalls != 0 {
		t.Fatal("nothing may be written without a source")
	}
}

func TestArticleVersionDoesNotDependOnTheProjection(t *testing.T) {
	// The read-back returns only what the caller asked for, so the version must
	// take its subject from what the flow wrote.
	svc, uow := newArticleFixture()
	uow.articles.written = &model.Article{ID: 7} // a projection without the subject

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "VPN"}, []byte(validDoc)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := uow.versions.createIn; got.Subject != "VPN" || got.ArticleID != 7 {
		t.Fatalf("version = %+v", got)
	}

	svc, uow = newArticleFixture()
	uow.articles.written = &model.Article{ID: 7}

	if _, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{Subject: "VPN v2"}, []byte(validDoc), 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := uow.versions.createIn; got.Subject != "VPN v2" || got.ArticleID != 7 {
		t.Fatalf("version = %+v", got)
	}
}

func TestArticleLockedReadCarriesTheMergedColumns(t *testing.T) {
	// The store rewrites every column, so a column missing from the locked read
	// would be written back empty.
	svc, uow := newArticleFixture()

	if _, err := svc.Update(context.Background(), updaterOpts(), &model.Article{}, nil, 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.articles.lockedLocates != 1 {
		t.Fatalf("locked reads = %d", uow.articles.lockedLocates)
	}

	for _, want := range []string{"subject", "tags", "type", "state", "parent_id", "space", "ver", "home"} {
		if !slices.Contains(uow.articles.locateFields[0], want) {
			t.Fatalf("locked read fields = %v, want %q", uow.articles.locateFields[0], want)
		}
	}
}

func TestArticleCreateTrimsTheSubject(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "  VPN  "}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if uow.articles.createIn.Subject != "VPN" {
		t.Fatalf("subject = %q, want it trimmed like the merge path", uow.articles.createIn.Subject)
	}
}

func TestArticleBodyLogsUnknownNodes(t *testing.T) {
	const docWithTable = `{"type":"doc","content":[{"type":"table","content":` +
		`[{"type":"text","text":"hi"}]}]}`

	var logged bytes.Buffer

	uow := newArticleUow()
	svc := NewArticleService(uow, slog.New(slog.NewTextHandler(&logged, nil)))

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a"}, []byte(docWithTable)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if out := logged.String(); !strings.Contains(out, "unknown") || !strings.Contains(out, "table") {
		t.Fatalf("log = %q, want the unknown node types", out)
	}

	logged.Reset()

	if _, err := svc.Update(context.Background(), updaterOpts(),
		&model.Article{}, []byte(docWithTable), 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if out := logged.String(); !strings.Contains(out, "table") {
		t.Fatalf("update log = %q, want the unknown node types", out)
	}
}

func TestArticleBodyWithKnownNodesLogsNothing(t *testing.T) {
	var logged bytes.Buffer

	svc := NewArticleService(newArticleUow(), slog.New(slog.NewTextHandler(&logged, nil)))

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a"}, []byte(validDoc)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if logged.Len() != 0 {
		t.Fatalf("log = %q, want silence", logged.String())
	}
}

func TestArticleReindex(t *testing.T) {
	tests := []struct {
		name       string
		indexState int32
		history    []*model.ArticleVersion
		wantID     string
	}{
		{
			name:       "failed article queues its latest version",
			indexState: model.IndexStateFailed,
			history:    []*model.ArticleVersion{{ID: 41, VersionNumber: 3}, {ID: 40, VersionNumber: 2}},
		},
		{
			name:       "indexed article is refused",
			indexState: model.IndexStateIndexed,
			history:    []*model.ArticleVersion{{ID: 41, VersionNumber: 3}},
			wantID:     "kb.article.reindex_not_failed",
		},
		{
			name:       "pending article is refused",
			indexState: model.IndexStatePending,
			history:    []*model.ArticleVersion{{ID: 41, VersionNumber: 3}},
			wantID:     "kb.article.reindex_not_failed",
		},
		{
			name:       "article without a version is refused",
			indexState: model.IndexStateFailed,
			wantID:     "kb.article.no_version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			uow.articles.current.IndexState = tt.indexState
			uow.versions.history = tt.history

			_, err := svc.Reindex(context.Background(), updaterOpts(), 4)

			if tt.wantID != "" {
				if errors.ID(err) != tt.wantID {
					t.Fatalf("error = %v, want %s", err, tt.wantID)
				}

				if uow.articles.updateCalls != 0 || len(uow.outbox.events) != 0 {
					t.Fatal("a refused reindex must write nothing")
				}

				return
			}

			if err != nil {
				t.Fatalf("Reindex: %v", err)
			}

			if !slices.Contains(uow.articles.locateFields[0], "index_state") {
				t.Fatalf("locked read fields = %v, want index_state", uow.articles.locateFields[0])
			}

			in := uow.articles.updateIn
			if in.IndexState != model.IndexStatePending || in.Subject != "stored" || uow.articles.updateVer != 4 {
				t.Fatalf("article update = (%+v, %d)", in, uow.articles.updateVer)
			}

			if len(uow.outbox.events) != 1 {
				t.Fatalf("events = %d, want one", len(uow.outbox.events))
			}

			if got := uow.outbox.events[0]; got.VersionID != 41 || got.ArticleID != 7 || got.SpaceID != 3 {
				t.Fatalf("event = %+v", got)
			}

			if uow.transactions != 1 {
				t.Fatalf("transactions = %d, want one", uow.transactions)
			}
		})
	}
}

func TestArticleListsCarryNoBodies(t *testing.T) {
	tests := []struct {
		name       string
		versions   bool
		fields     []string
		wantFields []string
	}{
		{name: "articles keep the default", fields: nil, wantFields: nil},
		{name: "articles drop the published body", fields: []string{"id", "published"}, wantFields: []string{"id"}},
		{name: "articles asked only for the body get the default", fields: []string{"published"}, wantFields: nil},
		{name: "history defaults to metadata", versions: true, fields: nil, wantFields: versionListFields},
		{name: "history drops a body", versions: true, fields: []string{"id", "body_plain"}, wantFields: []string{"id"}},
		{
			name: "history asked only for bodies gets metadata", versions: true,
			fields: []string{"body_rich_text", "body_markdown", "body_plain"}, wantFields: versionListFields,
		},
		{name: "history keeps other fields", versions: true, fields: []string{"notes"}, wantFields: []string{"notes"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()
			opts := readOptions{fields: tt.fields}

			var got []string

			if tt.versions {
				if _, _, err := svc.ListVersions(context.Background(), opts, 7); err != nil {
					t.Fatalf("ListVersions: %v", err)
				}

				got = uow.versions.listFields
			} else {
				if _, _, err := svc.List(context.Background(), opts, model.ArticleFilter{}); err != nil {
					t.Fatalf("List: %v", err)
				}

				got = uow.articles.listFields
			}

			if !slices.Equal(got, tt.wantFields) {
				t.Fatalf("fields = %v, want %v", got, tt.wantFields)
			}
		})
	}
}
