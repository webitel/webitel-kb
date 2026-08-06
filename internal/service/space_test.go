package service

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// errFakeUnused marks fake methods the tests never reach.
var errFakeUnused = errors.Internal("not implemented in fake")

// fakeAuther is a minimal caller session.
type fakeAuther struct {
	domainID int64
	userID   int64
}

func (a fakeAuther) GetRoles() []int64                            { return nil }
func (a fakeAuther) GetUserID() int64                             { return a.userID }
func (a fakeAuther) GetUserIP() string                            { return "" }
func (a fakeAuther) GetDomainID() int64                           { return a.domainID }
func (a fakeAuther) GetPermissions() []string                     { return nil }
func (a fakeAuther) GetObjectScope(string) auth.ObjectScoper      { return nil }
func (a fakeAuther) GetAllObjectScopes() []auth.ObjectScoper      { return nil }
func (a fakeAuther) CheckLicenseAccess(string) bool               { return true }
func (a fakeAuther) CheckObacAccess(string, auth.AccessMode) bool { return true }
func (a fakeAuther) IsRbacCheckRequired(string, auth.AccessMode) bool {
	return false
}
func (a fakeAuther) HasPermission(string) bool                    { return true }
func (a fakeAuther) HasSuperPermission(auth.SuperPermission) bool { return false }
func (a fakeAuther) GetMainAccessMode() auth.AccessMode           { return auth.NONE }
func (a fakeAuther) GetMainObjClassName() string                  { return "" }

// writeOpts implements options.Creator, Updator and Deleter.
type writeOpts struct {
	auth   auth.Auther
	fields []string
	id     int64
}

func (o *writeOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *writeOpts) GetFields() []string      { return o.fields }
func (o *writeOpts) GetID() int64             { return o.id }

// fakeSpaceStore records calls and plays back preset spaces. The stored
// (pre-write) space and the read-back result are distinct objects, so a test
// can tell which one a flow returned.
type fakeSpaceStore struct {
	current     *model.Space // LocateForUpdate result (the stored space)
	written     *model.Space // Create/Update/Delete result
	readBack    *model.Space // plain Locate result (the post-write read-back)
	hasArticles bool

	replaceErr error

	lockedLocates int
	locateIDs     []int64
	createCalls   int
	updateCalls   int
	deleteCalls   int
	replacedWith  [][]int64

	// replace scope args as received
	replaceSpaceID, replaceDomainID, replaceUserID int64

	updateIn *model.Space
}

func (f *fakeSpaceStore) List(context.Context, options.Searcher) ([]*model.Space, bool, error) {
	return nil, false, nil
}

func (f *fakeSpaceStore) Locate(_ context.Context, opts options.Searcher) (*model.Space, error) {
	f.locateIDs = append(f.locateIDs, opts.GetIDs()...)

	if f.readBack == nil {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.readBack, nil
}

func (f *fakeSpaceStore) LocateForUpdate(_ context.Context, opts options.Searcher) (*model.Space, error) {
	f.lockedLocates++
	f.locateIDs = append(f.locateIDs, opts.GetIDs()...)

	if f.current == nil {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return f.current, nil
}

func (f *fakeSpaceStore) Create(_ context.Context, _ options.Creator, _ *model.Space) (*model.Space, error) {
	f.createCalls++

	return f.written, nil
}

func (f *fakeSpaceStore) Update(_ context.Context, _ options.Updator, in *model.Space) (*model.Space, error) {
	f.updateCalls++
	f.updateIn = in

	return f.written, nil
}

func (f *fakeSpaceStore) Delete(context.Context, options.Deleter) (*model.Space, error) {
	f.deleteCalls++

	return f.written, nil
}

func (f *fakeSpaceStore) ReplaceTeams(_ context.Context, spaceID, domainID, userID int64, teamIDs []int64) error {
	f.replacedWith = append(f.replacedWith, teamIDs)
	f.replaceSpaceID, f.replaceDomainID, f.replaceUserID = spaceID, domainID, userID

	return f.replaceErr
}

func (f *fakeSpaceStore) HasArticles(context.Context, int64, int64) (bool, error) {
	return f.hasArticles, nil
}

// fakeModelStore serves the validated-model gate.
type fakeModelStore struct {
	models map[int64]*model.EmbeddingModel

	locatedIDs []int64
}

func (f *fakeModelStore) Locate(_ context.Context, opts options.Searcher) (*model.EmbeddingModel, error) {
	id := opts.GetIDs()[0]
	f.locatedIDs = append(f.locatedIDs, id)

	found, ok := f.models[id]
	if !ok {
		return nil, errors.NotFound("entity does not exist or access is denied")
	}

	return found, nil
}

func (f *fakeModelStore) List(context.Context, options.Searcher, model.EmbeddingModelFilter) ([]*model.EmbeddingModel, bool, error) {
	return nil, false, nil
}

func (f *fakeModelStore) Create(context.Context, options.Creator, *model.EmbeddingModel, []byte) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f *fakeModelStore) Update(context.Context, options.Updator, *model.EmbeddingModel, []byte, bool) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f *fakeModelStore) Delete(context.Context, options.Deleter) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f *fakeModelStore) MarkValidated(context.Context, options.Updator) (*model.EmbeddingModel, error) {
	return nil, errFakeUnused
}

