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
