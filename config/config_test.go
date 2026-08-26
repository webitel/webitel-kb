package config

import (
	"strings"
	"testing"
	"time"
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