func (f *fakeModelStore) GetConfig(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}

// fakeUow hands the same store fakes to transactional and direct callers and
// counts transactions.
type fakeUow struct {
	spaces *fakeSpaceStore
	models *fakeModelStore

	transactions int
}

func (u *fakeUow) WithinTransaction(ctx context.Context, fn func(context.Context, store.UnitOfWork) error) error {
	u.transactions++

	return fn(ctx, u)
}

func (u *fakeUow) EmbeddingModelStore() store.EmbeddingModelStore { return u.models }
func (u *fakeUow) SpaceStore() store.SpaceStore                   { return u.spaces }

func validatedModel(id int64, modelType string) *model.EmbeddingModel {
	return &model.EmbeddingModel{ID: id, Type: modelType, ValidatedAt: time.Now()}
}

func newSpaceFixture() (*SpaceService, *fakeUow) {
	uow := &fakeUow{
		spaces: &fakeSpaceStore{
			current:  &model.Space{ID: 7, Language: "uk", EmbeddingModelID: 3, RerankerModelID: 0},
			written:  &model.Space{ID: 7},
			readBack: &model.Space{ID: 7, Name: "read-back"},
		},
		models: &fakeModelStore{models: map[int64]*model.EmbeddingModel{
			3: validatedModel(3, model.ModelTypeEmbedding),
			4: validatedModel(4, model.ModelTypeReranker),
		}},
	}

	return NewSpaceService(uow), uow
}

func creatorOpts() *writeOpts {
	return &writeOpts{auth: fakeAuther{domainID: 5, userID: 9}}
}

func updaterOpts() *writeOpts {
	return &writeOpts{auth: fakeAuther{domainID: 5, userID: 9}, id: 7}
}

func TestSpaceCreateValidation(t *testing.T) {
	tests := []struct {
		name     string
		in       *model.Space
		wantCode codes.Code
	}{
		{"name required", &model.Space{Language: "uk"}, codes.InvalidArgument},
		{"language required", &model.Space{Name: "docs"}, codes.InvalidArgument},
		{
			"vector search requires a model",
			&model.Space{Name: "docs", Language: "uk", VectorSearchEnabled: true},
			codes.InvalidArgument,
		},
		{
			"rerank requires a reranker",
			&model.Space{Name: "docs", Language: "uk", RerankEnabled: true},
			codes.InvalidArgument,
		},
		{
			"unknown model",
			&model.Space{Name: "docs", Language: "uk", EmbeddingModelID: 404},
			codes.NotFound,
		},
		{
			"type mismatch: reranker as embedding",
			&model.Space{Name: "docs", Language: "uk", EmbeddingModelID: 4},
			codes.InvalidArgument,
		},
		{
			"type mismatch: embedding as reranker",
			&model.Space{Name: "docs", Language: "uk", RerankerModelID: 3},
			codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newSpaceFixture()

			_, err := svc.Create(context.Background(), creatorOpts(), tt.in, nil)
			if errors.Code(err) != tt.wantCode {
				t.Fatalf("err = %v, want %v", err, tt.wantCode)
			}

			if uow.spaces.createCalls != 0 {
				t.Fatal("store create must not run on a rejected input")
			}
		})
	}
}

