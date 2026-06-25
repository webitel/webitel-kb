package grpc

import (
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
	"go.uber.org/fx"
)

var Module = fx.Module("grpc",
	fx.Provide(
	//NewMessageService, provide your service here
	),
	fx.Invoke(RegisterService),
)

func RegisterService(
	server *grpcsrv.Server,
	//service *MessageService, set your service here
) {
	// register your gRPC service here
}
