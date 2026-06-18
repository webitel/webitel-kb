package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"

	"buf.build/go/protovalidate"
	validatemiddleware "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	intrcp "github.com/webitel/webitel-go-kit/pkg/interceptors"

	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/infra/server/grpc/interceptors"
	infratls "github.com/webitel/webitel-kb/infra/tls"
	"github.com/webitel/webitel-kb/internal/model"
)

var Module = fx.Module("grpc_server",
	fx.Provide(
		fx.Annotate(
			ProvideServer,
		),
	),
)

func ProvideServer(conf *config.Config, logger *slog.Logger, tls *infratls.Config, lc fx.Lifecycle) (*Server, error) {
	srv, err := New(conf.Service.Address, func(c *Config) error {
		c.TLS = tls.Server.Clone()
		c.Logger = logger

		return nil
	})
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				logger.Info(fmt.Sprintf("listen grpc %s:%d", srv.Host(), srv.Port()))
				if err := srv.Listen(); err != nil {
					logger.Error("grpc server error", "err", err)
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := srv.Shutdown(); err != nil {
				logger.Error("error stopping grpc server", "err", err.Error())

				return err
			}

			return nil
		},
	})

	return srv, nil
}

type Server struct {
	*grpc.Server

	Addr     string
	host     string
	port     int
	log      *slog.Logger
	listener net.Listener
	health   *health.Server
}

type Config struct {
	TLS    *tls.Config
	Logger *slog.Logger
}

type Option func(*Config) error

// New provides a new gRPC server.
func New(addr string, opts ...Option) (*Server, error) {
	var (
		conf    Config
		grpcTLS credentials.TransportCredentials
	)
	for _, opt := range opts {
		if err := opt(&conf); err != nil {
			return nil, err
		}
	}

	log := conf.Logger
	if log == nil {
		log = slog.Default()
	}

	if addr == "" {
		addr = ":0"
	}

	if conf.TLS != nil {
		grpcTLS = credentials.NewTLS(conf.TLS)
	}

	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}

	s := grpc.NewServer(
		grpc.Creds(grpcTLS),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			intrcp.UnaryServerErrorInterceptor(),
			interceptors.NewUnaryAuthInterceptor(),
			validatemiddleware.UnaryServerInterceptor(validator),
		),
	)

	healthSrv := health.NewServer()
	healthgrpc.RegisterHealthServer(s, healthSrv)
	healthSrv.SetServingStatus("", healthgrpc.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus(model.ServiceName, healthgrpc.HealthCheckResponse_SERVING)

	// Server reflection — lets grpcurl and clients discover services at runtime.
	reflection.Register(s)

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	h, p, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(p)

	if h == "::" {
		h = publicAddr()
	}

	return &Server{
		Addr:     addr,
		Server:   s,
		log:      log,
		host:     h,
		port:     port,
		listener: l,
		health:   healthSrv,
	}, nil
}

func (s *Server) Listen() error {
	return s.Serve(s.listener)
}

func (s *Server) Shutdown() error {
	s.log.Debug("receive shutdown grpc")
	if s.health != nil {
		// Flip health to NOT_SERVING so load balancers drain us before we stop.
		s.health.Shutdown()
	}

	s.GracefulStop()

	return nil
}

func (s *Server) Host() string {
	if e, ok := os.LookupEnv("PROXY_GRPC_HOST"); ok {
		return e
	}

	return s.host
}

func (s *Server) Port() int {
	return s.port
}

func publicAddr() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, i := range interfaces {
		addresses, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addresses {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			if isPublicIP(ip) {
				return ip.String()
			}
			// process IP address
		}
	}

	return ""
}

func isPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}

	return true
}
