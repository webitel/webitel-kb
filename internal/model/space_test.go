package model

import "testing"

func TestChunkingStrategySupported(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"supported", "recursive_markdown", true},
		{"empty", "", false},
		{"unknown", "semantic", false},
		{"other case", "Recursive_Markdown", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChunkingStrategySupported(tt.in); got != tt.want {
				t.Fatalf("ChunkingStrategySupported(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
