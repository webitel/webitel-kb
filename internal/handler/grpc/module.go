package grpc

import (
	kb "github.com/webitel/webitel-kb/api/kb"
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
	"go.uber.org/fx"
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
	),
	fx.Invoke(RegisterService),
)

// RegisterService registers all KB gRPC services on the server.
func RegisterService(
	server *grpcsrv.Server,
	spaces *SpacesServer,
	models *EmbeddingModelsServer,
	articles *ArticlesServer,
	versions *VersionsServer,
	tags *TagsServer,
	attachments *AttachmentsServer,
	retrieval *RetrievalServer,
) {
	kb.RegisterSpacesServer(server, spaces)
	kb.RegisterEmbeddingModelsServer(server, models)
	kb.RegisterArticlesServer(server, articles)
	kb.RegisterVersionsServer(server, versions)
	kb.RegisterTagsServer(server, tags)
	kb.RegisterAttachmentsServer(server, attachments)
	kb.RegisterRetrievalServer(server, retrieval)
}