func TestSpaceCreateRejectsUnvalidatedModel(t *testing.T) {
	svc, uow := newSpaceFixture()
	uow.models.models[8] = &model.EmbeddingModel{ID: 8, Type: model.ModelTypeEmbedding} // never validated

	_, err := svc.Create(context.Background(), creatorOpts(), &model.Space{
		Name: "docs", Language: "uk", EmbeddingModelID: 8,
	}, nil)

	if errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestSpaceCreateHappyPath(t *testing.T) {
	svc, uow := newSpaceFixture()

	created, err := svc.Create(context.Background(), creatorOpts(), &model.Space{
		Name: "docs", Language: "uk", EmbeddingModelID: 3, RerankerModelID: 4,
		VectorSearchEnabled: true, RerankEnabled: true,
	}, []int64{2, 1, 2})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created == nil || created.ID != 7 {
		t.Fatalf("created = %+v", created)
	}

	// ReplaceTeams runs with the caller's scope.
	if uow.spaces.replaceSpaceID != 7 || uow.spaces.replaceDomainID != 5 || uow.spaces.replaceUserID != 9 {
		t.Fatalf("replace scope = %d/%d/%d, want 7/5/9",
			uow.spaces.replaceSpaceID, uow.spaces.replaceDomainID, uow.spaces.replaceUserID)
	}

	if uow.transactions != 1 {
		t.Fatalf("transactions = %d, want everything in one", uow.transactions)
	}

	if uow.spaces.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", uow.spaces.createCalls)
	}

	// The read-back must target the row the store just wrote, and the flow
	// must return the read-back object, not the write result.
	if len(uow.spaces.locateIDs) != 1 || uow.spaces.locateIDs[0] != uow.spaces.written.ID {
		t.Fatalf("read-back ids = %v, want [%d]", uow.spaces.locateIDs, uow.spaces.written.ID)
	}

	if created.Name != "read-back" {
		t.Fatalf("returned %+v, want the read-back object", created)
	}

	// Both models gated; the team set deduplicated.
	if len(uow.models.locatedIDs) != 2 {
		t.Fatalf("model gates = %v, want both models checked", uow.models.locatedIDs)
	}

	if len(uow.spaces.replacedWith) != 1 || len(uow.spaces.replacedWith[0]) != 2 {
		t.Fatalf("teams replaced with %v, want deduplicated [1 2]", uow.spaces.replacedWith)
	}
}

func TestSpaceUpdateImmutability(t *testing.T) {
	tests := []struct {
		name string
		in   *model.Space
	}{
		{"language change rejected", &model.Space{Name: "docs", Language: "en", EmbeddingModelID: 3}},
		{"embedding model change rejected", &model.Space{Name: "docs", Language: "uk", EmbeddingModelID: 8}},
		{"embedding model clear rejected", &model.Space{Name: "docs", Language: "uk", EmbeddingModelID: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uow := newSpaceFixture()
			uow.models.models[8] = validatedModel(8, model.ModelTypeEmbedding)

			_, err := svc.Update(context.Background(), updaterOpts(), tt.in, nil)
			if errors.Code(err) != codes.InvalidArgument {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}

			if uow.spaces.updateCalls != 0 {
				t.Fatal("store update must not run on a rejected input")
			}
		})
	}
}

