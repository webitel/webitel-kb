package event

import (
	"strings"
	"testing"
	"time"
)

func validEnvelope() ArticleReindex {
	return NewArticleReindex(
		time.Date(2026, 7, 27, 10, 30, 0, 0, time.UTC),
		7, 19, 3, 1,
	)
}

func TestMarshalGolden(t *testing.T) {
	// Pins the producer's serialization: field names, value rendering and
	// determinism. The jsonb outbox column may reorder keys downstream, so
	// this guards the producer output, not the exact wire bytes.
	const golden = `{"type":"article.reindex","schema":1,` +
		`"occurred_at":"2026-07-27T10:30:00Z",` +
		`"article_id":7,"version_id":19,"space_id":3,"domain_id":1}`

	got, err := validEnvelope().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(got) != golden {
		t.Fatalf("wire JSON drifted:\ngot  %s\nwant %s", got, golden)
	}
}

func TestMarshalNormalizesTimezone(t *testing.T) {
	kyiv := time.FixedZone("EEST", 3*60*60)
	local := NewArticleReindex(time.Date(2026, 7, 27, 13, 30, 0, 0, kyiv), 7, 19, 3, 1)

	localJSON, err := local.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	utcJSON, err := validEnvelope().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(localJSON) != string(utcJSON) {
		t.Fatalf("equal instants rendered differently:\n%s\n%s", localJSON, utcJSON)
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	want := validEnvelope()

	data, err := want.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := UnmarshalArticleReindex(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ArticleReindex)
		wantErr string
	}{
		{name: "valid", mutate: func(*ArticleReindex) {}},
		{name: "wrong type", mutate: func(e *ArticleReindex) { e.Type = "article.deleted" }, wantErr: "type"},
		{name: "empty type", mutate: func(e *ArticleReindex) { e.Type = "" }, wantErr: "type"},
		{name: "zero schema", mutate: func(e *ArticleReindex) { e.Schema = 0 }, wantErr: "schema"},
		{name: "zero time", mutate: func(e *ArticleReindex) { e.OccurredAt = time.Time{} }, wantErr: "occurred_at"},
		{name: "zero article", mutate: func(e *ArticleReindex) { e.ArticleID = 0 }, wantErr: "article_id"},
		{name: "negative version", mutate: func(e *ArticleReindex) { e.VersionID = -5 }, wantErr: "version_id"},
		{name: "zero space", mutate: func(e *ArticleReindex) { e.SpaceID = 0 }, wantErr: "space_id"},
		{name: "zero domain", mutate: func(e *ArticleReindex) { e.DomainID = 0 }, wantErr: "domain_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEnvelope()
			tt.mutate(&e)

			err := e.Validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate error = %v, want mention of %q", err, tt.wantErr)
			}
		})
	}
}

func TestUnmarshalRejects(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "broken json", data: `{"type":`},
		{name: "empty object", data: `{}`},
		{name: "wrong type value", data: `{"type":"other","schema":1,"occurred_at":"2026-07-27T10:30:00Z","article_id":7,"version_id":19,"space_id":3,"domain_id":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UnmarshalArticleReindex([]byte(tt.data)); err == nil {
				t.Fatal("Unmarshal accepted an invalid envelope")
			}
		})
	}
}

func TestUnmarshalIgnoresUnknownFields(t *testing.T) {
	// Forward compatibility: schema additions must not break older readers.
	data := `{"type":"article.reindex","schema":1,"occurred_at":"2026-07-27T10:30:00Z",` +
		`"article_id":7,"version_id":19,"space_id":3,"domain_id":1,"brand_new_field":"x"}`

	if _, err := UnmarshalArticleReindex([]byte(data)); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}
