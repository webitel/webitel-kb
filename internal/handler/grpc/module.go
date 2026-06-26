package grpc

import (
	kb "github.com/webitel/webitel-kb/api/kb"
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
	"go.uber.org/fx"
)

var Module = fx.Module("grpc",
	fx.Provide(
		NewSpacesService,
		NewEmbeddingModelsService,
		NewArticlesService,
		NewVersionsService,
		NewTagsService,
		NewAttachmentsService,
		NewRetrievalService,
	),
	fx.Invoke(RegisterService),
)

// RegisterService registers all KB gRPC services on the server.
func RegisterService(
	server *grpcsrv.Server,
	spaces *SpacesService,
	models *EmbeddingModelsService,
	articles *ArticlesService,
	versions *VersionsService,
	tags *TagsService,
	attachments *AttachmentsService,
	retrieval *RetrievalService,
) {
	kb.RegisterSpacesServer(server, spaces)
	kb.RegisterEmbeddingModelsServer(server, models)
	kb.RegisterArticlesServer(server, articles)
	kb.RegisterVersionsServer(server, versions)
	kb.RegisterTagsServer(server, tags)
	kb.RegisterAttachmentsServer(server, attachments)
	kb.RegisterRetrievalServer(server, retrieval)
}
