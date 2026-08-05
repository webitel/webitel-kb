package server

import (
	"github.com/webitel/webitel-kb/infra/pubsub"
	"github.com/webitel/webitel-kb/infra/tls"
	"go.uber.org/fx"

	"github.com/webitel/webitel-go-kit/infra/discovery"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/infra/crypto"
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
	grpchandler "github.com/webitel/webitel-kb/internal/handler/grpc"
	"github.com/webitel/webitel-kb/internal/relay"
	"github.com/webitel/webitel-kb/internal/service"
	"github.com/webitel/webitel-kb/internal/store/postgres"
)

func NewApp(cfg *config.Config) *fx.App {
	return fx.New(
		fx.Provide(
			func() *config.Config { return cfg },
			ProvideLogger,
			ProvideSD,
			ProvideAuthManager,
			ProvideEncryptor,
		),
		fx.Invoke(func(discovery discovery.DiscoveryProvider) error { return nil }),
		fx.Invoke(func(crypto.Encryptor) {}),

		pubsub.Module,
		tls.Module,
		postgres.Module,
		relay.Module,
		service.Module,
		grpcsrv.Module,
		grpchandler.Module,
	)
}
