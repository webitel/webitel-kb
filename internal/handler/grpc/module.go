package grpc

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/api/kb"
	kbindexer "github.com/webitel/webitel-kb/api/kb/indexer"
	"github.com/webitel/webitel-kb/config"
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
)

var Module = fx.Module("grpc",
	fx.Provide(
		NewSpacesServer,
		NewEmbeddingModelsServer,
		NewArticlesServer,
		NewVersionsServer,
		NewTagsServer,
		NewAttachmentsServer,
		NewRetrievalServer,
		NewIndexingServer,
	),
	fx.Invoke(RegisterService),
)

// RegisterService registers all KB gRPC services on the server.
func RegisterService(
	conf *config.Config,
	log *slog.Logger,
	server *grpcsrv.Server,
	spaces *SpacesServer,
	models *EmbeddingModelsServer,
	articles *ArticlesServer,
	versions *VersionsServer,
	tags *TagsServer,
	attachments *AttachmentsServer,
	retrieval *RetrievalServer,
	indexing *IndexingServer,
) {
	kb.RegisterSpacesServer(server, spaces)
	kb.RegisterEmbeddingModelsServer(server, models)
	kb.RegisterArticlesServer(server, articles)
	kb.RegisterVersionsServer(server, versions)
	kb.RegisterTagsServer(server, tags)
	kb.RegisterAttachmentsServer(server, attachments)
	kb.RegisterRetrievalServer(server, retrieval)

	// Served only where a token guards it: it hands out a provider credential.
	if conf.Service.Internal.Token == "" {
		log.Warn("internal indexing api is disabled: service.internal.token is not set")

		return
	}

	kbindexer.RegisterIndexingServer(server, indexing)
	log.Info("internal indexing api is enabled")
}
