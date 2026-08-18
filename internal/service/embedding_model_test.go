package service

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// stubAuther is a minimal caller session.
type stubAuther struct {
	domainID int64
	userID   int64
}

func (a stubAuther) GetRoles() []int64                                { return nil }
func (a stubAuther) GetUserID() int64                                 { return a.userID }
func (a stubAuther) GetUserIP() string                                { return "" }
func (a stubAuther) GetDomainID() int64                               { return a.domainID }
func (a stubAuther) GetPermissions() []string                         { return nil }
func (a stubAuther) GetObjectScope(string) auth.ObjectScoper          { return nil }
func (a stubAuther) GetAllObjectScopes() []auth.ObjectScoper          { return nil }
func (a stubAuther) CheckLicenseAccess(string) bool                   { return true }
func (a stubAuther) CheckObacAccess(string, auth.AccessMode) bool     { return true }
func (a stubAuther) IsRbacCheckRequired(string, auth.AccessMode) bool { return false }
func (a stubAuther) HasPermission(string) bool                        { return true }
func (a stubAuther) HasSuperPermission(auth.SuperPermission) bool     { return false }
func (a stubAuther) GetMainAccessMode() auth.AccessMode               { return auth.NONE }
func (a stubAuther) GetMainObjClassName() string                      { return "" }

// stubWriteOpts implements options.Creator, Updator and Deleter.
type stubWriteOpts struct {
	auth   auth.Auther
	fields []string
	id     int64
}

func (o *stubWriteOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *stubWriteOpts) GetFields() []string      { return o.fields }
func (o *stubWriteOpts) GetID() int64             { return o.id }

// fakeModelStore records store calls and plays back preset results.
type fakeModelStore struct {
	located *model.EmbeddingModel // Locate result
	written *model.EmbeddingModel // Create/Update/Delete result
	marked  *model.EmbeddingModel // MarkValidated result
	config  []byte                // GetConfig result

	locateErr error
	configErr error

	locateIDs    []int64
	locateFields []string
	listFilter   model.EmbeddingModelFilter

	createIn     *model.EmbeddingModel
	createConfig []byte

	updateIn         *model.EmbeddingModel
	updateConfig     []byte
	updateKeepConfig bool

	configID, configDomainID int64

	createCalls, updateCalls, deleteCalls, markCalls, configCalls int
}

func (f *fakeModelStore) List(
	_ context.Context, _ options.Searcher, filter model.EmbeddingModelFilter,
) ([]*model.EmbeddingModel, bool, error) {
	f.listFilter = filter

	return nil, false, nil
}

func (f *fakeModelStore) Locate(_ context.Context, opts options.Searcher) (*model.EmbeddingModel, error) {
	f.locateIDs = append(f.locateIDs, opts.GetIDs()...)
	f.locateFields = opts.GetFields()

	if f.locateErr != nil {
		return nil, f.locateErr
	}

	return f.located, nil
}

func (f *fakeModelStore) Create(
	_ context.Context, _ options.Creator, in *model.EmbeddingModel, config []byte,
) (*model.EmbeddingModel, error) {
	f.createCalls++
	f.createIn = in
	f.createConfig = config

	return f.written, nil
}

func (f *fakeModelStore) Update(
	_ context.Context, _ options.Updator, in *model.EmbeddingModel, config []byte, keepConfig bool,
) (*model.EmbeddingModel, error) {
	f.updateCalls++
	f.updateIn = in
	f.updateConfig = config
	f.updateKeepConfig = keepConfig

	return f.written, nil
}

func (f *fakeModelStore) Delete(_ context.Context, _ options.Deleter) (*model.EmbeddingModel, error) {
	f.deleteCalls++

	return f.written, nil
}

func (f *fakeModelStore) MarkValidated(_ context.Context, _ options.Updator) (*model.EmbeddingModel, error) {
	f.markCalls++

	return f.marked, nil
}

