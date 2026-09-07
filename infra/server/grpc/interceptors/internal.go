package interceptors

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

const (
	// internalMethodPrefix is the full-method prefix of the service-to-service API.
	internalMethodPrefix = "/webitel.kb.service."

	// ServiceTokenHeader carries the caller's service token.
	ServiceTokenHeader = "x-webitel-service-token"
)

// InternalGuard authorizes service-to-service methods, which carry no user session.
type InternalGuard interface {
	Owns(fullMethod string) bool
	Authorize(ctx context.Context) error
}

// DisabledGuard owns nothing: the internal API is not served.
type DisabledGuard struct{}

func (DisabledGuard) Owns(string) bool { return false }

func (DisabledGuard) Authorize(context.Context) error { return errServiceToken() }

// ServiceTokenGuard authorizes the internal API by a shared service token.
type ServiceTokenGuard struct {
	token []byte
}

// NewServiceTokenGuard builds a guard for the token.
func NewServiceTokenGuard(token string) ServiceTokenGuard {
	return ServiceTokenGuard{token: []byte(token)}
}

func (g ServiceTokenGuard) Owns(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, internalMethodPrefix)
}

func (g ServiceTokenGuard) Authorize(ctx context.Context) error {
	// An empty expectation must not be met by an empty header.
	if len(g.token) == 0 {
		return errServiceToken()
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errServiceToken()
	}

	values := md.Get(ServiceTokenHeader)
	if len(values) != 1 {
		return errServiceToken()
	}

	if subtle.ConstantTimeCompare([]byte(values[0]), g.token) != 1 {
		return errServiceToken()
	}

	return nil
}

// NewStreamGuard refuses streaming calls to the internal API.
func NewStreamGuard() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if strings.HasPrefix(info.FullMethod, internalMethodPrefix) {
			return errServiceToken()
		}

		return handler(srv, stream)
	}
}

func errServiceToken() error {
	return errors.Unauthenticated(
		"unauthorized",
		errors.WithID("auth.internal.denied"),
	)
}
