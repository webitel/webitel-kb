package config

import (
	"strings"
	"testing"
	"time"
)

func TestRelayConfigValidate(t *testing.T) {
	valid := RelayConfig{Interval: time.Second, Batch: 100, PublishTimeout: 5 * time.Second}

	tests := []struct {
		name    string
		mutate  func(*RelayConfig)
		wantErr string
	}{
		{name: "defaults", mutate: func(*RelayConfig) {}},
		{name: "minimal batch", mutate: func(c *RelayConfig) { c.Batch = 1 }},
		{name: "maximal batch", mutate: func(c *RelayConfig) { c.Batch = 1000 }},
		{name: "zero interval", mutate: func(c *RelayConfig) { c.Interval = 0 }, wantErr: "relay.interval"},
		{name: "negative interval", mutate: func(c *RelayConfig) { c.Interval = -time.Second }, wantErr: "relay.interval"},
		{name: "zero batch", mutate: func(c *RelayConfig) { c.Batch = 0 }, wantErr: "relay.batch"},
		{name: "oversized batch", mutate: func(c *RelayConfig) { c.Batch = 1001 }, wantErr: "relay.batch"},
		{name: "zero publish timeout", mutate: func(c *RelayConfig) { c.PublishTimeout = 0 }, wantErr: "relay.publish_timeout"},
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
