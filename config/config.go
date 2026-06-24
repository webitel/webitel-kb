package config

import (
	"log/slog"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/pflag"

	"github.com/webitel/webitel-go-kit/appconfig"
	"github.com/webitel/webitel-go-kit/pkg/errors"
)

type Config struct {
	Service  ServiceConfig      `mapstructure:"service"`
	Log      appconfig.Log      `mapstructure:"log"`
	Postgres appconfig.Postgres `mapstructure:"postgres"`
	Redis    appconfig.Redis    `mapstructure:"redis"`
	Consul   appconfig.Consul   `mapstructure:"consul"`
	Pubsub   appconfig.Pubsub   `mapstructure:"pubsub"`
}

type ServiceConfig struct {
	Addr       string             `mapstructure:"addr"`
	Connection appconfig.GRPCConn `mapstructure:"conn"`
}

// LoadServerConfig loads the full configuration required by the gRPC server.
func LoadServerConfig() (*Config, error) {
	loader := appconfig.NewLoader(appconfig.Sections{
		Log:      true,
		Postgres: true,
		Redis:    true,
		Consul:   true,
		Pubsub:   true,
	})
	loader.RegisterFlags(pflag.CommandLine)
	registerServiceFlags()
	pflag.Parse()

	cfg := &Config{}
	if err := loader.Load(pflag.CommandLine, cfg); err != nil {
		return nil, err
	}

	loader.Watch(func(e fsnotify.Event) {
		slog.Info("config file changed", "name", e.Name)

		newCfg := &Config{}
		if err := loader.Viper().Unmarshal(newCfg); err != nil {
			slog.Error("config reload: unmarshal failed", "error", err)

			return
		}

		if err := newCfg.validate(); err != nil {
			slog.Error("config reload: validation failed", "error", err)

			return
		}

		*cfg = *newCfg

		slog.Info("config reloaded")
	})

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func registerServiceFlags() {
	pflag.String("service.addr", "localhost:8080", "gRPC listen address")
	appconfig.RegisterGRPCConnFlags(pflag.CommandLine, "service.conn", true)
}

func (c *Config) validate() error {
	if c.Service.Addr == "" {
		return errors.New("config: service.addr is required")
	}

	if err := appconfig.ValidateGRPCConn("service.conn", c.Service.Connection); err != nil {
		return err
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}

	if c.Postgres.DSN == "" {
		return errors.New("config: postgres.dsn is required (use --postgres.dsn or POSTGRES_DSN env)")
	}

	if c.Redis.Addr == "" {
		return errors.New("config: redis.addr is required")
	}

	if c.Consul.Addr == "" {
		return errors.New("config: consul.addr is required")
	}

	if c.Pubsub.URL == "" {
		return errors.New("config: pubsub.url is required (use --pubsub.url or PUBSUB_URL env)")
	}

	if !strings.HasPrefix(c.Pubsub.URL, "amqp://") && !strings.HasPrefix(c.Pubsub.URL, "amqps://") {
		return errors.New("config: pubsub.url must start with amqp:// or amqps://")
	}

	return nil
}
