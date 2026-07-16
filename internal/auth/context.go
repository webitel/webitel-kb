package auth

import (
	"context"
	"reflect"
)

// sessionKey is the private context key the authorized caller session is stored
// under.
type sessionKey struct{}

// WithSession returns a copy of ctx carrying the authorized caller session.
func WithSession(ctx context.Context, session Auther) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// FromContext returns the authorized caller session carried by ctx. The second
// result reports whether a usable session was present: callers must handle its
// absence rather than assume authorization already happened.
func FromContext(ctx context.Context) (Auther, bool) {
	session, ok := ctx.Value(sessionKey{}).(Auther)
	if !ok || isNilSession(session) {
		return nil, false
	}

	return session, true
}

// isNilSession reports whether session is nil or an interface wrapping a nil
// pointer. The latter compares unequal to nil yet panics on first method call,
// so it is caught here instead of in every caller. Sessions are implemented on
// pointer types, so a nil pointer is the only wrapped-nil shape to look for.
func isNilSession(session Auther) bool {
	if session == nil {
		return true
	}

	v := reflect.ValueOf(session)

	return v.Kind() == reflect.Pointer && v.IsNil()
}
