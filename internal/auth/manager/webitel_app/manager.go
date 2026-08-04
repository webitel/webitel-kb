package webitel_app

import (
	"context"
	"net"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	authclient "buf.build/gen/go/webitel/webitel-go/grpc/go/_gogrpc"
	authmodel "buf.build/gen/go/webitel/webitel-go/protocolbuffers/go"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	session "github.com/webitel/webitel-kb/internal/auth/session/user_session"
)

var _ auth.Manager = &Manager{}

// Manager resolves caller sessions from the webitel-go auth service.
// Concurrent lookups of the same token are collapsed via singleflight.
type Manager struct {
	Client     authclient.AuthClient
	Group      singleflight.Group
	Connection *grpc.ClientConn
}

func New(conn *grpc.ClientConn) (*Manager, error) {
	return &Manager{Client: authclient.NewAuthClient(conn), Group: singleflight.Group{}, Connection: conn}, nil
}

func (i *Manager) AuthorizeFromContext(ctx context.Context, mainObjClassName string, mainAccessMode auth.AccessMode) (auth.Auther, error) {
	var (
		token []string
		info  metadata.MD
		ok    bool
	)

	v := ctx.Value(session.RequestContextName)
	info, ok = v.(metadata.MD)

	if !ok {
		info, ok = metadata.FromIncomingContext(ctx)
	}

	if !ok {
		return nil, errors.Unauthenticated(
			"metadata is empty; authorization token required",
			errors.WithID("auth.webitel_app.metadata.missing"),
		)
	}

	token = info.Get(session.AuthTokenName)

	if len(token) < 1 || token[0] == "" {
		return nil, errors.Unauthenticated(
			"authorization token is missing",
			errors.WithID("auth.webitel_app.token.missing"),
		)
	}

	newContext := metadata.NewOutgoingContext(ctx, info)
	userToken := token[0]

	sess, err, _ := i.Group.Do(userToken, func() (any, error) {
		return i.Client.UserInfo(newContext, nil)
	})
	if err != nil {
		return nil, errors.Unauthenticated(
			"unable to resolve user info",
			errors.WithID("auth.webitel_app.user_info"),
			errors.WithCause(err),
		)
	}

	userinfo, ok := sess.(*authmodel.Userinfo)
	if !ok {
		return nil, errors.Internal(
			"unexpected user info payload",
			errors.WithID("auth.webitel_app.user_info.type"),
		)
	}

	return ConstructSessionFromUserInfo(userinfo, mainObjClassName, mainAccessMode, getClientIP(ctx)), nil
}

func ConstructSessionFromUserInfo(userinfo *authmodel.Userinfo, mainObjClass string, mainAccess auth.AccessMode, ip string) *session.UserAuthSession {
	sess := &session.UserAuthSession{
		User: &session.User{
			ID:        userinfo.GetUserId(),
			Name:      userinfo.GetName(),
			Username:  userinfo.GetUsername(),
			Extension: userinfo.GetExtension(),
		},
		ExpiresAt:        userinfo.GetExpiresAt(),
		DomainID:         userinfo.GetDc(),
		Permissions:      make([]string, 0),
		License:          make(map[string]bool),
		Scopes:           make(map[string]*session.Scope),
		MainAccess:       mainAccess,
		MainObjClassName: mainObjClass,
		UserIP:           ip,
	}
	for _, lic := range userinfo.GetLicense() {
		sess.License[lic.GetId()] = lic.GetExpiresAt() > time.Now().UnixMilli()
	}

	for _, permission := range userinfo.GetPermissions() {
		switch auth.SuperPermission(permission.GetId()) {
		case auth.SuperCreatePermission:
			sess.SuperCreate = true
		case auth.SuperDeletePermission:
			sess.SuperDelete = true
		case auth.SuperEditPermission:
			sess.SuperEdit = true
		case auth.SuperSelectPermission:
			sess.SuperSelect = true
		}

		sess.Permissions = append(sess.Permissions, permission.GetId())
	}

	for _, scope := range userinfo.GetScope() {
		sess.Scopes[scope.GetClass()] = &session.Scope{
			ID:     scope.GetId(),
			Name:   scope.GetName(),
			Abac:   scope.GetAbac(),
			Obac:   scope.GetObac(),
			Rbac:   scope.GetRbac(),
			Class:  scope.GetClass(),
			Access: scope.GetAccess(),
		}
	}

	for i, role := range userinfo.GetRoles() {
		if i == 0 {
			sess.Roles = make([]*session.Role, 0)
		}

		sess.Roles = append(sess.Roles, &session.Role{
			ID:   role.GetId(),
			Name: role.GetName(),
		})
	}

	return sess
}

func getClientIP(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	// First try to get IP from headers
	ip := strings.Join(md.Get("x-real-ip"), ",")
	if ip == "" {
		ip = strings.Join(md.Get("x-forwarded-for"), ",")
	}

	// If no IP from headers, try to get from peer
	if ip == "" {
		if p, ok := peer.FromContext(ctx); ok {
			if addr, ok := p.Addr.(*net.TCPAddr); ok {
				ip = addr.IP.String()
			} else if addr, ok := p.Addr.(*net.UDPAddr); ok {
				ip = addr.IP.String()
			}
		}
	}

	return ip
}