func TestSpaceUpdateAcceptsOmittedLanguage(t *testing.T) {
	// An empty input language on PATCH is not a change: the update proceeds
	// and the store statement never touches the language column anyway.
	svc, uow := newSpaceFixture()

	updated, err := svc.Update(context.Background(), updaterOpts(), &model.Space{
		Name: "docs", EmbeddingModelID: 3,
	}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if uow.spaces.updateCalls != 1 || updated.Name != "read-back" {
		t.Fatalf("update ran %d times, returned %+v; want 1 and the read-back", uow.spaces.updateCalls, updated)
	}

	// The current-read must be the locking variant: immutability decisions
	// hold only against a row no concurrent writer can move.
	if uow.spaces.lockedLocates != 1 {
		t.Fatalf("locked locates = %d, want 1", uow.spaces.lockedLocates)
	}
}

func TestSpaceCreateAbortsWhenTeamsFail(t *testing.T) {
	// A failed team write must abort the flow before the read-back: the
	// transaction rolls back as a whole.
	svc, uow := newSpaceFixture()
	uow.spaces.replaceErr = errors.Aborted("referenced entity does not exist")

	_, err := svc.Create(context.Background(), creatorOpts(), &model.Space{
		Name: "docs", Language: "uk",
	}, []int64{404})

	if errors.Code(err) != codes.Aborted {
		t.Fatalf("err = %v, want the team failure surfaced", err)
	}

	if len(uow.spaces.locateIDs) != 0 {
		t.Fatal("read-back must not run after a failed team write")
	}
}

func TestSpaceUpdateVectorWithoutModelReportsImmutability(t *testing.T) {
	// Enabling vector while omitting the model on a space that has one is a
	// model-clear attempt: the immutability error is the true cause, not a
	// missing-model complaint.
	svc, _ := newSpaceFixture()

	_, err := svc.Update(context.Background(), updaterOpts(), &model.Space{
		Name: "docs", VectorSearchEnabled: true,
	}, nil)

	if errors.Code(err) != codes.InvalidArgument || errors.ID(err) != "kb.space.embedding_model_immutable" {
		t.Fatalf("err = %v (id %s), want the immutability cause", err, errors.ID(err))
	}
}

func TestSpaceUpdateAllowsUpgradeFromNoModel(t *testing.T) {
	// A lexical-only space may configure a validated model once.
	svc, uow := newSpaceFixture()
	uow.spaces.current = &model.Space{ID: 7, Language: "uk", EmbeddingModelID: 0}

	_, err := svc.Update(context.Background(), updaterOpts(), &model.Space{
		Name: "docs", Language: "uk", EmbeddingModelID: 3, VectorSearchEnabled: true,
	}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(uow.models.locatedIDs) != 1 || uow.models.locatedIDs[0] != 3 {
		t.Fatalf("model gates = %v, want the new model checked", uow.models.locatedIDs)
	}
}

func TestSpaceUpdateSkipsGateForUnchangedModels(t *testing.T) {
	// A model that was validated once and later invalidated must not block
	// unrelated updates of the space that keeps using it.
	svc, uow := newSpaceFixture()

	_, err := svc.Update(context.Background(), updaterOpts(), &model.Space{
		Name: "renamed", Language: "uk", EmbeddingModelID: 3,
	}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(uow.models.locatedIDs) != 0 {
		t.Fatalf("model gates = %v, want none for unchanged references", uow.models.locatedIDs)
	}
}

func TestSpaceDeleteGate(t *testing.T) {
	t.Run("referenced space aborts the delete", func(t *testing.T) {
		svc, uow := newSpaceFixture()
		uow.spaces.hasArticles = true

		_, err := svc.Delete(context.Background(), updaterOpts())
		if errors.Code(err) != codes.Aborted {
			t.Fatalf("err = %v, want Aborted", err)
		}

		if uow.spaces.deleteCalls != 0 {
			t.Fatal("store delete must not run while articles reference the space")
		}
	})

	t.Run("empty space deletes in a transaction", func(t *testing.T) {
		svc, uow := newSpaceFixture()

		deleted, err := svc.Delete(context.Background(), updaterOpts())
		if err != nil || deleted.ID != 7 {
			t.Fatalf("deleted = %+v, err %v", deleted, err)
		}

		if uow.transactions != 1 || uow.spaces.deleteCalls != 1 {
			t.Fatalf("tx/delete = %d/%d, want 1/1", uow.transactions, uow.spaces.deleteCalls)
		}
	})
}
