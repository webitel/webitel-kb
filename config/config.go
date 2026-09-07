package config

import (
	"log/slog"
	"strings"
	"time"

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
	Relay    RelayConfig        `mapstructure:"relay"`
}

type ServiceConfig struct {
	Addr       string             `mapstructure:"addr"`
	Internal   InternalAPIConfig  `mapstructure:"internal"`
	Connection appconfig.GRPCConn `mapstructure:"conn"`
}

// InternalAPIConfig gates the service-to-service API for the indexer worker.
type InternalAPIConfig struct {
	// Token every internal caller must present. Empty disables the API.
	Token string `mapstructure:"token"`
}

// minServiceTokenLen is the shortest accepted service token.
const minServiceTokenLen = 32

// RelayConfig tunes the outbox relay.
type RelayConfig struct {
	PollInterval    time.Duration `mapstructure:"poll_interval"`
	PublishTimeout  time.Duration `mapstructure:"publish_timeout"`
	Retention       time.Duration `mapstructure:"retention"`
	CleanupInterval time.Duration `mapstructure:"cleanup_interval"`
	CleanupBatch    int           `mapstructure:"cleanup_batch"`
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

// LoadMigrateConfig loads the minimal configuration required by the migrate command.
func LoadMigrateConfig() (*Config, error) {
	loader := appconfig.NewLoader(appconfig.Sections{
		Log:      true,
		Postgres: true,
	})
	loader.RegisterFlags(pflag.CommandLine)
	pflag.Parse()

	cfg := &Config{}
	if err := loader.Load(pflag.CommandLine, cfg); err != nil {
		return nil, err
	}

	if cfg.Postgres.DSN == "" {
		return nil, errors.New("config: postgres.dsn is required (use --postgres.dsn or POSTGRES_DSN env)")
	}

	return cfg, nil
}

func registerServiceFlags() {
	pflag.String("service.addr", "localhost:8080", "gRPC listen address")
	pflag.String("service.internal.token", "", "service token of the internal indexer API; prefer SERVICE_INTERNAL_TOKEN over the flag (empty disables the API)")
	appconfig.RegisterGRPCConnFlags(pflag.CommandLine, "service.conn", true)
	pflag.Duration("relay.poll_interval", time.Second, "outbox relay poll interval")
	pflag.Duration("relay.publish_timeout", 5*time.Second, "outbox relay publish confirmation timeout")
	pflag.Duration("relay.retention", 72*time.Hour, "how long a relayed outbox row is kept")
	pflag.Duration("relay.cleanup_interval", 24*time.Hour, "how often relayed outbox rows are removed")
	pflag.Int("relay.cleanup_batch", 5000, "how many outbox rows one cleanup statement removes")
}

func (c *Config) validate() error {
	if c.Service.Addr == "" {
		return errors.New("config: service.addr is required")
	}

	if err := appconfig.ValidateGRPCConn("service.conn", c.Service.Connection); err != nil {
		return err
	}

	if token := c.Service.Internal.Token; token != "" && len(token) < minServiceTokenLen {
		return errors.New("config: service.internal.token must be at least 32 characters")
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

	return c.Relay.validate()
}

func (c RelayConfig) validate() error {
	if c.PollInterval <= 0 {
		return errors.New("config: relay.poll_interval must be positive")
	}

	if c.PublishTimeout <= 0 {
		return errors.New("config: relay.publish_timeout must be positive")
	}

	if c.Retention <= 0 {
		return errors.New("config: relay.retention must be positive")
	}

	if c.CleanupInterval <= 0 {
		return errors.New("config: relay.cleanup_interval must be positive")
	}

	if c.CleanupBatch < 1 || c.CleanupBatch > 100000 {
		return errors.New("config: relay.cleanup_batch must be within [1, 100000]")
	}

	return nil
}
