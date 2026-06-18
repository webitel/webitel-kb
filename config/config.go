package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	Service  ServiceConfig  `mapstructure:"service"`
	Log      LogConfig      `mapstructure:"log"`
	Postgres PostgresConfig `mapstructure:"postgres"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Consul   ConsulConfig   `mapstructure:"consul"`
	Pubsub   PubsubConfig   `mapstructure:"pubsub"`
}

type ServiceConfig struct {
	Id         string           `mapstructure:"id"`
	Address    string           `mapstructure:"addr"`
	Connection ConnectionConfig `mapstructure:"conn"`
}

type ConnectionConfig struct {
	TLSConfig

	VerifyCerts bool      `mapstructure:"verify_certs"`
	Client      TLSConfig `mapstructure:"client"`
}

type TLSConfig struct {
	CA   string `mapstructure:"ca"`
	Cert string `mapstructure:"cert"`
	Key  string `mapstructure:"key"`
}

type LogConfig struct {
	Level   string `mapstructure:"level"`
	JSON    bool   `mapstructure:"json"`
	Otel    bool   `mapstructure:"otel"`
	File    string `mapstructure:"file"`
	Console bool   `mapstructure:"console"`
}

type PostgresConfig struct {
	DSN string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type ConsulConfig struct {
	Address       string `mapstructure:"addr"`
	PublicAddress string `mapstructure:"grpc_addr"`
}

type PubsubConfig struct {
	URL    string `mapstructure:"broker_url"`
	Driver string `mapstructure:"broker_driver"`
}

func LoadConfig() (*Config, error) {
	defineFlags()
	pflag.Parse()

	viper.AutomaticEnv()

	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		return nil, err
	}

	cfg := &Config{}

	configFile := viper.GetString("config_file")
	if configFile != "" {
		viper.SetConfigFile(configFile)
		if err := viper.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		viper.OnConfigChange(func(e fsnotify.Event) {
			log.Printf("Config file changed: %s", e.Name)

			newCfg := &Config{}
			if err := viper.Unmarshal(newCfg); err != nil {
				log.Printf("Reload error: unable to decode: %v", err)
				return
			}

			if err := newCfg.validate(); err != nil {
				log.Printf("Reload error: invalid config: %v", err)
				return
			}

			*cfg = *newCfg
			log.Println("Config reloaded successfully")
		})

		viper.WatchConfig()
	}

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %v", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func defineFlags() {
	pflag.String("config_file", "", "Configuration file (YAML, JSON, etc.)")

	pflag.String("service.id", "", "Service ID")
	pflag.String("service.addr", "localhost:8080", "Service address")

	pflag.String("log.level", "info", "Log level")
	pflag.Bool("log.json", false, "Log in JSON format")
	pflag.String("log.file", "", "Log file path")

	pflag.String("postgres.dsn", "", "Postgres DSN")
	pflag.String("redis.addr", "localhost:6379", "Redis address")
	pflag.String("consul.addr", "localhost:8500", "Consul address")
	pflag.String("pubsub.broker_url", "", "PubSub broker URL")

	defineConnectionFlags()
}

func (c *Config) validate() error {
	if c.Service.Id == "" {
		return fmt.Errorf("config: service.id is required (use --service.id or SERVICE_ID env)")
	}

	if c.Service.Address == "" {
		return fmt.Errorf("config: service.addr is required")
	}

	err := validateConnectionConfig(c.Service.Connection)
	if err != nil {
		return err
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}

	if c.Postgres.DSN == "" {
		return fmt.Errorf("config: postgres.dsn is required (use --postgres.dsn or DATA_SOURCE env)")
	}

	if c.Redis.Addr == "" {
		return fmt.Errorf("config: redis.addr is required")
	}

	if c.Consul.Address == "" {
		return fmt.Errorf("config: consul.addr is required")
	}

	if c.Pubsub.URL == "" {
		return fmt.Errorf("config: pubsub.broker_url is required (use --pubsub.broker_url or PUBSUB env)")
	}

	if !strings.HasPrefix(c.Pubsub.URL, "amqp://") && !strings.HasPrefix(c.Pubsub.URL, "amqps://") {
		return fmt.Errorf("config: pubsub.broker_url must start with amqp:// or amqps://")
	}

	return nil
}

func validateConnectionConfig(conn ConnectionConfig) error {
	if conn.VerifyCerts {
		if conn.CA == "" {
			return fmt.Errorf("config: service.conn.ca is required when verify_certs is true")
		}
		if conn.Cert == "" {
			return fmt.Errorf("config: service.conn.cert is required when verify_certs is true")
		}
		if conn.Key == "" {
			return fmt.Errorf("config: service.conn.key is required when verify_certs is true")
		}
	}
	return nil
}

func defineConnectionFlags() error {
	pflag.String("service.conn.verify_certs", "true", "Determine whether to verify certificates (false only for development)")
	pflag.String("service.conn.ca", "", "Server CA certificate path")
	pflag.String("service.conn.key", "", "Server certificate key path")
	pflag.String("service.conn.cert", "", "Server certificate path")
	pflag.String("service.conn.client.ca", "", "Client CA certificate path")
	pflag.String("service.conn.client.key", "", "Client certificate key path")
	pflag.String("service.conn.client.cert", "", "Client certificate path")
	return nil
}
