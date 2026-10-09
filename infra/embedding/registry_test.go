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
		{ProviderCohere, r.cohere, false},
		{ProviderOpenAI, r.openai, false},
		{ProviderBGEM3, r.endpoint, false},
		{ProviderE5, r.e5, false},
		{ProviderBGEReranker, r.endpoint, false},
		{ProviderBYOM, r.endpoint, false},
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

			m, ok := got.(measured)
			if !ok || m.Provider != tt.want || m.key != tt.provider {
				t.Errorf("provider %q mapped to wrong implementation", tt.provider)
			}
		})
	}
}
