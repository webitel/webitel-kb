package model

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/webitel/webitel-kb/migrations"
)

// The registration gate only holds while the constant equals the vector size
// the schema actually stores.
func TestEmbeddingStorageDimensionsMatchTheSchema(t *testing.T) {
	files, err := migrations.EmbedMigrations.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	vectorColumn := regexp.MustCompile(`vector\((\d+)\)`)
	found := 0

	for _, file := range files {
		sql, err := migrations.EmbedMigrations.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}

		for _, match := range vectorColumn.FindAllStringSubmatch(string(sql), -1) {
			size, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatalf("%s: vector size %q: %v", file.Name(), match[1], err)
			}

			if int32(size) != EmbeddingStorageDimensions {
				t.Fatalf("%s declares vector(%d); EmbeddingStorageDimensions is %d",
					file.Name(), size, EmbeddingStorageDimensions)
			}

			found++
		}
	}

	if found == 0 {
		t.Fatal("no vector column found in the migrations; the constant pins nothing")
	}
}

func TestEmbeddingModelMerge(t *testing.T) {
	stored := EmbeddingModel{
		ID: 1, Type: ModelTypeEmbedding, Name: "gemini", Provider: "gemini",
		ModelRef: "gemini-embedding-001", Dimensions: 768, Endpoint: "https://x",
	}

	renamed := stored
	renamed.Name = "new"

	noEndpoint := stored
	noEndpoint.Endpoint = ""

	tests := []struct {
		name string
		in   *EmbeddingModel
		mask []string
		want EmbeddingModel
	}{
		{
			name: "empty mask takes every field",
			in:   &EmbeddingModel{Type: ModelTypeEmbedding, Name: "new"},
			want: EmbeddingModel{ID: 1, Type: ModelTypeEmbedding, Name: "new", Dimensions: 768},
		},
		{
			name: "mask takes only its fields",
			in:   &EmbeddingModel{Name: "new", ModelRef: "ignored"},
			mask: []string{"name"},
			want: renamed,
		},
		{
			name: "masked empty endpoint clears",
			in:   &EmbeddingModel{},
			mask: []string{"endpoint"},
			want: noEndpoint,
		},
		{
			name: "api key and dimensions are not merged",
			in:   &EmbeddingModel{Dimensions: 1024},
			mask: []string{"api_key", "dimensions"},
			want: stored,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stored.Merge(tt.in, tt.mask); *got != tt.want {
				t.Fatalf("merged = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
