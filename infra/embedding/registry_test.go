package embedding

import (
	"errors"
	"testing"
)

func TestRegistryForModel(t *testing.T) {
	r := NewRegistry()

	tests := []struct {
		provider string
		want     Provider
		wantErr  bool
	}{
		{ProviderGemini, r.gemini, false},
		{ProviderBGEM3, r.endpoint, false},
		{ProviderE5, r.endpoint, false},
		{ProviderBGEReranker, r.endpoint, false},
		{ProviderBYOM, r.endpoint, false},
		{ProviderOpenAI, nil, true},
		{ProviderCohere, nil, true},
		{ProviderAzure, nil, true},
		{"unknown", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got, err := r.ForModel(tt.provider)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("want ErrUnsupported, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("provider %q mapped to wrong implementation", tt.provider)
			}
		})
	}
}
