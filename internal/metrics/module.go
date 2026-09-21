package metrics

import "go.uber.org/fx"

// Module provides the instruments. The global meter forwards them to the
// provider the OTel setup installs, whichever of the two comes first.
var Module = fx.Module("metrics",
	fx.Provide(Provide),
)
