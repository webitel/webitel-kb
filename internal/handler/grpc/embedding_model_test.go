package grpc

import (
	"context"
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

// modelSession is a minimal caller session for full-path handler tests.
type modelSession struct{}

func (modelSession) GetRoles() []int64                                { return nil }
func (modelSession) GetUserID() int64                                 { return 9 }
func (modelSession) GetUserIP() string                                { return "" }
func (modelSession) GetDomainID() int64                               { return 1 }
func (modelSession) GetPermissions() []string                         { return nil }
func (modelSession) GetObjectScope(string) auth.ObjectScoper          { return nil }
func (modelSession) GetAllObjectScopes() []auth.ObjectScoper          { return nil }
func (modelSession) CheckLicenseAccess(string) bool                   { return true }
func (modelSession) CheckObacAccess(string, auth.AccessMode) bool     { return true }
func (modelSession) IsRbacCheckRequired(string, auth.AccessMode) bool { return false }
func (modelSession) HasPermission(string) bool                        { return true }
func (modelSession) HasSuperPermission(auth.SuperPermission) bool     { return false }
func (modelSession) GetMainAccessMode() auth.AccessMode               { return auth.NONE }
func (modelSession) GetMainObjClassName() string                      { return "" }

// modelStoreFake records the List call the handler path produces.
type modelStoreFake struct {
	filter model.EmbeddingModelFilter
	search string
	items  []*model.EmbeddingModel
}

func (f *modelStoreFake) List(
	_ context.Context, opts options.Searcher, filter model.EmbeddingModelFilter,
) ([]*model.EmbeddingModel, bool, error) {
	f.filter = filter
	f.search = opts.GetSearch()

	return f.items, false, nil
}

func (f *modelStoreFake) Locate(context.Context, options.Searcher) (*model.EmbeddingModel, error) {
	return nil, errors.Internal("not implemented in fake")
}

func (f *modelStoreFake) Create(
	context.Context, options.Creator, *model.EmbeddingModel, []byte,
) (*model.EmbeddingModel, error) {
	return nil, errors.Internal("not implemented in fake")
}

func (f *modelStoreFake) Update(
	context.Context, options.Updator, *model.EmbeddingModel, []byte, bool,
) (*model.EmbeddingModel, error) {
	return nil, errors.Internal("not implemented in fake")
}

func (f *modelStoreFake) Delete(context.Context, options.Deleter) (*model.EmbeddingModel, error) {
	return nil, errors.Internal("not implemented in fake")
}

func (f *modelStoreFake) MarkValidated(context.Context, options.Updator) (*model.EmbeddingModel, error) {
	return nil, errors.Internal("not implemented in fake")
}

func (f *modelStoreFake) GetConfig(context.Context, int64, int64) ([]byte, error) {
	return nil, errors.Internal("not implemented in fake")
}

// modelUoWFake hands out the fake store.
type modelUoWFake struct {
	models *modelStoreFake
}

func (f *modelUoWFake) WithinTransaction(
	ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error,
) error {
	return fn(ctx, f)
}

func (f *modelUoWFake) EmbeddingModelStore() store.EmbeddingModelStore { return f.models }
func (f *modelUoWFake) SpaceStore() store.SpaceStore                   { return nil }
func (f *modelUoWFake) ArticleStore() store.ArticleStore               { return nil }
func (f *modelUoWFake) ArticleVersionStore() store.ArticleVersionStore { return nil }

func TestModelToProto(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		in   *model.EmbeddingModel
		want func(t *testing.T, got *kb.EmbeddingModel)
	}{
		{
			name: "full model",
			in: &model.EmbeddingModel{
				ID: 5, DomainID: 1, Type: "embedding", Name: "gemini prod",
				Provider: "gemini", ModelRef: "gemini-embedding-001",
				Dimensions: 768, Endpoint: "https://example.test",
				ValidatedAt: now, CreatedAt: now,
				CreatedBy: &model.Lookup{ID: 9, Name: "Admin"},
			},
			want: func(t *testing.T, got *kb.EmbeddingModel) {
				t.Helper()

				if got.GetId() != 5 || got.GetProvider() != "gemini" || got.GetDimensions() != 768 {
					t.Fatalf("model = %+v", got)
				}

				if got.GetValidatedAt() != now.UnixMilli() || got.GetCreatedAt() != now.UnixMilli() {
					t.Fatalf("timestamps = %d / %d, want epoch ms", got.GetValidatedAt(), got.GetCreatedAt())
				}

				if got.GetCreatedBy().GetId() != 9 || got.GetCreatedBy().GetName() != "Admin" {
					t.Fatalf("created_by = %+v", got.GetCreatedBy())
				}
			},
		},
		{
			name: "zero times and missing creator stay zero",
			in:   &model.EmbeddingModel{ID: 5, Type: "reranker", IsSelfHosted: true},
			want: func(t *testing.T, got *kb.EmbeddingModel) {
				t.Helper()

				if got.GetValidatedAt() != 0 || got.GetCreatedAt() != 0 {
					t.Fatalf("timestamps = %d / %d, want zero", got.GetValidatedAt(), got.GetCreatedAt())
				}

				if got.GetCreatedBy() != nil {
					t.Fatalf("created_by = %+v, want nil", got.GetCreatedBy())
				}

				if !got.GetIsSelfHosted() {
					t.Fatal("is_self_hosted lost")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, modelToProto(tt.in))
		})
	}
}

