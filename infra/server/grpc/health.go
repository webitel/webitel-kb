package server

import (
	"github.com/webitel/webitel-go-kit/infra/health"
)

// registerHealth adds the gRPC listener as the critical check.
func registerHealth(srv *Server, h *health.Registry) {
	h.Critical("grpc", health.ListenerCheck(srv.Listener()))
}
