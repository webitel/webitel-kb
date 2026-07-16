package interceptors

import (
	"context"
	"regexp"
	"strings"

	"google.golang.org/grpc"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	kb "github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/auth"
)

// SessionHeader is the context key the authorized caller session is stored under.
const SessionHeader = "session"

// kbMethodPrefix is the full-method prefix of the KB API services. Methods under
// it must be present in kb.WebitelAPI; anything else (health, reflection) is infra.
const kbMethodPrefix = "/webitel.kb."

// reg strips the proto package prefix from a service name (webitel.kb.Spaces -> Spaces).
var reg = regexp.MustCompile(`^(.*\.)`)

// NewUnaryAuthInterceptor authenticates and authorizes unary RPCs.
// The object class and required access mode are resolved from the generated
// kb.WebitelAPI map. A KB method absent from the map is
// denied rather than served unauthenticated; non-KB infra methods (health
// checks, reflection) pass through without authorization.
func NewUnaryAuthInterceptor(manager auth.Manager) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		objClass, licenses, action, ok := objClassWithAction(info.FullMethod)
		if !ok {
			// Fail closed for unrecognized KB methods; pass through infra methods.
			if strings.HasPrefix(info.FullMethod, kbMethodPrefix) {
				return nil, errors.Forbidden(
					"method is not exposed for authorization",
					errors.WithID("auth.interceptor.unknown_method"),
				)
			}
			return handler(ctx, req)
		}

		session, err := manager.AuthorizeFromContext(ctx, objClass, action)
		if err != nil {
			return nil, errors.Unauthenticated(
				"unauthorized",
				errors.WithID("auth.interceptor.unauthorized"),
				errors.WithCause(err),
			)
		}

		if missing := checkLicenses(session, licenses); len(missing) > 0 {
			return nil, errors.Forbidden(
				"missing required licenses: "+strings.Join(missing, ", "),
				errors.WithID("auth.interceptor.license"),
			)
		}

		if !session.CheckObacAccess(objClass, action) {
			return nil, errors.Forbidden(
				"missing required permissions: "+objClass,
				errors.WithID("auth.interceptor.permission"),
			)
		}

		ctx = context.WithValue(ctx, SessionHeader, session)

		return handler(ctx, req)
	}
}

// objClassWithAction resolves the object class, additional licenses and access
// mode of a gRPC method from the generated kb.WebitelAPI map.
func objClassWithAction(fullMethod string) (string, []string, auth.AccessMode, bool) {
	serviceName, methodName := splitFullMethodName(fullMethod)

	service, ok := kb.WebitelAPI[serviceName]
	if !ok {
		return "", nil, auth.NONE, false
	}
	method, ok := service.WebitelMethods[methodName]
	if !ok {
		return "", nil, auth.NONE, false
	}

	var accessMode auth.AccessMode
	switch method.Access {
	case 0:
		accessMode = auth.Add
	case 1:
		accessMode = auth.Read
	case 2:
		accessMode = auth.Edit
	case 3:
		accessMode = auth.Delete
	}

	return service.ObjClass, service.AdditionalLicenses, accessMode, true
}

// checkLicenses reports the required licenses the session lacks.
func checkLicenses(session auth.Auther, licenses []string) []string {
	var missing []string
	for _, license := range licenses {
		if !session.CheckLicenseAccess(license) {
			missing = append(missing, license)
		}
	}
	return missing
}

// splitFullMethodName extracts service and method names from the full gRPC method name.
func splitFullMethodName(fullMethod string) (string, string) {
	fullMethod = strings.TrimPrefix(fullMethod, "/")
	if i := strings.Index(fullMethod, "/"); i >= 0 {
		return reg.ReplaceAllString(fullMethod[:i], ""), fullMethod[i+1:]
	}
	return "unknown", "unknown"
}
