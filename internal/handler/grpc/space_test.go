package grpc

import (
	"context"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/service"
	"github.com/webitel/webitel-kb/internal/store"
)

func TestSpaceToProto(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		in   *model.Space
		want func(t *testing.T, got *kb.Space)
	}{
		{
			name: "full space",
			in: &model.Space{
				ID: 7, DomainID: 5, Name: "docs", Description: "main", Language: "uk",
				EmbeddingModelID: 3, RerankerModelID: 4, VectorSearchEnabled: true,
				ChunkingStrategy: "recursive_markdown", HomeArticleID: 11,
				Teams:     []model.Lookup{{ID: 2, Name: "Sales"}},
				CreatedAt: now, UpdatedAt: now,
				CreatedBy: &model.Lookup{ID: 9, Name: "Admin"},
			},
			want: func(t *testing.T, got *kb.Space) {
				t.Helper()

				if got.GetId() != 7 || got.GetLanguage() != "uk" || got.GetEmbeddingModelId() != 3 {
					t.Fatalf("space = %+v", got)
				}

				if got.GetCreatedAt() != now.UnixMilli() {
					t.Fatalf("created_at = %d, want epoch ms", got.GetCreatedAt())
				}

				if len(got.GetTeams()) != 1 || got.GetTeams()[0].GetName() != "Sales" {
					t.Fatalf("teams = %+v", got.GetTeams())
				}

				if got.GetCreatedBy().GetId() != 9 || got.GetUpdatedBy() != nil {
					t.Fatalf("lookups = %+v / %+v", got.GetCreatedBy(), got.GetUpdatedBy())
				}
			},
		},
		{
			name: "zero times and empty lookups stay zero",
			in:   &model.Space{ID: 7},
			want: func(t *testing.T, got *kb.Space) {
				t.Helper()

				if got.GetCreatedAt() != 0 || got.GetUpdatedAt() != 0 {
					t.Fatalf("timestamps = %d/%d, want 0", got.GetCreatedAt(), got.GetUpdatedAt())
				}

				if got.GetCreatedBy() != nil || len(got.GetTeams()) != 0 {
					t.Fatalf("unset refs leaked: %+v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, spaceToProto(tt.in))
		})
	}
}

func TestSpaceFromInput(t *testing.T) {
	in := &kb.InputSpace{
		Name: "docs", Description: "main", Language: "uk",
		EmbeddingModelId: 3, RerankerModelId: 4,
		VectorSearchEnabled: true, RerankEnabled: true,
		ChunkingStrategy: "recursive_markdown", HomeArticleId: 11,
		TeamIds: []int64{1, 2},
	}

	got := spaceFromInput(in)

	want := &model.Space{
		Name: "docs", Description: "main", Language: "uk",
		EmbeddingModelID: 3, RerankerModelID: 4,
		VectorSearchEnabled: true, RerankEnabled: true,
		ChunkingStrategy: "recursive_markdown", HomeArticleID: 11,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("input mapped to %+v, want %+v", got, want)
	}
}

// fakeUow is the minimal store.UnitOfWork for handler-level tests: happy-path
// stores that record what the handler passed through the service.
type fakeUow struct {
	replacedTeams [][]int64
	space         *model.Space
	embModel      *model.EmbeddingModel
}

func (u *fakeUow) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	return fn(ctx, u)
}

func (u *fakeUow) EmbeddingModelStore() store.EmbeddingModelStore { return fakeModels{u} }
func (u *fakeUow) SpaceStore() store.SpaceStore                   { return fakeSpaces{u} }
func (u *fakeUow) ArticleStore() store.ArticleStore               { return nil }

type fakeSpaces struct{ u *fakeUow }

func (f fakeSpaces) List(context.Context, options.Searcher) ([]*model.Space, bool, error) {
	return []*model.Space{f.u.space}, true, nil
}

func (f fakeSpaces) Locate(context.Context, options.Searcher) (*model.Space, error) {
	return f.u.space, nil
}

func (f fakeSpaces) LocateForUpdate(context.Context, options.Searcher) (*model.Space, error) {
	return f.u.space, nil
}

func (f fakeSpaces) Create(context.Context, options.Creator, *model.Space) (*model.Space, error) {
	return f.u.space, nil
}

func (f fakeSpaces) Update(context.Context, options.Updator, *model.Space) (*model.Space, error) {
	return f.u.space, nil
}

func (f fakeSpaces) Delete(context.Context, options.Deleter) (*model.Space, error) {
	return f.u.space, nil
}

func (f fakeSpaces) ReplaceTeams(_ context.Context, _, _, _ int64, teamIDs []int64) error {
	f.u.replacedTeams = append(f.u.replacedTeams, teamIDs)

	return nil
}

func (f fakeSpaces) HasArticles(context.Context, int64, int64) (bool, error) { return false, nil }

type fakeModels struct{ u *fakeUow }

func (f fakeModels) List(context.Context, options.Searcher, model.EmbeddingModelFilter) ([]*model.EmbeddingModel, bool, error) {
	return nil, false, nil
}

