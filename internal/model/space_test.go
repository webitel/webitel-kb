package model

import "testing"

func TestSupportedChunkingStrategy(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"supported", "recursive_markdown", "recursive_markdown"},
		{"empty", "", DefaultChunkingStrategy},
		{"unknown", "semantic", DefaultChunkingStrategy},
		{"other case", "Recursive_Markdown", DefaultChunkingStrategy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SupportedChunkingStrategy(tt.in); got != tt.want {
				t.Fatalf("SupportedChunkingStrategy(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
