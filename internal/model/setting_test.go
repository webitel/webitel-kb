package model

import "testing"

func TestParseTagsFiltrationMode(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   TagMatch
		wantOK bool
	}{
		{"and", `"AND"`, TagMatchAll, true},
		{"or", `"OR"`, TagMatchAny, true},
		{"lower case", `"and"`, TagMatchDefault, false},
		{"empty string", `""`, TagMatchDefault, false},
		{"number", `1`, TagMatchDefault, false},
		{"null", `null`, TagMatchDefault, false},
		{"object", `{"mode":"AND"}`, TagMatchDefault, false},
		{"not json", `AND`, TagMatchDefault, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseTagsFiltrationMode([]byte(tt.raw))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ParseTagsFiltrationMode(%s) = %v, %v, want %v, %v", tt.raw, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestParseVersionLimit(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   int
		wantOK bool
	}{
		{"one", `1`, 1, true},
		{"ten", `10`, 10, true},
		{"zero", `0`, 0, false},
		{"negative", `-3`, 0, false},
		{"fraction", `2.5`, 0, false},
		{"string", `"10"`, 0, false},
		{"null", `null`, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseVersionLimit([]byte(tt.raw))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ParseVersionLimit(%s) = %d, %v, want %d, %v", tt.raw, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