func (f fakeModels) Locate(context.Context, options.Searcher) (*model.EmbeddingModel, error) {
	return f.u.embModel, nil
}

func (f fakeModels) Create(context.Context, options.Creator, *model.EmbeddingModel, []byte) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f fakeModels) Update(context.Context, options.Updator, *model.EmbeddingModel, []byte, bool) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f fakeModels) Delete(context.Context, options.Deleter) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f fakeModels) MarkValidated(context.Context, options.Updator) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f fakeModels) GetConfig(context.Context, int64, int64) ([]byte, error) { return nil, nil }

// errFakeUnused marks fake methods the tests never reach.
var errFakeUnused = errors.Internal("not implemented in fake")

// fakeSession is the minimal auth.Auther for handler tests.
type fakeSession struct{}

func (fakeSession) GetRoles() []int64                            { return nil }
func (fakeSession) GetUserID() int64                             { return 9 }
func (fakeSession) GetUserIP() string                            { return "" }
func (fakeSession) GetDomainID() int64                           { return 5 }
func (fakeSession) GetPermissions() []string                     { return nil }
func (fakeSession) GetObjectScope(string) auth.ObjectScoper      { return nil }
func (fakeSession) GetAllObjectScopes() []auth.ObjectScoper      { return nil }
func (fakeSession) CheckLicenseAccess(string) bool               { return true }
func (fakeSession) CheckObacAccess(string, auth.AccessMode) bool { return true }
func (fakeSession) IsRbacCheckRequired(string, auth.AccessMode) bool {
	return false
}
func (fakeSession) HasPermission(string) bool                    { return true }
func (fakeSession) HasSuperPermission(auth.SuperPermission) bool { return false }
func (fakeSession) GetMainAccessMode() auth.AccessMode           { return auth.NONE }
func (fakeSession) GetMainObjClassName() string                  { return "" }

func newSpacesFixture() (*SpacesServer, *fakeUow, context.Context) {
	uow := &fakeUow{
		space:    &model.Space{ID: 7, Name: "docs", Language: "uk", EmbeddingModelID: 3},
		embModel: &model.EmbeddingModel{ID: 3, Type: model.ModelTypeEmbedding, ValidatedAt: time.Now()},
	}

	server := NewSpacesServer(service.NewSpaceService(uow))
	ctx := auth.WithSession(context.Background(), fakeSession{})

	return server, uow, ctx
}

func TestSpacesServerInputGuards(t *testing.T) {
	server, _, ctx := newSpacesFixture()

	if _, err := server.CreateSpace(ctx, &kb.CreateSpaceRequest{}); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("create nil input err = %v, want InvalidArgument", err)
	}

	if _, err := server.UpdateSpace(ctx, &kb.UpdateSpaceRequest{Id: 7}); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("update nil input err = %v, want InvalidArgument", err)
	}

	if _, err := server.UpdateSpace(ctx, &kb.UpdateSpaceRequest{Input: &kb.InputSpace{Name: "n", Language: "uk"}}); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("update zero id err = %v, want InvalidArgument", err)
	}

	if _, err := server.DeleteSpace(ctx, &kb.DeleteSpaceRequest{}); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("delete zero id err = %v, want InvalidArgument", err)
	}

	if _, err := server.LocateSpace(ctx, &kb.LocateSpaceRequest{}); errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("locate zero id err = %v, want InvalidArgument", err)
	}

	if _, err := server.ListSpaces(context.Background(), &kb.ListSpacesRequest{}); errors.Code(err) != codes.Unauthenticated {
		t.Fatalf("no session err = %v, want Unauthenticated", err)
	}
}

func TestSpacesServerPassesTeamsThrough(t *testing.T) {
	// spaceFromInput deliberately drops team ids; the handler must hand them
	// to the service separately on both writes.
	server, uow, ctx := newSpacesFixture()

	if _, err := server.CreateSpace(ctx, &kb.CreateSpaceRequest{Input: &kb.InputSpace{
		Name: "docs", Language: "uk", TeamIds: []int64{1, 2},
	}}); err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}

	if _, err := server.UpdateSpace(ctx, &kb.UpdateSpaceRequest{Id: 7, Input: &kb.InputSpace{
		Name: "docs", Language: "uk", EmbeddingModelId: 3, TeamIds: []int64{3},
	}}); err != nil {
		t.Fatalf("UpdateSpace: %v", err)
	}

	if len(uow.replacedTeams) != 2 ||
		len(uow.replacedTeams[0]) != 2 || len(uow.replacedTeams[1]) != 1 {
		t.Fatalf("teams passed = %v, want [[1 2] [3]]", uow.replacedTeams)
	}
}

func TestSpacesServerListMapsPage(t *testing.T) {
	server, _, ctx := newSpacesFixture()

	page, err := server.ListSpaces(ctx, &kb.ListSpacesRequest{Size: 1, Page: 1})
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}

	if len(page.GetItems()) != 1 || page.GetItems()[0].GetId() != 7 || !page.GetNext() {
		t.Fatalf("page = %+v", page)
	}
}
