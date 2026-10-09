package embedding

import (
	"context"
	"testing"
)

func TestEndpointRequiresURL(t *testing.T) {
	e := NewEndpoint()
	if _, err := e.Embed(context.Background(), EmbedRequest{Texts: []string{"x"}}); err == nil {
		t.Error("expected error for missing endpoint url")
	}

	if _, err := e.Rerank(context.Background(), RerankRequest{Documents: []string{"x"}}); err == nil {
		t.Error("expected error for missing endpoint url")
	}
}
