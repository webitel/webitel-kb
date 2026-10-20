package service

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/store"
)

var Module = fx.Module("service",
	fx.Provide(
		NewSpaceService,
		provideDomainSettings,
		provideArticleService,
		NewAttachmentService,
		NewIndexingService,
		provideRetrievalService,
		provideEmbeddingModelService,
	),
)

// provideEmbeddingModelService binds the concrete registry to the service's
// resolver seam.
func provideEmbeddingModelService(
	uow store.UnitOfWork, encryptor crypto.Encryptor, registry *embedding.Registry,
) *EmbeddingModelService {
	return NewEmbeddingModelService(uow, encryptor, registry)
}

// provideRetrievalService binds the concrete registry and domain settings to
// the service's seams.
func provideRetrievalService(
	uow store.UnitOfWork, encryptor crypto.Encryptor, registry *embedding.Registry, settings *DomainSettings,
	log *slog.Logger,
) *RetrievalService {
	return NewRetrievalService(uow, encryptor, registry, settings, log)
}

// provideArticleService binds the concrete domain settings to the service's seam.
func provideArticleService(uow store.UnitOfWork, settings *DomainSettings, log *slog.Logger) *ArticleService {
	return NewArticleService(uow, settings, log)
}

// provideDomainSettings builds the domain settings the services apply and
// releases their cache on shutdown.
func provideDomainSettings(uow store.UnitOfWork, log *slog.Logger, lc fx.Lifecycle) (*DomainSettings, error) {
	settings, err := NewDomainSettings(uow, log)
	if err != nil {
		return nil, err
	}

	lc.Append(fx.StopHook(settings.Close))

	return settings, nil
}