func TestModelFromInput(t *testing.T) {
	in := &kb.InputEmbeddingModel{
		Type: "embedding", Name: "bge local", Provider: "bge-m3",
		IsSelfHosted: true, ModelRef: "BAAI/bge-m3", Dimensions: 1024,
		Endpoint: "http://embedder:8080", ApiKey: "must-not-enter-the-model",
	}

	got := modelFromInput(in)

	want := &model.EmbeddingModel{
		Type: "embedding", Name: "bge local", Provider: "bge-m3",
		IsSelfHosted: true, ModelRef: "BAAI/bge-m3",
		Endpoint: "http://embedder:8080",
	}

	if *got != *want {
		t.Fatalf("model = %+v, want %+v", got, want)
	}
}

func TestListModelsFullPath(t *testing.T) {
	fakeStore := &modelStoreFake{items: []*model.EmbeddingModel{
		{ID: 6, Type: "reranker", Name: "bge reranker"},
	}}
	server := NewEmbeddingModelsServer(
		service.NewEmbeddingModelService(&modelUoWFake{models: fakeStore}, nil, nil))
	ctx := auth.WithSession(context.Background(), modelSession{})

	resp, err := server.ListModels(ctx, &kb.ListModelsRequest{Type: "reranker", Q: "bge"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if fakeStore.filter.Type != "reranker" {
		t.Fatalf("filter = %+v, want the request type", fakeStore.filter)
	}

	if fakeStore.search != "bge" {
		t.Fatalf("search = %q, want the request q", fakeStore.search)
	}

	if len(resp.GetItems()) != 1 || resp.GetItems()[0].GetName() != "bge reranker" {
		t.Fatalf("items = %+v", resp.GetItems())
	}
}

func TestListModelsRequiresSession(t *testing.T) {
	server := NewEmbeddingModelsServer(nil)

	_, err := server.ListModels(context.Background(), &kb.ListModelsRequest{})

	if err == nil || errors.Code(err) != codes.Unauthenticated {
		t.Fatalf("ListModels error = %v, want unauthenticated", err)
	}
}

func TestCreateModelRequiresInput(t *testing.T) {
	server := NewEmbeddingModelsServer(nil)

	_, err := server.CreateModel(context.Background(), &kb.CreateModelRequest{})

	if err == nil || errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateModel error = %v, want invalid argument", err)
	}
}

func TestUpdateModelRequiresInput(t *testing.T) {
	server := NewEmbeddingModelsServer(nil)

	_, err := server.UpdateModel(context.Background(), &kb.UpdateModelRequest{Id: 5})

	if err == nil || errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateModel error = %v, want invalid argument", err)
	}
}
