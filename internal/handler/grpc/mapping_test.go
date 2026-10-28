package grpc

import (
	"testing"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/model"
)

func TestTagMatchFromProto(t *testing.T) {
	tests := []struct {
		name string
		in   kb.TagMatch
		want model.TagMatch
	}{
		{"unspecified is left to the domain", kb.TagMatch_TAG_MATCH_UNSPECIFIED, model.TagMatchDefault},
		{"all", kb.TagMatch_TAG_MATCH_ALL, model.TagMatchAll},
		{"any", kb.TagMatch_TAG_MATCH_ANY, model.TagMatchAny},
		{"unknown is left to the domain", kb.TagMatch(42), model.TagMatchDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tagMatchFromProto(tt.in); got != tt.want {
				t.Fatalf("tagMatchFromProto(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
