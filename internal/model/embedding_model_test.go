package model

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/webitel/webitel-kb/migrations"
)

// The registration gate only holds while every supported size has its column
// in the schema, every vector column is a supported size and the model table
// checks the same sizes.
func TestEmbeddingDimensionsMatchTheSchema(t *testing.T) {
	files, err := migrations.EmbedMigrations.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	vectorColumn := regexp.MustCompile(`(\w+)\s+vector\((\d+)\)`)
	renamed := regexp.MustCompile(`RENAME COLUMN (\w+) TO (\w+)`)
	sizesChecked := regexp.MustCompile(`dimensions IN \(([\d, ]+)\)`)
	columns := make(map[string]int32)

	var checked []int32

	for _, file := range files {
		sql, err := migrations.EmbedMigrations.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}

		up, _, _ := strings.Cut(string(sql), "-- +goose Down")

		for _, match := range vectorColumn.FindAllStringSubmatch(up, -1) {
			size, err := strconv.Atoi(match[2])
			if err != nil {
				t.Fatalf("%s: vector size %q: %v", file.Name(), match[2], err)
			}

			columns[match[1]] = int32(size)
		}

		if match := sizesChecked.FindStringSubmatch(up); match != nil {
			checked = checked[:0]

			for _, size := range strings.Split(match[1], ",") {
				n, err := strconv.Atoi(strings.TrimSpace(size))
				if err != nil {
					t.Fatalf("%s: checked size %q: %v", file.Name(), size, err)
				}

				checked = append(checked, int32(n))
			}
		}

		for _, match := range renamed.FindAllStringSubmatch(up, -1) {
			if size, ok := columns[match[1]]; ok {
				delete(columns, match[1])
				columns[match[2]] = size
			}
		}
	}

	if !slices.Equal(checked, EmbeddingDimensions) {
		t.Errorf("kb.embedding_model checks dimensions IN %v, want %v", checked, EmbeddingDimensions)
	}

	for _, dims := range EmbeddingDimensions {
		if got, ok := columns[EmbeddingColumn(dims)]; !ok || got != dims {
			t.Errorf("no %s vector(%d) column in the migrations", EmbeddingColumn(dims), dims)
		}
	}

	for column, dims := range columns {
		if !StoresEmbeddingDimensions(dims) || column != EmbeddingColumn(dims) {
			t.Errorf("%s vector(%d) is not a supported size", column, dims)
		}
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

	resized := stored
	resized.Dimensions = 1024

	tests := []struct {
		name string
		in   *EmbeddingModel
		mask []string
		want EmbeddingModel
	}{
		{
			name: "empty mask takes every field",
			in:   &EmbeddingModel{Type: ModelTypeEmbedding, Name: "new"},
			want: EmbeddingModel{ID: 1, Type: ModelTypeEmbedding, Name: "new"},
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
			name: "masked dimensions are merged",
			in:   &EmbeddingModel{Dimensions: 1024},
			mask: []string{"dimensions"},
			want: resized,
		},
		{
			name: "api key is not merged",
			in:   &EmbeddingModel{},
			mask: []string{"api_key"},
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
