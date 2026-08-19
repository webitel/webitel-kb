package leader

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/webitel/webitel-go-kit/infra/discovery"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/internal/model"
)

// Module provides the cluster elector.
var Module = fx.Module("leader",
	fx.Provide(fx.Annotate(ProvideElector, fx.As(new(Elector)))),
)

// ProvideElector builds the elector for this instance; the node id is the
// same one the service registers itself with.
func ProvideElector(cfg *config.Config, log *slog.Logger) (*ConsulElector, error) {
	return New(cfg.Consul.Addr, discovery.GenerateInstanceID(model.ServiceName), log)
}
