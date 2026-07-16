package util

import (
	"slices"
	"testing"
)

func TestInlineFields(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"one per element", []string{"id", "name"}, []string{"id", "name"}},
		{"comma separated", []string{"id,name"}, []string{"id", "name"}},
		{"space separated", []string{"id name"}, []string{"id", "name"}},
		{"mixed separators", []string{"id, name created_at"}, []string{"id", "name", "created_at"}},
		{"lowercased", []string{"ID", "Name"}, []string{"id", "name"}},
		{"blanks dropped", []string{"", "  ", "id,,name"}, []string{"id", "name"}},
		{"duplicates kept", []string{"id", "id"}, []string{"id", "id"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InlineFields(tt.in); !slices.Equal(got, tt.want) {
				t.Fatalf("InlineFields(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeduplicateFields(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, []string{}},
		{"no duplicates", []string{"id", "name"}, []string{"id", "name"}},
		{"duplicates dropped keeping first-seen order", []string{"name", "id", "name"}, []string{"name", "id"}},
		{"all duplicates", []string{"id", "id", "id"}, []string{"id"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeduplicateFields(tt.in); !slices.Equal(got, tt.want) {
				t.Fatalf("DeduplicateFields(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
