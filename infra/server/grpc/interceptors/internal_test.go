package interceptors

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/api/kb"
	servicepb "github.com/webitel/webitel-kb/api/kb/service"
	"github.com/webitel/webitel-kb/internal/auth"
)

const (
	testToken    = "0123456789abcdef0123456789abcdef"
	indexerCall  = "/webitel.kb.service.Indexing/ResolveSpaceEmbedding"
	publicCall   = "/webitel.kb.Spaces/ListSpaces"
	unknownCall  = "/webitel.kb.Ghost/Vanish"
	infraCall    = "/grpc.health.v1.Health/Check"
	otherService = "/webitel.kb.serviceish.Indexing/Call"
)

func tokenContext(values ...string) context.Context {
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, ServiceTokenHeader, value)
	}

	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestServiceTokenGuardOwns(t *testing.T) {
	guard := NewServiceTokenGuard(testToken)

	tests := []struct {
		name       string
		fullMethod string
		want       bool
	}{
		{"the indexer api is owned", indexerCall, true},
		{"a public method is not", publicCall, false},
		{"an infra method is not", infraCall, false},
		{"a lookalike package is not", otherService, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := guard.Owns(tt.fullMethod); got != tt.want {
				t.Fatalf("owns(%q) = %v, want %v", tt.fullMethod, got, tt.want)
			}
		})
	}
}

func TestServiceTokenGuardAuthorize(t *testing.T) {
	tests := []struct {
		name  string
		guard InternalGuard
		call  func() context.Context

		wantErr bool
	}{
		{"the configured token is accepted", NewServiceTokenGuard(testToken), func() context.Context { return tokenContext(testToken) }, false},
		{"no metadata is refused", NewServiceTokenGuard(testToken), context.Background, true},
		{"a missing header is refused", NewServiceTokenGuard(testToken), func() context.Context {
			return metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-other", testToken))
		}, true},
		{"an empty value is refused", NewServiceTokenGuard(testToken), func() context.Context { return tokenContext("") }, true},
		{"a wrong token is refused", NewServiceTokenGuard(testToken), func() context.Context {
			return tokenContext("0123456789abcdef0123456789abcdeg")
		}, true},
		{"a prefix of the token is refused", NewServiceTokenGuard(testToken), func() context.Context { return tokenContext(testToken[:16]) }, true},
		{"several values are refused", NewServiceTokenGuard(testToken), func() context.Context { return tokenContext(testToken, testToken) }, true},
		{"a guard without a token accepts nothing", NewServiceTokenGuard(""), func() context.Context { return tokenContext("") }, true},
		{"the disabled guard accepts nothing", DisabledGuard{}, func() context.Context { return tokenContext(testToken) }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.guard.Authorize(tt.call())
			if (err != nil) != tt.wantErr {
				t.Fatalf("authorize error = %v, want error %v", err, tt.wantErr)
			}

			if err != nil && errors.Code(err) != codes.Unauthenticated {
				t.Fatalf("error code = %v, want unauthenticated", errors.Code(err))
			}
		})
	}
}

func TestDisabledGuardOwnsNothing(t *testing.T) {
	if (DisabledGuard{}).Owns(indexerCall) {
		t.Fatal("the disabled guard owns the indexer api")
	}
}

func TestUnaryAuthInterceptorGuardsTheInternalAPI(t *testing.T) {
	tests := []struct {
		name  string
		guard InternalGuard
		call  func() context.Context

		wantCode       codes.Code
		wantHandlerRun bool
		wantAuthorize  bool
	}{
		{
			name:           "a valid service token serves the call without rbac",
			guard:          NewServiceTokenGuard(testToken),
			call:           func() context.Context { return tokenContext(testToken) },
			wantCode:       codes.OK,
			wantHandlerRun: true,
		},
		{
			name:     "a wrong service token is refused",
			guard:    NewServiceTokenGuard(testToken),
			call:     func() context.Context { return tokenContext("wrong") },
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "no service token is refused",
			guard:    NewServiceTokenGuard(testToken),
			call:     context.Background,
			wantCode: codes.Unauthenticated,
		},
		{
			// The guard owns nothing, so the fail-closed branch answers.
			name:     "the disabled guard leaves the api closed",
			guard:    DisabledGuard{},
			call:     func() context.Context { return tokenContext(testToken) },
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var handlerRun bool

			handler := func(context.Context, any) (any, error) {
				handlerRun = true

				return "ok", nil
			}

			manager := &fakeManager{session: sessionWithScope("kb_spaces", "r")}
			interceptor := NewUnaryAuthInterceptor(manager, tt.guard)

			_, err := interceptor(tt.call(), nil, &grpc.UnaryServerInfo{FullMethod: indexerCall}, handler)

			if tt.wantCode == codes.OK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if got := errors.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}

			if handlerRun != tt.wantHandlerRun {
				t.Errorf("handler run = %v, want %v", handlerRun, tt.wantHandlerRun)
			}

			// Reaching the auth manager would mean the guard branch was skipped.
			if manager.called != tt.wantAuthorize {
				t.Errorf("authorize called = %v, want %v", manager.called, tt.wantAuthorize)
			}
		})
	}
}

func TestUnaryAuthInterceptorDeniesMethodsWithoutObjectClass(t *testing.T) {
	// A service without an objclass still lands in the generated map.
	previous := webitelAPI
	webitelAPI = kb.WebitelServicesInfo{
		"Indexing": kb.WebitelServices{
			WebitelMethods: map[string]kb.WebitelMethod{"ResolveSpaceEmbedding": {}},
		},
	}

	t.Cleanup(func() { webitelAPI = previous })

	manager := &fakeManager{session: sessionWithScope("kb_spaces", "r")}
	interceptor := NewUnaryAuthInterceptor(manager, DisabledGuard{})

	_, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: indexerCall},
		func(context.Context, any) (any, error) { return "ok", nil },
	)

	if got := errors.Code(err); got != codes.PermissionDenied {
		t.Fatalf("error code = %v, want permission denied (err: %v)", got, err)
	}

	if manager.called {
		t.Error("authorized a method that declares no object class")
	}
}

var _ auth.Manager = (*fakeManager)(nil)

func TestInternalPrefixCoversTheGeneratedService(t *testing.T) {
	// Renaming the proto package would leave the guard owning nothing.
	full := "/" + servicepb.Indexing_ServiceDesc.ServiceName + "/ResolveSpaceEmbedding"

	if !NewServiceTokenGuard(testToken).Owns(full) {
		t.Fatalf("guard does not own %q (prefix %q)", full, internalMethodPrefix)
	}
}

func TestStreamGuardRefusesTheInternalAPI(t *testing.T) {
	tests := []struct {
		name       string
		fullMethod string

		wantHandlerRun bool
	}{
		{name: "an internal stream is refused", fullMethod: indexerCall},
		{name: "a public stream is untouched", fullMethod: publicCall, wantHandlerRun: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var handlerRun bool

			err := NewStreamGuard()(
				nil, nil,
				&grpc.StreamServerInfo{FullMethod: tt.fullMethod},
				func(any, grpc.ServerStream) error {
					handlerRun = true

					return nil
				},
			)

			if handlerRun != tt.wantHandlerRun {
				t.Fatalf("handler run = %v, want %v", handlerRun, tt.wantHandlerRun)
			}

			if !tt.wantHandlerRun && errors.Code(err) != codes.Unauthenticated {
				t.Fatalf("error = %v, want unauthenticated", err)
			}
		})
	}
}