func (f *fakeModelStore) GetConfig(_ context.Context, id, domainID int64) ([]byte, error) {
	f.configCalls++
	f.configID, f.configDomainID = id, domainID

	if f.configErr != nil {
		return nil, f.configErr
	}

	return f.config, nil
}

// fakeModelUoW hands out the fake store; transactions run the callback on the
// same unit.
type fakeModelUoW struct {
	models *fakeModelStore
}

func (f *fakeModelUoW) WithinTransaction(
	ctx context.Context, fn func(ctx context.Context, uow store.UnitOfWork) error,
) error {
	return fn(ctx, f)
}

func (f *fakeModelUoW) EmbeddingModelStore() store.EmbeddingModelStore { return f.models }
func (f *fakeModelUoW) SpaceStore() store.SpaceStore                   { return nil }
func (f *fakeModelUoW) ArticleStore() store.ArticleStore               { return nil }
func (f *fakeModelUoW) ArticleVersionStore() store.ArticleVersionStore { return nil }

// fakeSealer marks what passed through encryption, so a test can tell a sealed
// credential from a plaintext leak.
type fakeSealer struct {
	decryptErr error
}

func (f fakeSealer) Encrypt(_ context.Context, plain []byte) ([]byte, error) {
	return append([]byte("enc:"), plain...), nil
}

func (f fakeSealer) Decrypt(_ context.Context, blob []byte) ([]byte, error) {
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}

	if len(blob) == 0 {
		return nil, nil
	}

	return bytes.TrimPrefix(blob, []byte("enc:")), nil
}

// fakeProvider records probe requests and plays back preset results.
type fakeProvider struct {
	embedRes  embedding.EmbedResult
	embedErr  error
	rerankRes embedding.RerankResult
	rerankErr error

	embedReq  *embedding.EmbedRequest
	rerankReq *embedding.RerankRequest
}

func (f *fakeProvider) Embed(_ context.Context, req embedding.EmbedRequest) (embedding.EmbedResult, error) {
	f.embedReq = &req

	return f.embedRes, f.embedErr
}

func (f *fakeProvider) Rerank(_ context.Context, req embedding.RerankRequest) (embedding.RerankResult, error) {
	f.rerankReq = &req

	return f.rerankRes, f.rerankErr
}

// fakeResolver resolves every provider key to one fake, or fails.
type fakeResolver struct {
	provider *fakeProvider
	err      error

	resolved []string
}

func (f *fakeResolver) ForModel(provider string) (embedding.Provider, error) {
	f.resolved = append(f.resolved, provider)

	if f.err != nil {
		return nil, f.err
	}

	return f.provider, nil
}

func newModelService(models *fakeModelStore, sealer fakeSealer, resolver *fakeResolver) *EmbeddingModelService {
	return NewEmbeddingModelService(&fakeModelUoW{models: models}, sealer, resolver)
}

func cloudInput() *model.EmbeddingModel {
	return &model.EmbeddingModel{
		Type:       model.ModelTypeEmbedding,
		Name:       "gemini prod",
		Provider:   embedding.ProviderGemini,
		ModelRef:   "gemini-embedding-001",
		Dimensions: 768,
	}
}

func embeddedInput() *model.EmbeddingModel {
	return &model.EmbeddingModel{
		Type:         model.ModelTypeEmbedding,
		Name:         "bge local",
		Provider:     embedding.ProviderBGEM3,
		IsSelfHosted: true,
		ModelRef:     "BAAI/bge-m3",
		Dimensions:   1024,
		Endpoint:     "http://embedder:8080",
	}
}

func TestValidateInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *model.EmbeddingModel)
		apiKey string
		create bool
		wantID string
	}{
		{name: "valid cloud create", mutate: func(*model.EmbeddingModel) {}, apiKey: "k", create: true},
		{name: "valid cloud update without key", mutate: func(*model.EmbeddingModel) {}, create: false},
		{
			name: "valid embedded without key and endpoint",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
				in.Endpoint = ""
			},
			create: true,
		},
		{
			name:   "unknown type",
			mutate: func(in *model.EmbeddingModel) { in.Type = "generator" },
			apiKey: "k", create: true, wantID: "kb.model.type_invalid",
		},
		{
			name:   "empty type",
			mutate: func(in *model.EmbeddingModel) { in.Type = "" },
			apiKey: "k", create: true, wantID: "kb.model.type_invalid",
		},
		{
			name:   "missing name",
			mutate: func(in *model.EmbeddingModel) { in.Name = "" },
			apiKey: "k", create: true, wantID: "kb.model.name_required",
		},
		{
			name:   "empty provider",
			mutate: func(in *model.EmbeddingModel) { in.Provider = "" },
			apiKey: "k", create: true, wantID: "kb.model.provider_invalid",
		},
		{
			name:   "unknown provider",
			mutate: func(in *model.EmbeddingModel) { in.Provider = "acme" },
			apiKey: "k", create: true, wantID: "kb.model.provider_invalid",
		},
		{
			name:   "cloud provider marked self-hosted",
			mutate: func(in *model.EmbeddingModel) { in.IsSelfHosted = true },
			apiKey: "k", create: true, wantID: "kb.model.self_hosted_mismatch",
		},
		{
			name: "embedded provider marked cloud",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
				in.IsSelfHosted = false
			},
			create: true, wantID: "kb.model.self_hosted_mismatch",
		},
		{
			name:   "missing model_ref",
			mutate: func(in *model.EmbeddingModel) { in.ModelRef = "" },
			apiKey: "k", create: true, wantID: "kb.model.model_ref_required",
		},
		{
			name:   "embedding without dimensions",
			mutate: func(in *model.EmbeddingModel) { in.Dimensions = 0 },
			apiKey: "k", create: true, wantID: "kb.model.dimensions_required",
		},
		{
			name: "reranker with dimensions",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
				in.Type = model.ModelTypeReranker
				in.Provider = embedding.ProviderBGEReranker
			},
			create: true, wantID: "kb.model.dimensions_not_applicable",
		},
		{
			name: "valid embedded reranker",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
				in.Type = model.ModelTypeReranker
				in.Provider = embedding.ProviderBGEReranker
				in.Dimensions = 0
			},
			create: true,
		},
		{
			name:   "relative endpoint",
			mutate: func(in *model.EmbeddingModel) { in.Endpoint = "embedder:8080" },
			apiKey: "k", create: true, wantID: "kb.model.endpoint_invalid",
		},
		{
			name:   "non-http endpoint",
			mutate: func(in *model.EmbeddingModel) { in.Endpoint = "ftp://embedder" },
			apiKey: "k", create: true, wantID: "kb.model.endpoint_invalid",
		},
		{
			name:   "cloud create without key",
			mutate: func(*model.EmbeddingModel) {},
			create: true, wantID: "kb.model.api_key_required",
		},
		{
			name: "embedded with key",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
			},
			apiKey: "k", create: true, wantID: "kb.model.api_key_not_applicable",
		},
		{
			name: "embedded update with key",
			mutate: func(in *model.EmbeddingModel) {
				*in = *embeddedInput()
			},
			apiKey: "k", create: false, wantID: "kb.model.api_key_not_applicable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := cloudInput()
			tt.mutate(in)

			err := validateInput(in, tt.apiKey, tt.create)

			if tt.wantID == "" {
				if err != nil {
					t.Fatalf("validateInput: %v", err)
				}

				return
			}

			if err == nil || errors.ID(err) != tt.wantID {
				t.Fatalf("validateInput error = %v, want id %q", err, tt.wantID)
			}
		})
	}
}

