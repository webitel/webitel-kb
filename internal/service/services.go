package service

import (
	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/infra/embedding"
	"github.com/webitel/webitel-kb/internal/store"
)

var Module = fx.Module("service",
	fx.Provide(
		NewSpaceService,
		NewArticleService,
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
