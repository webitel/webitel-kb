package util

import (
	"slices"
	"testing"
)

func TestResolvePaging(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		items    []int
		want     []int
		wantNext bool
	}{
		{"fewer than size", 5, []int{1, 2}, []int{1, 2}, false},
		{"exactly size", 2, []int{1, 2}, []int{1, 2}, false},
		{"lookahead row trimmed and next reported", 2, []int{1, 2, 3}, []int{1, 2}, true},
		{"unlimited passes through", -1, []int{1, 2, 3}, []int{1, 2, 3}, false},
		{"zero size passes through", 0, []int{1, 2, 3}, []int{1, 2, 3}, false},
		{"empty", 5, nil, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, next := ResolvePaging(tt.size, tt.items)
			if !slices.Equal(got, tt.want) {
				t.Errorf("items = %v, want %v", got, tt.want)
			}

			if next != tt.wantNext {
				t.Errorf("next = %v, want %v", next, tt.wantNext)
			}
		})
	}
}