func TestCreateSealsKey(t *testing.T) {
	models := &fakeModelStore{written: &model.EmbeddingModel{ID: 1}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{})
	opts := &stubWriteOpts{auth: stubAuther{domainID: 1}}

	created, err := svc.Create(context.Background(), opts, cloudInput(), "secret")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created == nil || models.createCalls != 1 {
		t.Fatalf("create calls = %d", models.createCalls)
	}

	if string(models.createConfig) != "enc:secret" {
		t.Fatalf("stored config = %q, want the sealed credential", models.createConfig)
	}
}

func TestCreateEmbeddedStoresNoConfig(t *testing.T) {
	models := &fakeModelStore{written: &model.EmbeddingModel{ID: 1}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{})
	opts := &stubWriteOpts{auth: stubAuther{domainID: 1}}

	if _, err := svc.Create(context.Background(), opts, embeddedInput(), ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if models.createConfig != nil {
		t.Fatalf("stored config = %q, want none", models.createConfig)
	}
}

func TestUpdateCredentialFlow(t *testing.T) {
	tests := []struct {
		name       string
		in         *model.EmbeddingModel
		apiKey     string
		wantConfig string
		wantKeep   bool
	}{
		{name: "cloud empty key keeps credential", in: cloudInput(), wantKeep: true},
		{name: "cloud new key replaces credential", in: cloudInput(), apiKey: "next", wantConfig: "enc:next"},
		{name: "embedded always clears credential", in: embeddedInput()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := &fakeModelStore{
				located: &model.EmbeddingModel{ID: 1, Type: tt.in.Type},
				written: &model.EmbeddingModel{ID: 1},
			}
			svc := newModelService(models, fakeSealer{}, &fakeResolver{})
			opts := &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 1}

			if _, err := svc.Update(context.Background(), opts, tt.in, tt.apiKey); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if models.updateCalls != 1 || models.updateKeepConfig != tt.wantKeep {
				t.Fatalf("keepConfig = %v, want %v", models.updateKeepConfig, tt.wantKeep)
			}

			if string(models.updateConfig) != tt.wantConfig {
				t.Fatalf("config = %q, want %q", models.updateConfig, tt.wantConfig)
			}
		})
	}
}

func TestUpdateTypeImmutable(t *testing.T) {
	models := &fakeModelStore{
		located: &model.EmbeddingModel{ID: 1, Type: model.ModelTypeEmbedding},
	}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{})
	opts := &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 1}

	flip := &model.EmbeddingModel{
		Type:         model.ModelTypeReranker,
		Name:         "bge local",
		Provider:     embedding.ProviderBGEReranker,
		IsSelfHosted: true,
		ModelRef:     "BAAI/bge-reranker-v2-m3",
	}

	_, err := svc.Update(context.Background(), opts, flip, "")
	if err == nil || errors.ID(err) != "kb.model.type_immutable" || errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want kb.model.type_immutable InvalidArgument", err)
	}

	if models.updateCalls != 0 {
		t.Fatalf("update calls = %d, want none after a rejected type flip", models.updateCalls)
	}

	if !slices.Contains(models.locateFields, "type") {
		t.Fatalf("current-read fields = %v, must request type", models.locateFields)
	}
}

func TestUpdateLocateErrorPropagates(t *testing.T) {
	models := &fakeModelStore{locateErr: errors.NotFound("model not found")}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{})
	opts := &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 42}

	_, err := svc.Update(context.Background(), opts, cloudInput(), "")
	if err == nil || errors.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want the store NotFound passed through", err)
	}

	if models.updateCalls != 0 {
		t.Fatalf("update calls = %d, want none when the current read fails", models.updateCalls)
	}
}

func TestListPassesFilter(t *testing.T) {
	models := &fakeModelStore{}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{})

	_, _, err := svc.List(context.Background(), readOptions{auth: stubAuther{domainID: 1}},
		model.EmbeddingModelFilter{Type: model.ModelTypeReranker})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if models.listFilter.Type != model.ModelTypeReranker {
		t.Fatalf("filter = %+v", models.listFilter)
	}
}

