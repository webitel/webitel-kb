package interceptors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

// rpcError is the error body the webitel.go gateway reads from a status
// message: code is the HTTP status it answers with.
type rpcError struct {
	ID     string `json:"id"`
	Code   int32  `json:"code"`
	Detail string `json:"detail"`
	Status string `json:"status"`
}

// NewUnaryErrorInterceptor turns a handler error into a gRPC status with the
// HTTP status and the error id for the gateway.
func NewUnaryErrorInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		return nil, errorStatus(ctx, log, info.FullMethod, err)
	}
}

func errorStatus(ctx context.Context, log *slog.Logger, method string, err error) error {
	code, detail := classify(err)
	httpCode := httpStatus(code)

	id := errors.ID(err)
	if id == "" {
		id = fallbackID(code)

		// An unexpected failure keeps its text in the log only.
		if httpCode >= http.StatusInternalServerError {
			detail = http.StatusText(httpCode)
		}
	}

	// A caller that went away is not a failure of the service.
	level := slog.LevelWarn
	if httpCode >= http.StatusInternalServerError && code != codes.Canceled {
		level = slog.LevelError

		trace.SpanFromContext(ctx).RecordError(err)
	}

	log.Log(ctx, level, "rpc failed",
		slog.String("method", method),
		slog.String("code", code.String()),
		slog.String("id", id),
		slog.String("error", errors.Details(err)),
	)

	body, _ := json.Marshal(rpcError{
		ID:     id,
		Code:   int32(httpCode),
		Detail: detail,
		Status: http.StatusText(httpCode),
	})

	return status.Error(code, string(body))
}

// classify finds the code of an application error or a plain gRPC status; an
// internal failure caused by the call context reports as that context error.
func classify(err error) (codes.Code, string) {
	code, detail := codes.Internal, err.Error()

	if value, ok := errors.Lookup(err, errors.ErrKeyCode); ok {
		if c, ok := value.(codes.Code); ok && c != codes.OK {
			code = c
		}
	} else if st, ok := status.FromError(err); ok {
		code, detail = st.Code(), st.Message()
	}

	if code == codes.Internal || code == codes.Unknown {
		switch {
		case stderrors.Is(err, context.DeadlineExceeded):
			return codes.DeadlineExceeded, detail
		case stderrors.Is(err, context.Canceled):
			return codes.Canceled, detail
		}
	}

	return code, detail
}

// httpStatus is the canonical HTTP mapping of google.rpc.Code.
func httpStatus(code codes.Code) int {
	switch code {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.Aborted, codes.AlreadyExists:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.OK, codes.Canceled, codes.Unknown, codes.Internal, codes.DataLoss:
		return http.StatusInternalServerError
	}

	return http.StatusInternalServerError
}

// fallbackID is the legacy id of an error that carries none.
func fallbackID(code codes.Code) string {
	switch httpStatus(code) {
	case http.StatusUnauthorized:
		return "api.process.unauthenticated"
	case http.StatusForbidden:
		return "api.process.unauthorized"
	case http.StatusNotFound:
		return "api.process.not_found"
	case http.StatusBadRequest:
		return "api.process.bad_args"
	case http.StatusConflict:
		return "api.process.conflict"
	default:
		return "api.process.internal"
	}
}
