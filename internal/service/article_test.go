package service

import (
	"context"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

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

	createIn  *model.Article
	updateIn  *model.Article
	updateVer int32
	moveTo    int64
	moveVer   int32

	lockedSpaceIDs []int64
	locateFields   [][]string
	writeFields    [][]string
	listFilter     model.ArticleFilter
	suggestSize    int
	suggestPrefix  string

	createCalls, updateCalls, deleteCalls, moveCalls, subtreeCalls int
	lockedLocates, plainLocates                                    int

	// callOrder records the flow steps a test wants to assert on.
	callOrder []string
}

func (f *fakeArticleStore) step(name string) { f.callOrder = append(f.callOrder, name) }

func (f *fakeArticleStore) List(_ context.Context, _ options.Searcher, filter model.ArticleFilter) ([]*model.Article, bool, error) {
	f.listFilter = filter

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

	if f.current == nil {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.current, nil
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

	return f.written, nil
}

func (f *fakeArticleStore) Move(_ context.Context, opts options.Updator, newParentID int64, expectedVer int32) (*model.Article, error) {
	f.moveCalls++
	f.writeFields = append(f.writeFields, opts.GetFields())
	f.moveTo = newParentID
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

func (f *fakeArticleStore) AcquireSpaceMoveLock(_ context.Context, spaceID int64) error {
	f.lockedSpaceIDs = append(f.lockedSpaceIDs, spaceID)
	f.step("space-lock")

	return nil
}

// fakeVersionStore records version writes.
type fakeVersionStore struct {
	created *model.ArticleVersion // Create result
	source  *model.ArticleVersion // Locate result
	history []*model.ArticleVersion

	locateErr error

	createIn     *model.ArticleVersion
	createConfig string
	createCalls  int

	order *fakeArticleStore // shared call-order recorder
}

func (f *fakeVersionStore) List(context.Context, options.Searcher, int64) ([]*model.ArticleVersion, bool, error) {
	return f.history, false, nil
}

func (f *fakeVersionStore) Locate(context.Context, options.Searcher, int64, int32) (*model.ArticleVersion, error) {
	if f.locateErr != nil {
		return nil, f.locateErr
	}

	return f.source, nil
}

func (f *fakeVersionStore) Create(_ context.Context, _ options.Creator, in *model.ArticleVersion, config string) (*model.ArticleVersion, error) {
	f.createCalls++
	f.createIn = in
	f.createConfig = config

	if f.order != nil {
		f.order.step("create-version")
	}

	return f.created, nil
}

// articleUow hands both fakes to transactional and direct callers.
type articleUow struct {
	articles *fakeArticleStore
	versions *fakeVersionStore

	transactions int
}

func (u *articleUow) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	u.transactions++

	return fn(ctx, u)
}

func (u *articleUow) EmbeddingModelStore() store.EmbeddingModelStore { return nil }
func (u *articleUow) SpaceStore() store.SpaceStore                   { return nil }
func (u *articleUow) ArticleStore() store.ArticleStore               { return u.articles }
func (u *articleUow) ArticleVersionStore() store.ArticleVersionStore { return u.versions }

func newArticleFixture() (*ArticleService, *articleUow) {
	articles := &fakeArticleStore{
		current: &model.Article{
			ID: 7, SpaceID: 3, ParentID: 2, Depth: 2, Ver: 4,
			Subject: "stored", Type: model.ArticleTypeArticle,
			State: model.ArticleStateActive, Tags: []string{"vpn"},
		},
		located: &model.Article{ID: 2, Depth: 2},
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
	}

	return NewArticleService(uow), uow
}

const validDoc = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`

func TestArticleCreateValidation(t *testing.T) {
	tests := []struct {
		name   string
		in     *model.Article
		body   string
		wantID string
	}{
		{name: "space required", in: &model.Article{Subject: "a"}, wantID: "kb.article.space_required"},
		{name: "subject required", in: &model.Article{SpaceID: 3}, wantID: "kb.article.subject_required"},
		{name: "blank subject rejected", in: &model.Article{SpaceID: 3, Subject: "   "}, wantID: "kb.article.subject_required"},
		{name: "unknown type code", in: &model.Article{SpaceID: 3, Subject: "a", Type: 9}, wantID: "kb.article.code_invalid"},
		{name: "unknown state code", in: &model.Article{SpaceID: 3, Subject: "a", State: 9}, wantID: "kb.article.code_invalid"},
		{name: "negative type code", in: &model.Article{SpaceID: 3, Subject: "a", Type: -1}, wantID: "kb.article.code_invalid"},
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

	order := strings.Join(uow.articles.callOrder, ",")
	if order != "locate-for-update,update-article,create-version" {
		t.Fatalf("flow order = %s", order)
	}
}

func TestArticleUpdateWithoutBodySkipsVersion(t *testing.T) {
	svc, uow := newArticleFixture()

	if _, err := svc.Update(context.Background(), updaterOpts(), &model.Article{Subject: "new"}, nil, 4); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.versions.createCalls != 0 {
		t.Fatal("a metadata-only update must not produce a version")
	}
}

func TestArticleUpdateRejectsHierarchyInput(t *testing.T) {
	tests := []struct {
		name   string
		in     *model.Article
		wantID string
	}{
		{name: "different parent", in: &model.Article{ParentID: 99}, wantID: "kb.article.parent_immutable"},
		{name: "different space", in: &model.Article{SpaceID: 99}, wantID: "kb.article.space_immutable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()

			_, err := svc.Update(context.Background(), updaterOpts(), tt.in, nil, 4)

			if errors.ID(err) != tt.wantID {
				t.Fatalf("error = %v, want %s", err, tt.wantID)
			}

			if uow.articles.updateCalls != 0 {
				t.Fatal("the write must not run")
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

	if _, err := svc.Move(context.Background(), updaterOpts(), 50, 4); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// The space lock comes before every row lock and before any validation
	// read, all inside one transaction: concurrent moves then serialize on a
	// consistent snapshot instead of deadlocking on inverted lock order.
	order := strings.Join(uow.articles.callOrder, ",")
	if order != "locate,space-lock,locate-for-update,subtree,locate,move" {
		t.Fatalf("flow order = %s", order)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want one", uow.transactions)
	}

	if len(uow.articles.lockedSpaceIDs) != 1 || uow.articles.lockedSpaceIDs[0] != 3 {
		t.Fatalf("locked spaces = %v, want the article's", uow.articles.lockedSpaceIDs)
	}

	if uow.articles.moveTo != 50 || uow.articles.moveVer != 4 {
		t.Fatalf("move args = (%d, %d)", uow.articles.moveTo, uow.articles.moveVer)
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

			_, err := svc.Move(context.Background(), updaterOpts(), tt.newParentID, 4)

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

	if _, err := svc.Move(context.Background(), updaterOpts(), 0, 4); err != nil {
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

func TestSuggestTagsGuards(t *testing.T) {
	svc, uow := newArticleFixture()
	uow.articles.tags = []string{"vpn"}

	if _, err := svc.SuggestTags(context.Background(), readOptions{auth: fakeAuther{domainID: 5}}, 0, "v", 10); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("error = %v, want InvalidArgument on a missing space", err)
	}

	if _, err := svc.SuggestTags(context.Background(), readOptions{auth: fakeAuther{domainID: 5}}, 3, "v", options.MaxSearchSize+1); err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}

	if uow.articles.suggestSize != options.MaxSearchSize {
		t.Fatalf("size = %d, want capped", uow.articles.suggestSize)
	}
}

func TestRestoreVersionFlow(t *testing.T) {
	svc, uow := newArticleFixture()

	restored, err := svc.RestoreVersion(context.Background(), updaterOpts(), 7, 1,
		"  Restored from version #1 with a very long tail that exceeds the fifty character limit  ")
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

	if got := len([]rune(v.Notes)); got > 50 {
		t.Fatalf("notes length = %d, want at most 50", got)
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

func TestArticleWritesCarryTheFieldsTheFlowNeeds(t *testing.T) {
	narrowCreate := &writeOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"subject"}}
	narrowWrite := &writeOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"subject"}, id: 7}

	tests := []struct {
		name string
		call func(svc *ArticleService) error
	}{
		{name: "create", call: func(svc *ArticleService) error {
			_, err := svc.Create(context.Background(), narrowCreate,
				&model.Article{SpaceID: 3, Subject: "a"}, nil)

			return err
		}},
		{name: "update", call: func(svc *ArticleService) error {
			_, err := svc.Update(context.Background(), narrowWrite, &model.Article{}, nil, 4)

			return err
		}},
		{name: "move", call: func(svc *ArticleService) error {
			_, err := svc.Move(context.Background(), narrowWrite, 0, 4)

			return err
		}},
		{name: "delete", call: func(svc *ArticleService) error {
			_, err := svc.Delete(context.Background(), narrowWrite, 4)

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newArticleFixture()

			if err := tt.call(svc); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}

			if len(uow.articles.writeFields) != 1 {
				t.Fatalf("write calls = %d", len(uow.articles.writeFields))
			}

			got := uow.articles.writeFields[0]
			for _, want := range []string{"subject", "id", "ver"} {
				if !slices.Contains(got, want) {
					t.Fatalf("fields = %v, want %q kept", got, want)
				}
			}
		})
	}
}

func TestArticleEmptySelectionLeavesTheDefaults(t *testing.T) {
	// Asking for nothing means the store's own default projection; forcing a
	// short list here would strip the response down to it.
	svc, uow := newArticleFixture()

	if _, err := svc.Create(context.Background(), creatorOpts(),
		&model.Article{SpaceID: 3, Subject: "a"}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := uow.articles.writeFields[0]; len(got) != 0 {
		t.Fatalf("fields = %v, want the store defaults", got)
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

	for _, want := range []string{"subject", "tags", "type", "state", "parent_id", "space", "ver"} {
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