func registeredModel() *model.EmbeddingModel {
	return &model.EmbeddingModel{
		ID:         5,
		DomainID:   1,
		Type:       model.ModelTypeEmbedding,
		Provider:   embedding.ProviderGemini,
		ModelRef:   "gemini-embedding-001",
		Dimensions: 3,
	}
}

func TestValidateEmbeddingHappyPath(t *testing.T) {
	marked := &model.EmbeddingModel{ID: 5}
	models := &fakeModelStore{
		located: registeredModel(),
		marked:  marked,
		config:  []byte("enc:key123"),
	}
	provider := &fakeProvider{embedRes: embedding.EmbedResult{Vectors: [][]float32{{1, 2, 3}}}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	got, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if got != marked || models.markCalls != 1 {
		t.Fatalf("mark calls = %d, result = %+v", models.markCalls, got)
	}

	if models.configID != 5 || models.configDomainID != 1 {
		t.Fatalf("GetConfig args = (%d, %d)", models.configID, models.configDomainID)
	}

	req := provider.embedReq
	if req == nil {
		t.Fatal("probe was not sent")
	}

	if req.ModelRef != "gemini-embedding-001" || req.APIKey != "key123" ||
		req.Dimensions != 3 || req.Task != embedding.TaskDocument ||
		len(req.Texts) != 1 || req.Texts[0] == "" {
		t.Fatalf("probe request = %+v", req)
	}
}

func TestValidateDimensionsMismatch(t *testing.T) {
	models := &fakeModelStore{located: registeredModel(), config: []byte("enc:key123")}
	provider := &fakeProvider{embedRes: embedding.EmbedResult{Vectors: [][]float32{{1, 2}}}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || errors.ID(err) != "kb.model.dimensions_mismatch" || errors.Code(err) != codes.Aborted {
		t.Fatalf("Validate error = %v, want a dimensions-mismatch abort", err)
	}

	if models.markCalls != 0 {
		t.Fatal("a failed probe must not stamp the model as validated")
	}
}

func TestValidateProviderCallFails(t *testing.T) {
	models := &fakeModelStore{located: registeredModel(), config: []byte("enc:key123")}
	provider := &fakeProvider{embedErr: fmt.Errorf("embedding: endpoint url is required")}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || errors.ID(err) != "kb.model.validation_failed" || errors.Code(err) != codes.Aborted {
		t.Fatalf("Validate error = %v, want a validation-failed abort", err)
	}

	if models.markCalls != 0 {
		t.Fatal("a failed probe must not stamp the model as validated")
	}
}

func TestValidateUnsupportedProvider(t *testing.T) {
	located := registeredModel()
	located.Provider = embedding.ProviderOpenAI
	models := &fakeModelStore{located: located}
	resolver := &fakeResolver{err: fmt.Errorf("%w: %s", embedding.ErrUnsupported, located.Provider)}
	svc := newModelService(models, fakeSealer{}, resolver)

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || errors.ID(err) != "kb.model.provider_unsupported" || errors.Code(err) != codes.Aborted {
		t.Fatalf("Validate error = %v, want a provider-unsupported abort", err)
	}

	if models.markCalls != 0 {
		t.Fatal("an unsupported provider must not validate")
	}
}

func TestValidateUnsupportedRole(t *testing.T) {
	located := registeredModel()
	located.Type = model.ModelTypeReranker
	located.Dimensions = 0
	models := &fakeModelStore{located: located, config: []byte("enc:key123")}
	provider := &fakeProvider{rerankErr: fmt.Errorf("%w: gemini rerank", embedding.ErrUnsupported)}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || errors.ID(err) != "kb.model.provider_unsupported" || errors.Code(err) != codes.Aborted {
		t.Fatalf("Validate error = %v, want a provider-unsupported abort", err)
	}

	if models.markCalls != 0 {
		t.Fatal("an unsupported role must not validate")
	}
}

func TestValidateRerankerHappyPath(t *testing.T) {
	located := &model.EmbeddingModel{
		ID:       6,
		DomainID: 1,
		Type:     model.ModelTypeReranker,
		Provider: embedding.ProviderBGEReranker,
		ModelRef: "BAAI/bge-reranker-v2-m3",
		Endpoint: "http://reranker:8080",
	}
	models := &fakeModelStore{located: located, marked: located}
	provider := &fakeProvider{rerankRes: embedding.RerankResult{Scores: []float64{0.9, 0.1}}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	if _, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 6}); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if models.markCalls != 1 {
		t.Fatalf("mark calls = %d", models.markCalls)
	}

	req := provider.rerankReq
	if req == nil {
		t.Fatal("probe was not sent")
	}

	if req.ModelRef != located.ModelRef || req.Endpoint != located.Endpoint ||
		req.Query == "" || len(req.Documents) != 2 {
		t.Fatalf("probe request = %+v", req)
	}
}

func TestValidateRerankerScoreCountMismatch(t *testing.T) {
	located := &model.EmbeddingModel{
		ID:       6,
		DomainID: 1,
		Type:     model.ModelTypeReranker,
		Provider: embedding.ProviderBGEReranker,
		Endpoint: "http://reranker:8080",
	}
	models := &fakeModelStore{located: located}
	provider := &fakeProvider{rerankRes: embedding.RerankResult{Scores: []float64{0.9}}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 6})

	if err == nil || errors.ID(err) != "kb.model.validation_failed" {
		t.Fatalf("Validate error = %v, want a validation-failed abort", err)
	}

	if models.markCalls != 0 {
		t.Fatal("a failed probe must not stamp the model as validated")
	}
}

