package embedding

import (
	"context"
	"slices"
	"testing"
)

func TestE5MarksTheTask(t *testing.T) {
	tests := []struct {
		name string
		task TaskType
		want []string
	}{
		{name: "query", task: TaskQuery, want: []string{"query: як повернути товар"}},
		{name: "document", task: TaskDocument, want: []string{"passage: як повернути товар"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []embedCall

			srv := embedServer(t, &calls)

			e5, err := NewRegistry().ForModel(ProviderE5)
			if err != nil {
				t.Fatal(err)
			}

			texts := []string{"як повернути товар"}

			if _, err := e5.Embed(context.Background(), EmbedRequest{
				ModelRef: "intfloat/multilingual-e5-base", Endpoint: srv.URL, Task: tt.task, Texts: texts,
			}); err != nil {
				t.Fatalf("Embed: %v", err)
			}

			if !slices.Equal(calls[0].body.Input, tt.want) {
				t.Errorf("input = %q, want %q", calls[0].body.Input, tt.want)
			}

			if texts[0] != "як повернути товар" {
				t.Errorf("caller's texts changed: %q", texts)
			}
		})
	}
}

func TestOtherSelfHostedModelsKeepTheText(t *testing.T) {
	for _, provider := range []string{ProviderBGEM3, ProviderBYOM} {
		t.Run(provider, func(t *testing.T) {
			var calls []embedCall

			srv := embedServer(t, &calls)

			p, err := NewRegistry().ForModel(provider)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := p.Embed(context.Background(), EmbedRequest{
				ModelRef: "BAAI/bge-m3", Endpoint: srv.URL, Task: TaskQuery, Texts: []string{"текст"},
			}); err != nil {
				t.Fatalf("Embed: %v", err)
			}

			if !slices.Equal(calls[0].body.Input, []string{"текст"}) {
				t.Errorf("input = %q, want the text as is", calls[0].body.Input)
			}
		})
	}
}
