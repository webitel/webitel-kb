package model

import "testing"

func TestCanonicalLanguage(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"lower code", "uk", "uk", true},
		{"upper code", "RU", "ru", true},
		{"region", "pt-br", "pt-BR", true},
		{"script", "zh-Hans", "zh-Hans", true},
		{"empty", "", "", false},
		{"undetermined", "und", "", false},
		{"english name", "Ukrainian", "", false},
		{"garbage", "!!", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CanonicalLanguage(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("CanonicalLanguage(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestTextSearchDictionary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"russian stems", "ru", "russian"},
		{"region keeps the base stemmer", "pt-BR", "portuguese"},
		{"spanish of latin america", "es-419", "spanish"},
		{"bokmal is norwegian", "nb", "norwegian"},
		{"ukrainian has no stemmer", "uk", TextSearchFallback},
		{"vietnamese has no stemmer", "vi", TextSearchFallback},
		{"chinese has no word boundaries", "zh-Hans", TextSearchFallback},
		{"unparsable falls back", "Ukrainian", TextSearchFallback},
		{"empty falls back", "", TextSearchFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TextSearchDictionary(tt.in); got != tt.want {
				t.Fatalf("TextSearchDictionary(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
