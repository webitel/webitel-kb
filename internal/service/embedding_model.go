package service

import (
	"context"
	stderrors "errors"
	"net/url"
	"slices"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// Validation probes. The texts are arbitrary: a probe only proves the model is
// reachable, the credential works and the declared dimensions are real.
const (
	probeText  = "Webitel knowledge base validation probe."
	probeQuery = "how do I reset my password?"
)

var probeDocuments = []string{
	"To reset your password, open the profile settings and choose a new one.",
	"Company holidays are announced at the start of every year.",
}

// Provider groups. Registration rules are driven by the group, not by the
// self-hosted flag: cloud providers authenticate with an API key, embedded
// ones serve the canonical HTTP contract and take no credential at all.
var (
	cloudProviders = map[string]struct{}{
		embedding.ProviderGemini: {},
		embedding.ProviderOpenAI: {},
		embedding.ProviderCohere: {},
		embedding.ProviderAzure:  {},
	}

	embeddedProviders = map[string]struct{}{
		embedding.ProviderBGEM3:       {},
		embedding.ProviderE5:          {},
		embedding.ProviderBGEReranker: {},
		embedding.ProviderBYOM:        {},
	}
)

// ProviderResolver resolves a registered provider key to its client.
// *embedding.Registry satisfies it.
type ProviderResolver interface {
	ForModel(provider string) (embedding.Provider, error)
}

// EmbeddingModelService owns the model registry business rules.
type EmbeddingModelService struct {
	uow       store.UnitOfWork
	encryptor crypto.Encryptor
	providers ProviderResolver
}

func NewEmbeddingModelService(
	uow store.UnitOfWork, encryptor crypto.Encryptor, providers ProviderResolver,
) *EmbeddingModelService {
	return &EmbeddingModelService{uow: uow, encryptor: encryptor, providers: providers}
}

func (s *EmbeddingModelService) List(
	ctx context.Context, opts options.Searcher, filter model.EmbeddingModelFilter,
) ([]*model.EmbeddingModel, bool, error) {
	return s.uow.EmbeddingModelStore().List(ctx, opts, filter)
}

func (s *EmbeddingModelService) Locate(
	ctx context.Context, opts options.Searcher,
) (*model.EmbeddingModel, error) {
	return s.uow.EmbeddingModelStore().Locate(ctx, opts)
}

// Create registers a model owned by the caller's domain. The API key travels
// outside the model and is stored encrypted; the model starts unvalidated.
func (s *EmbeddingModelService) Create(
	ctx context.Context, opts options.Creator, in *model.EmbeddingModel, apiKey string,
) (*model.EmbeddingModel, error) {
	if err := validateInput(in, apiKey, true); err != nil {
		return nil, err
	}

	setStorageDimensions(in)

	config, err := s.sealKey(ctx, apiKey)
	if err != nil {
		return nil, err
	}

	return s.uow.EmbeddingModelStore().Create(ctx, opts, in, config)
}

// modelMergeFields is what the locked read must carry for the merge.
var modelMergeFields = []string{
	"id", "type", "name", "provider", "is_self_hosted", "model_ref", "endpoint",
}

// errGlobalReadOnly refuses a write to a model seeded for every domain.
var errGlobalReadOnly = errors.InvalidArgument(
	"global models are read-only",
	errors.WithID("kb.model.global_read_only"),
)

// errModelInUse refuses an identity change of a model a space relies on.
var errModelInUse = errors.New(
	"a model used by a space cannot change its provider or model",
	errors.WithCode(codes.FailedPrecondition),
	errors.WithID("kb.model.in_use"),
)

// errModelChanged reports a model rewritten while its update was being probed.
var errModelChanged = errors.Aborted(
	"model was changed concurrently",
	errors.WithID("kb.model.version_conflict"),
)

// Update merges the input over the stored model and rewrites it. A model in use
// keeps its identity; a new connection is probed before the write. An empty API
// key on a cloud model keeps the stored credential (the contract has no way to
// clear it); an embedded model always clears the credential, so switching a
// model from a cloud provider cannot leave a stale key behind.
func (s *EmbeddingModelService) Update(
	ctx context.Context, opts options.Updator, in *model.EmbeddingModel, apiKey string,
) (*model.EmbeddingModel, error) {
	mask := opts.GetMask()
	if len(mask) != 0 && !slices.Contains(mask, "api_key") {
		apiKey = ""
	}

	config, err := s.sealKey(ctx, apiKey)
	if err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()
	read := readOptions{
		auth: session, ids: []int64{opts.GetID()}, fields: append(slices.Clone(modelMergeFields), "domain_id"),
	}

	found, err := s.uow.EmbeddingModelStore().Locate(ctx, read)
	if err != nil {
		return nil, err
	}

	if found.DomainID == 0 {
		return nil, errGlobalReadOnly
	}

	merged := found.Merge(in, mask)

	if err := validateInput(merged, apiKey, false); err != nil {
		return nil, err
	}

	setStorageDimensions(merged)

	if found.Type != merged.Type {
		return nil, errors.InvalidArgument(
			"model type is immutable",
			errors.WithID("kb.model.type_immutable"),
		)
	}

	inUse, err := s.uow.EmbeddingModelStore().InUse(ctx, found.ID, session.GetDomainID())
	if err != nil {
		return nil, err
	}

	validation, err := s.revalidate(ctx, found, merged, apiKey, session.GetDomainID(), inUse)
	if err != nil {
		return nil, err
	}

	var updated *model.EmbeddingModel

	err = s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		locked, err := tx.EmbeddingModelStore().LocateForUpdate(ctx, read)
		if err != nil {
			return err
		}

		used, err := tx.EmbeddingModelStore().InUse(ctx, found.ID, session.GetDomainID())
		if err != nil {
			return err
		}

		// A model a space took meanwhile must not lose its stamp.
		if !sameRegistration(found, locked) || (used && !inUse && validation == store.ValidationReset) {
			return errModelChanged
		}

		switch {
		case isEmbedded(merged.Provider):
			updated, err = tx.EmbeddingModelStore().Update(ctx, opts, merged, nil, false, validation)
		case apiKey == "":
			updated, err = tx.EmbeddingModelStore().Update(ctx, opts, merged, nil, true, validation)
		default:
			updated, err = tx.EmbeddingModelStore().Update(ctx, opts, merged, config, false, validation)
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// revalidate decides what the update does with the validation stamp.
func (s *EmbeddingModelService) revalidate(
	ctx context.Context, found, merged *model.EmbeddingModel, apiKey string, domainID int64, inUse bool,
) (store.ModelValidation, error) {
	identity := found.Provider != merged.Provider || found.ModelRef != merged.ModelRef ||
		found.IsSelfHosted != merged.IsSelfHosted
	connection := found.Endpoint != merged.Endpoint || apiKey != ""

	switch {
	case !identity && !connection:
		return store.ValidationKeep, nil
	case !inUse:
		return store.ValidationReset, nil
	case identity:
		return store.ValidationKeep, errModelInUse
	}

	key := apiKey
	if key == "" {
		stored, err := s.openKey(ctx, found.ID, domainID)
		if err != nil {
			return store.ValidationKeep, err
		}

		key = stored
	}

	provider, err := s.provider(merged.Provider)
	if err != nil {
		return store.ValidationKeep, err
	}

	if err := probe(ctx, provider, merged, key); err != nil {
		return store.ValidationKeep, err
	}

	return store.ValidationStamp, nil
}

// sameRegistration reports whether a model kept the fields the update read.
func sameRegistration(a, b *model.EmbeddingModel) bool {
	return a.Type == b.Type && a.Name == b.Name && a.Provider == b.Provider &&
		a.IsSelfHosted == b.IsSelfHosted && a.ModelRef == b.ModelRef && a.Endpoint == b.Endpoint
}

func (s *EmbeddingModelService) Delete(
	ctx context.Context, opts options.Deleter,
) (*model.EmbeddingModel, error) {
	return s.uow.EmbeddingModelStore().Delete(ctx, opts)
}

// Validate runs the registration probe: a test call against the live provider
// proving the credential works, the endpoint answers and the declared
// dimensions are real. Success stamps the model as selectable. The probe is an
// outbound HTTP call, so no transaction or pinned connection is held across it;
// a registration changed mid-probe simply validates again.
func (s *EmbeddingModelService) Validate(
	ctx context.Context, opts options.Updator,
) (*model.EmbeddingModel, error) {
	found, err := s.uow.EmbeddingModelStore().Locate(ctx, readOptions{
		auth: opts.GetAuthOpts(),
		ids:  []int64{opts.GetID()},
		fields: []string{
			"id", "domain_id", "type", "provider", "is_self_hosted",
			"model_ref", "dimensions", "endpoint",
		},
	})
	if err != nil {
		return nil, err
	}

	if found.DomainID == 0 {
		return nil, errGlobalReadOnly
	}

	apiKey, err := s.openKey(ctx, found.ID, opts.GetAuthOpts().GetDomainID())
	if err != nil {
		return nil, err
	}

	provider, err := s.provider(found.Provider)
	if err != nil {
		return nil, err
	}

	if err := probe(ctx, provider, found, apiKey); err != nil {
		return nil, err
	}

	return s.uow.EmbeddingModelStore().MarkValidated(ctx, opts)
}

// provider resolves the client of a provider key.
func (s *EmbeddingModelService) provider(name string) (embedding.Provider, error) {
	p, err := s.providers.ForModel(name)
	if err != nil {
		if stderrors.Is(err, embedding.ErrUnsupported) {
			return nil, unsupportedProvider(err)
		}

		return nil, err
	}

	return p, nil
}

// sealKey encrypts a non-empty API key into the stored credential.
func (s *EmbeddingModelService) sealKey(ctx context.Context, apiKey string) ([]byte, error) {
	if apiKey == "" {
		return nil, nil
	}

	return s.encryptor.Encrypt(ctx, []byte(apiKey))
}

// openKey loads and decrypts the stored credential; a model without one yields
// an empty key.
func (s *EmbeddingModelService) openKey(ctx context.Context, id, domainID int64) (string, error) {
	config, err := s.uow.EmbeddingModelStore().GetConfig(ctx, id, domainID)
	if err != nil {
		return "", err
	}

	plain, err := s.encryptor.Decrypt(ctx, config)
	if err != nil {
		return "", err
	}

	return string(plain), nil
}

// probe makes the test call matching the model's role and checks the response
// shape against the registration.
func probe(ctx context.Context, p embedding.Provider, m *model.EmbeddingModel, apiKey string) error {
	switch m.Type {
	case model.ModelTypeEmbedding:
		res, err := p.Embed(ctx, embedding.EmbedRequest{
			ModelRef:   m.ModelRef,
			APIKey:     apiKey,
			Endpoint:   m.Endpoint,
			Dimensions: int(m.Dimensions),
			Task:       embedding.TaskDocument,
			Texts:      []string{probeText},
		})
		if err != nil {
			return probeFailed(err)
		}

		if len(res.Vectors) != 1 {
			return errors.New(
				"model validation failed: provider returned no vector for the probe",
				errors.WithCode(codes.FailedPrecondition),
				errors.WithID("kb.model.validation_failed"),
			)
		}

		if got := int32(len(res.Vectors[0])); got != m.Dimensions {
			return errors.New(
				"dimensions mismatch between the model response and the registration",
				errors.WithCode(codes.FailedPrecondition),
				errors.WithID("kb.model.dimensions_mismatch"),
				errors.WithValue("got", got),
				errors.WithValue("registered", m.Dimensions),
			)
		}
	case model.ModelTypeReranker:
		res, err := p.Rerank(ctx, embedding.RerankRequest{
			ModelRef:  m.ModelRef,
			APIKey:    apiKey,
			Endpoint:  m.Endpoint,
			Query:     probeQuery,
			Documents: probeDocuments,
		})
		if err != nil {
			return probeFailed(err)
		}

		if len(res.Scores) != len(probeDocuments) {
			return errors.New(
				"model validation failed: provider scored a different number of documents",
				errors.WithCode(codes.FailedPrecondition),
				errors.WithID("kb.model.validation_failed"),
			)
		}
	default:
		return errors.Internal(
			"model has an unknown type",
			errors.WithID("kb.model.type_unknown"),
		)
	}

	return nil
}

// probeFailed classifies a failed provider call: a missing capability (the
// provider has no client yet, or no client for this model role) reports as
// unsupported, anything else as a plain validation failure.
func probeFailed(err error) error {
	if stderrors.Is(err, embedding.ErrUnsupported) {
		return unsupportedProvider(err)
	}

	return errors.New(
		"model validation failed",
		errors.WithCode(codes.FailedPrecondition),
		errors.WithID("kb.model.validation_failed"),
		errors.WithCause(err),
	)
}

func unsupportedProvider(err error) error {
	return errors.New(
		"provider is not supported yet",
		errors.WithCode(codes.FailedPrecondition),
		errors.WithID("kb.model.provider_unsupported"),
		errors.WithCause(err),
	)
}

// validateInput enforces the registration matrix the shared input message
// cannot: provider-group consistency, role-specific dimensions and the
// credential rules.
func validateInput(in *model.EmbeddingModel, apiKey string, create bool) error {
	if in.Type != model.ModelTypeEmbedding && in.Type != model.ModelTypeReranker {
		return errors.InvalidArgument(
			"type must be embedding or reranker",
			errors.WithID("kb.model.type_invalid"),
		)
	}

	if in.Name == "" {
		return errors.InvalidArgument(
			"name is required",
			errors.WithID("kb.model.name_required"),
		)
	}

	if err := validateProvider(in); err != nil {
		return err
	}

	if in.ModelRef == "" {
		return errors.InvalidArgument(
			"model_ref is required",
			errors.WithID("kb.model.model_ref_required"),
		)
	}

	if in.Endpoint != "" && !validEndpointURL(in.Endpoint) {
		return errors.InvalidArgument(
			"endpoint must be an absolute http(s) URL",
			errors.WithID("kb.model.endpoint_invalid"),
		)
	}

	return validateKey(in, apiKey, create)
}

// setStorageDimensions assigns the vector size the schema stores.
func setStorageDimensions(in *model.EmbeddingModel) {
	if in.Type == model.ModelTypeEmbedding {
		in.Dimensions = model.EmbeddingStorageDimensions

		return
	}

	in.Dimensions = 0
}

func validateProvider(in *model.EmbeddingModel) error {
	_, cloud := cloudProviders[in.Provider]
	_, embedded := embeddedProviders[in.Provider]

	if !cloud && !embedded {
		return errors.InvalidArgument(
			"provider is not recognized",
			errors.WithID("kb.model.provider_invalid"),
		)
	}

	if in.IsSelfHosted != embedded {
		return errors.InvalidArgument(
			"is_self_hosted does not match the provider",
			errors.WithID("kb.model.self_hosted_mismatch"),
		)
	}

	return nil
}

func validateKey(in *model.EmbeddingModel, apiKey string, create bool) error {
	if isEmbedded(in.Provider) {
		if apiKey != "" {
			return errors.InvalidArgument(
				"api_key applies to cloud providers only",
				errors.WithID("kb.model.api_key_not_applicable"),
			)
		}

		return nil
	}

	if create && apiKey == "" {
		return errors.InvalidArgument(
			"api_key is required for a cloud provider",
			errors.WithID("kb.model.api_key_required"),
		)
	}

	return nil
}

func isEmbedded(provider string) bool {
	_, ok := embeddedProviders[provider]

	return ok
}

func validEndpointURL(raw string) bool {
	u, err := url.Parse(raw)

	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
