package embedding

import (
	"slices"
	"testing"
)

func TestServiceURL(t *testing.T) {
	tests := []struct {
		name  string
		root  string
		route string
		want  string
	}{
		{name: "root", root: "http://tei:8080", route: "v1/embeddings", want: "http://tei:8080/v1/embeddings"},
		{name: "root with a slash", root: "http://tei:8080/", route: "v1/embeddings", want: "http://tei:8080/v1/embeddings"},
		{name: "root with the version", root: "http://tei:8080/v1", route: "v1/embeddings", want: "http://tei:8080/v1/embeddings"},
		{name: "root with the route", root: "http://tei:8080/v1/embeddings/", route: "v1/embeddings", want: "http://tei:8080/v1/embeddings"},
		{name: "proxy path is kept", root: "http://gw/models/e5", route: "v1/embeddings", want: "http://gw/models/e5/v1/embeddings"},
		{name: "rerank at the root", root: "http://llama:8080", route: "rerank", want: "http://llama:8080/rerank"},
		{name: "rerank under a version", root: "http://vllm:8000/v1", route: "rerank", want: "http://vllm:8000/v1/rerank"},
		{name: "rerank with the route", root: "http://vllm:8000/v1/rerank", route: "rerank", want: "http://vllm:8000/v1/rerank"},
		{name: "host named as the route", root: "http://rerank", route: "rerank", want: "http://rerank/rerank"},
		{name: "host named as the version", root: "http://v1", route: "v1/embeddings", want: "http://v1/v1/embeddings"},
		{name: "path that only ends like the version", root: "http://gw/xv1", route: "v1/embeddings", want: "http://gw/xv1/v1/embeddings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := serviceURL(tt.root, tt.route)
			if err != nil || got != tt.want {
				t.Errorf("serviceURL(%q, %q) = %q, %v; want %q", tt.root, tt.route, got, err, tt.want)
			}
		})
	}
}

func TestServiceURLRefusesABrokenRoot(t *testing.T) {
	if got, err := serviceURL("http://bad host:80", "rerank"); err == nil {
		t.Fatalf("serviceURL = %q, want an error", got)
	}
}

func TestByIndex(t *testing.T) {
	type item struct{ i int }

	index := func(it item) int { return it.i }

	tests := []struct {
		name    string
		items   []item
		inputs  int
		want    []item
		wantErr bool
	}{
		{name: "listed in any order", items: []item{{2}, {0}, {1}}, inputs: 3, want: []item{{0}, {1}, {2}}},
		{name: "no inputs", items: []item{}, inputs: 0, want: []item{}},
		{name: "input missing", items: []item{{0}}, inputs: 2, wantErr: true},
		{name: "extra answer", items: []item{{0}, {1}, {1}}, inputs: 2, wantErr: true},
		{name: "input answered twice", items: []item{{0}, {0}}, inputs: 2, wantErr: true},
		{name: "unknown input", items: []item{{0}, {2}}, inputs: 2, wantErr: true},
		{name: "negative input", items: []item{{-1}, {0}}, inputs: 2, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := byIndex(tt.items, tt.inputs, index)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}

				return
			}

			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("byIndex = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}
