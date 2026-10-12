package embedding

import (
	"context"
	"errors"
	"testing"
)

func TestAzureRequiresItsResource(t *testing.T) {
	_, err := NewAzure().Embed(context.Background(), EmbedRequest{
		ModelRef: "text-embedding-3-small", APIKey: "k", Texts: []string{"a"},
	})
	if err == nil {
		t.Fatal("want an error without the resource url")
	}
}

func TestAzureRerankUnsupported(t *testing.T) {
	_, err := NewAzure().Rerank(context.Background(), RerankRequest{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}
