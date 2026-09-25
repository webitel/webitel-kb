package interceptors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// gatewayError mirrors the error the webitel.go gateway decodes.
type gatewayError struct {
	ID     string `json:"id"`
	Code   int32  `json:"code"`
	Detail string `json:"detail"`
	Status string `json:"status"`
}

func callWithError(t *testing.T, handlerErr error) (codes.Code, gatewayError) {
	t.Helper()

	intercept := NewUnaryErrorInterceptor(slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := func(context.Context, any) (any, error) { return nil, handlerErr }

	_, err := intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: publicCall}, handler)

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v is not a status", err)
	}

	var body gatewayError
	if uerr := json.Unmarshal([]byte(st.Message()), &body); uerr != nil {
		t.Fatalf("status message %q is not gateway json: %v", st.Message(), uerr)
	}

	return st.Code(), body
}

func TestUnaryErrorInterceptor(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   codes.Code
		wantHTTP   int
		wantID     string
		wantDetail string
	}{
		{
			name:     "a version conflict is a conflict",
			err:      errors.Aborted("article was changed concurrently", errors.WithID("kb.article.version_conflict")),
			wantCode: codes.Aborted, wantHTTP: http.StatusConflict,
			wantID: "kb.article.version_conflict", wantDetail: "article was changed concurrently",
		},
		{
			name:     "an invalid argument keeps its id",
			err:      errors.InvalidArgument("maximum hierarchy depth is 5", errors.WithID("kb.article.create_depth")),
			wantCode: codes.InvalidArgument, wantHTTP: http.StatusBadRequest,
			wantID: "kb.article.create_depth", wantDetail: "maximum hierarchy depth is 5",
		},
		{
			name: "a failed precondition is a bad request",
			err: errors.New("model is not validated",
				errors.WithCode(codes.FailedPrecondition), errors.WithID("kb.space.model_not_validated")),
			wantCode: codes.FailedPrecondition, wantHTTP: http.StatusBadRequest,
			wantID: "kb.space.model_not_validated", wantDetail: "model is not validated",
		},
		{
			name:     "a duplicate is a conflict",
			err:      errors.New("entity already exists", errors.WithCode(codes.AlreadyExists), errors.WithID("store.pg.unique")),
			wantCode: codes.AlreadyExists, wantHTTP: http.StatusConflict,
			wantID: "store.pg.unique", wantDetail: "entity already exists",
		},
		{
			name:     "a missing entity is not found",
			err:      errors.NotFound("entity does not exist", errors.WithID("store.pg.not_found")),
			wantCode: codes.NotFound, wantHTTP: http.StatusNotFound,
			wantID: "store.pg.not_found", wantDetail: "entity does not exist",
		},
		{
			name:     "an unauthenticated call without an id gets the legacy one",
			err:      errors.Unauthenticated("session is required"),
			wantCode: codes.Unauthenticated, wantHTTP: http.StatusUnauthorized,
			wantID: "api.process.unauthenticated", wantDetail: "session is required",
		},
		{
			name:     "a denied call is forbidden",
			err:      errors.Forbidden("access denied", errors.WithID("kb.auth.denied")),
			wantCode: codes.PermissionDenied, wantHTTP: http.StatusForbidden,
			wantID: "kb.auth.denied", wantDetail: "access denied",
		},
		{
			name:     "an unavailable provider is a service outage",
			err:      errors.Unavailable("embedding provider is unavailable", errors.WithID("kb.retrieval.embedding_unavailable")),
			wantCode: codes.Unavailable, wantHTTP: http.StatusServiceUnavailable,
			wantID: "kb.retrieval.embedding_unavailable", wantDetail: "embedding provider is unavailable",
		},
		{
			name:     "a plain status from the validator is a bad request",
			err:      status.Error(codes.InvalidArgument, "validation error: name: value is required"),
			wantCode: codes.InvalidArgument, wantHTTP: http.StatusBadRequest,
			wantID: "api.process.bad_args", wantDetail: "validation error: name: value is required",
		},
		{
			name:     "an expired deadline is a gateway timeout",
			err:      fmt.Errorf("query: %w", context.DeadlineExceeded),
			wantCode: codes.DeadlineExceeded, wantHTTP: http.StatusGatewayTimeout,
			wantID: "api.process.internal", wantDetail: http.StatusText(http.StatusGatewayTimeout),
		},
		{
			name: "a storage error from an expired deadline is a gateway timeout",
			err: errors.Internal("storage error", errors.WithID("store.pg.internal"),
				errors.WithCause(fmt.Errorf("query: %w", context.DeadlineExceeded))),
			wantCode: codes.DeadlineExceeded, wantHTTP: http.StatusGatewayTimeout,
			wantID: "store.pg.internal", wantDetail: "storage error",
		},
		{
			name:     "a coded error keeps its code over the context",
			err:      errors.Unavailable("embedding provider is unavailable", errors.WithCause(context.DeadlineExceeded)),
			wantCode: codes.Unavailable, wantHTTP: http.StatusServiceUnavailable,
			wantID: "api.process.internal", wantDetail: http.StatusText(http.StatusServiceUnavailable),
		},
		{
			name:     "a call the caller canceled is internal",
			err:      fmt.Errorf("query: %w", context.Canceled),
			wantCode: codes.Canceled, wantHTTP: http.StatusInternalServerError,
			wantID: "api.process.internal", wantDetail: http.StatusText(http.StatusInternalServerError),
		},
		{
			name:     "an unexpected error hides its text",
			err:      stderrors.New("pq: relation kb.secret does not exist"),
			wantCode: codes.Internal, wantHTTP: http.StatusInternalServerError,
			wantID: "api.process.internal", wantDetail: http.StatusText(http.StatusInternalServerError),
		},
		{
			name:     "an internal error with an id keeps its safe text",
			err:      errors.Internal("storage error", errors.WithID("store.pg.internal"), errors.WithCause(stderrors.New("raw"))),
			wantCode: codes.Internal, wantHTTP: http.StatusInternalServerError,
			wantID: "store.pg.internal", wantDetail: "storage error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := callWithError(t, tt.err)

			if code != tt.wantCode {
				t.Errorf("grpc code = %v, want %v", code, tt.wantCode)
			}

			if body.Code != int32(tt.wantHTTP) || body.Status != http.StatusText(tt.wantHTTP) {
				t.Errorf("http = %d %q, want %d", body.Code, body.Status, tt.wantHTTP)
			}

			if body.ID != tt.wantID {
				t.Errorf("id = %q, want %q", body.ID, tt.wantID)
			}

			if body.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", body.Detail, tt.wantDetail)
			}
		})
	}
}

func TestUnaryErrorInterceptorPassesSuccess(t *testing.T) {
	intercept := NewUnaryErrorInterceptor(slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := func(context.Context, any) (any, error) { return "ok", nil }

	resp, err := intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: publicCall}, handler)
	if err != nil || resp != "ok" {
		t.Fatalf("resp, err = %v, %v, want the handler answer", resp, err)
	}
}