func TestValidateGlobalModelRejected(t *testing.T) {
	located := registeredModel()
	located.DomainID = 0
	models := &fakeModelStore{located: located}
	resolver := &fakeResolver{provider: &fakeProvider{}}
	svc := newModelService(models, fakeSealer{}, resolver)

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || errors.ID(err) != "kb.model.global_read_only" || errors.Code(err) != codes.InvalidArgument {
		t.Fatalf("Validate error = %v, want a global-read-only rejection", err)
	}

	if models.configCalls != 0 || len(resolver.resolved) != 0 || models.markCalls != 0 {
		t.Fatal("a global model must be rejected before any credential or provider work")
	}
}

func TestValidateDecryptErrorPropagates(t *testing.T) {
	models := &fakeModelStore{located: registeredModel(), config: []byte("enc:key123")}
	resolver := &fakeResolver{provider: &fakeProvider{}}
	svc := newModelService(models, fakeSealer{decryptErr: fmt.Errorf("crypto: corrupted blob")}, resolver)

	_, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5})

	if err == nil || !strings.Contains(err.Error(), "corrupted blob") {
		t.Fatalf("Validate error = %v, want the decrypt failure", err)
	}

	if len(resolver.resolved) != 0 || models.markCalls != 0 {
		t.Fatal("a broken credential must stop the probe")
	}
}

func TestValidateReadsProbeFields(t *testing.T) {
	models := &fakeModelStore{
		located: registeredModel(),
		marked:  registeredModel(),
		config:  []byte("enc:key123"),
	}
	provider := &fakeProvider{embedRes: embedding.EmbedResult{Vectors: [][]float32{{1, 2, 3}}}}
	svc := newModelService(models, fakeSealer{}, &fakeResolver{provider: provider})

	if _, err := svc.Validate(context.Background(), &stubWriteOpts{auth: stubAuther{domainID: 1}, id: 5}); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if len(models.locateIDs) != 1 || models.locateIDs[0] != 5 {
		t.Fatalf("locate ids = %v", models.locateIDs)
	}

	for _, field := range []string{"domain_id", "type", "provider", "model_ref", "dimensions", "endpoint"} {
		found := false

		for _, got := range models.locateFields {
			if got == field {
				found = true

				break
			}
		}

		if !found {
			t.Fatalf("probe read misses field %q in %v", field, models.locateFields)
		}
	}
}
