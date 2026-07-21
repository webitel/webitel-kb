package interceptors

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	user_session "github.com/webitel/webitel-kb/internal/auth/session/user_session"
)

type fakeManager struct {
	session auth.Auther
	err     error

	calledObjClass string
	calledAccess   auth.AccessMode
	called         bool
}

func (m *fakeManager) AuthorizeFromContext(_ context.Context, objClass string, access auth.AccessMode) (auth.Auther, error) {
	m.called = true
	m.calledObjClass = objClass
	m.calledAccess = access
	if m.err != nil {
		return nil, m.err
	}
	return m.session, nil
}

func sessionWithScope(class, access string) *user_session.UserAuthSession {
	return &user_session.UserAuthSession{
		Scopes: map[string]*user_session.Scope{
			class: {Class: class, Obac: true, Access: access},
		},
	}
}

func TestUnaryAuthInterceptor(t *testing.T) {
	tests := []struct {
		name string

		fullMethod string
		manager    *fakeManager

		wantCode         codes.Code
		wantHandlerRun   bool
		wantAuthorize    bool
		wantObjClass     string
		wantAccess       auth.AccessMode
		wantSessionInCtx bool
	}{
		{
			name:           "non-webitel method passes through without authorization",
			fullMethod:     "/grpc.health.v1.Health/Check",
			manager:        &fakeManager{},
			wantCode:       codes.OK,
			wantHandlerRun: true,
			wantAuthorize:  false,
		},
		{
			name:           "unrecognized kb method is denied without contacting the manager",
			fullMethod:     "/webitel.kb.Spaces/GhostMethod",
			manager:        &fakeManager{},
			wantCode:       codes.PermissionDenied,
			wantHandlerRun: false,
			wantAuthorize:  false,
		},
		{
			name:           "unauthorized when the manager rejects the token",
			fullMethod:     "/webitel.kb.Spaces/ListSpaces",
			manager:        &fakeManager{err: errors.Unauthenticated("bad token")},
			wantCode:       codes.Unauthenticated,
			wantHandlerRun: false,
			wantAuthorize:  true,
			wantObjClass:   "kb_spaces",
			wantAccess:     auth.Read,
		},
		{
			name:           "denied when the session lacks obac access",
			fullMethod:     "/webitel.kb.Spaces/CreateSpace",
			manager:        &fakeManager{session: sessionWithScope("kb_spaces", "r")},
			wantCode:       codes.PermissionDenied,
			wantHandlerRun: false,
			wantAuthorize:  true,
			wantObjClass:   "kb_spaces",
			wantAccess:     auth.Edit,
		},
		{
			name:             "authorized call reaches the handler with the session in context",
			fullMethod:       "/webitel.kb.EmbeddingModels/ListModels",
			manager:          &fakeManager{session: sessionWithScope("kb_models", "r")},
			wantCode:         codes.OK,
			wantHandlerRun:   true,
			wantAuthorize:    true,
			wantObjClass:     "kb_models",
			wantAccess:       auth.Read,
			wantSessionInCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				handlerRun   bool
				sessionInCtx bool
			)
			handler := func(ctx context.Context, _ any) (any, error) {
				handlerRun = true
				_, sessionInCtx = auth.FromContext(ctx)
				return "ok", nil
			}

			interceptor := NewUnaryAuthInterceptor(tt.manager)
			_, err := interceptor(
				context.Background(),
				nil,
				&grpc.UnaryServerInfo{FullMethod: tt.fullMethod},
				handler,
			)

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
			if tt.manager.called != tt.wantAuthorize {
				t.Errorf("authorize called = %v, want %v", tt.manager.called, tt.wantAuthorize)
			}
			if tt.wantAuthorize {
				if tt.manager.calledObjClass != tt.wantObjClass {
					t.Errorf("objclass = %q, want %q", tt.manager.calledObjClass, tt.wantObjClass)
				}
				if tt.manager.calledAccess != tt.wantAccess {
					t.Errorf("access = %v, want %v", tt.manager.calledAccess, tt.wantAccess)
				}
			}
			if sessionInCtx != tt.wantSessionInCtx {
				t.Errorf("session in ctx = %v, want %v", sessionInCtx, tt.wantSessionInCtx)
			}
		})
	}
}
