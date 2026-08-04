package util

import (
	"slices"
	"testing"
)

func TestSplitSort(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"bare name ascends", "name", []string{"+name"}},
		{"explicit ascending kept", "+name", []string{"+name"}},
		{"descending kept", "-name", []string{"-name"}},
		{"multiple criteria", "name,-created_at", []string{"+name", "-created_at"}},
		{"spaces trimmed", " name , -id ", []string{"+name", "-id"}},
		{"url-decoded plus becomes space", " name", []string{"+name"}},
		{"empty criteria dropped", ",,name,", []string{"+name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitSort(tt.in)

			if len(got) == 0 && len(tt.want) == 0 {
				return
			}

			if !slices.Equal(got, tt.want) {
				t.Fatalf("SplitSort(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
