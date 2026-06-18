package interceptors

import (
	"context"

	"google.golang.org/grpc"
)

// UnaryAuthInterceptor is a gRPC interceptor for handling authentication.
// TODO: pass auth methods in args and implement auth logic.
func NewUnaryAuthInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		return handler(ctx, req)
	}
}
