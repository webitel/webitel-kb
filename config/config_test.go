package config

import (
	"strings"
	"testing"
	"time"

	"github.com/webitel/webitel-go-kit/appconfig"
)

func TestRelayConfigValidate(t *testing.T) {
	valid := RelayConfig{
		PollInterval:    time.Second,
		PublishTimeout:  5 * time.Second,
		Retention:       72 * time.Hour,
		CleanupInterval: 24 * time.Hour,
		CleanupBatch:    5000,
	}

	tests := []struct {
		name    string
		mutate  func(*RelayConfig)
		wantErr string
	}{
		{name: "defaults", mutate: func(*RelayConfig) {}},
		{name: "minimal cleanup batch", mutate: func(c *RelayConfig) { c.CleanupBatch = 1 }},
		{name: "maximal cleanup batch", mutate: func(c *RelayConfig) { c.CleanupBatch = 100000 }},
		{name: "zero poll interval", mutate: func(c *RelayConfig) { c.PollInterval = 0 }, wantErr: "relay.poll_interval"},
		{name: "negative poll interval", mutate: func(c *RelayConfig) { c.PollInterval = -time.Second }, wantErr: "relay.poll_interval"},
		{name: "zero publish timeout", mutate: func(c *RelayConfig) { c.PublishTimeout = 0 }, wantErr: "relay.publish_timeout"},
		{name: "zero retention", mutate: func(c *RelayConfig) { c.Retention = 0 }, wantErr: "relay.retention"},
		{name: "zero cleanup interval", mutate: func(c *RelayConfig) { c.CleanupInterval = 0 }, wantErr: "relay.cleanup_interval"},
		{name: "zero cleanup batch", mutate: func(c *RelayConfig) { c.CleanupBatch = 0 }, wantErr: "relay.cleanup_batch"},
		{name: "oversized cleanup batch", mutate: func(c *RelayConfig) { c.CleanupBatch = 100001 }, wantErr: "relay.cleanup_batch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			err := cfg.validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validate error = %v, want mention of %q", err, tt.wantErr)
			}
		})
	}
}

func TestInternalTokenValidation(t *testing.T) {
	valid := Config{
		Service:  ServiceConfig{Addr: "localhost:8080"},
		Postgres: appconfig.Postgres{DSN: "postgres://kb@localhost/kb"},
		Redis:    appconfig.Redis{Addr: "localhost:6379"},
		Consul:   appconfig.Consul{Addr: "localhost:8500"},
		Pubsub:   appconfig.Pubsub{URL: "amqp://localhost"},
		Relay: RelayConfig{
			PollInterval:    time.Second,
			PublishTimeout:  5 * time.Second,
			Retention:       72 * time.Hour,
			CleanupInterval: 24 * time.Hour,
			CleanupBatch:    5000,
		},
	}

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{name: "no token disables the api", token: ""},
		{name: "a token of the minimum length", token: strings.Repeat("a", 32)},
		{name: "a longer token", token: strings.Repeat("a", 64)},
		{name: "a short token is refused", token: "short", wantErr: true},
		{name: "one character below the minimum", token: strings.Repeat("a", 31), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			cfg.Service.Internal.Token = tt.token

			err := cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate error = %v, want error %v", err, tt.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), "service.internal.token") {
				t.Fatalf("error = %v, want it to name the setting", err)
			}
		})
	}
}
