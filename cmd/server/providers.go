package server

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
	"go.uber.org/fx"

	"github.com/webitel/webitel-go-kit/infra/discovery"
	otelsdk "github.com/webitel/webitel-go-kit/infra/otel/sdk"
	"github.com/webitel/webitel-kb/config"
	"github.com/webitel/webitel-kb/internal/model"

	_ "github.com/webitel/webitel-go-kit/infra/discovery/consul"
)

func ProvideLogger(cfg *config.Config, lc fx.Lifecycle) (*slog.Logger, error) {
	logSettings := cfg.Log

	if !logSettings.Console && !logSettings.Otel && logSettings.File == "" {
		logSettings.Console = true
	}

	level := parseLevel(logSettings.Level)
	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handlers []slog.Handler

	if logSettings.Console {
		var h slog.Handler
		if logSettings.JSON {
			h = slog.NewJSONHandler(os.Stdout, opts)
		} else {
			h = slog.NewTextHandler(os.Stdout, opts)
		}
		handlers = append(handlers, h)
	}

	// File Handler
	if logSettings.File != "" {
		f, err := os.OpenFile(logSettings.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}

		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return f.Close()
			},
		})

		var h slog.Handler
		if logSettings.JSON {
			h = slog.NewJSONHandler(f, opts)
		} else {
			h = slog.NewTextHandler(f, opts)
		}
		handlers = append(handlers, h)
	}

	if logSettings.Otel {
		service := resource.NewSchemaless(
			semconv.ServiceName(model.ServiceName),
			semconv.ServiceVersion(model.Version),
			semconv.ServiceInstanceID(discovery.GenerateInstanceID(model.ServiceName)),
			semconv.ServiceNamespace(model.ServiceNamespace),
		)
		otelHandler := otelslog.NewHandler("slog")

		shutdown, err := otelsdk.Configure(context.Background(), otelsdk.WithResource(service),
			otelsdk.WithLogBridge(
				func() {
					handlers = append(handlers, otelHandler)
				},
			),
		)
		if err != nil {
			return nil, err
		}

		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return shutdown(ctx)
			},
		})
	}

	var finalHandler slog.Handler
	if len(handlers) == 0 {
		finalHandler = slog.NewTextHandler(os.Stdout, opts)
	} else if len(handlers) == 1 {
		finalHandler = handlers[0]
	} else {
		finalHandler = MultiHandler(handlers...)
	}

	logger := slog.New(finalHandler)
	slog.SetDefault(logger)

	return logger, nil
}

func parseLevel(lvl string) slog.Level {
	switch lvl {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type multiHandler struct {
	handlers []slog.Handler
}

func MultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, hh := range h.handlers {
		if hh.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, hh := range h.handlers {
		if hh.Enabled(ctx, r.Level) {
			_ = hh.Handle(ctx, r)
		}
	}

	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, hh := range h.handlers {
		newHandlers[i] = hh.WithAttrs(attrs)
	}

	return &multiHandler{handlers: newHandlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, hh := range h.handlers {
		newHandlers[i] = hh.WithGroup(name)
	}

	return &multiHandler{handlers: newHandlers}
}

func ProvideSD(cfg *config.Config, log *slog.Logger, lc fx.Lifecycle) (discovery.DiscoveryProvider, error) {
	provider, err := discovery.DefaultFactory.CreateProvider(
		discovery.ProviderConsul,
		log,
		cfg.Consul.Addr,
		discovery.WithHeartbeat[discovery.DiscoveryProvider](true),
		discovery.WithTimeout[discovery.DiscoveryProvider](time.Second*30),
	)
	if err != nil {
		return nil, err
	}

	si := new(discovery.ServiceInstance)
	{
		si.Id = discovery.GenerateInstanceID(model.ServiceName)
		si.Name = model.ServiceName
		si.Version = model.Version
		si.Metadata = map[string]string{
			"commit":         model.Commit,
			"commitDate":     model.CommitDate,
			"branch":         model.Branch,
			"buildTimestamp": model.BuildTimestamp,
		}
		si.Endpoints = []string{(&url.URL{Scheme: "grpc", Host: cfg.Service.Addr}).String()}
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := provider.Register(ctx, si); err != nil {
				return err
			}

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := provider.Deregister(ctx, si); err != nil {
				return err
			}

			return nil
		},
	})

	return provider, nil
}
