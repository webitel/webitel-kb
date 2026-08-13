package etag

import (
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
	kitetag "github.com/webitel/webitel-go-kit/pkg/etag"
)

// The type code is part of the wire format of every article token: a registry
// renumbering would silently invalidate the tokens already handed out.
func TestTypeArticleCodeIsPinned(t *testing.T) {
	if TypeArticle != 6 {
		t.Fatalf("article etag type = %d, want 6: the go-kit registry moved and existing tokens break", TypeArticle)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	token, err := Encode(TypeArticle, 42, 7)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	id, ver, err := Parse(TypeArticle, token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if id != 42 || ver != 7 {
		t.Fatalf("Parse = (%d, %d), want (42, 7)", id, ver)
	}
}

func TestEncodeRejectsInvalidID(t *testing.T) {
	tests := []struct {
		name string
		id   int64
	}{
		{name: "zero", id: 0},
		{name: "negative", id: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Encode(TypeArticle, tt.id, 1); err == nil {
				t.Fatal("Encode accepted an invalid id")
			}
		})
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	foreign, err := kitetag.EncodeEtag(kitetag.EtagCase, 42, 7)
	if err != nil {
		t.Fatalf("encode foreign token: %v", err)
	}

	tests := []struct {
		name  string
		input string
	}{
		{name: "bare id", input: "42"},
		{name: "empty", input: ""},
		{name: "garbage", input: "abc"},
		{name: "foreign type", input: foreign},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse(TypeArticle, tt.input)
			if errors.Code(err) != codes.InvalidArgument {
				t.Fatalf("error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestParseLocator(t *testing.T) {
	token, err := Encode(TypeArticle, 42, 7)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	foreign, err := kitetag.EncodeEtag(kitetag.EtagCase, 42, 7)
	if err != nil {
		t.Fatalf("encode foreign token: %v", err)
	}

	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "full token", input: token, want: 42},
		{name: "bare id", input: "42", want: 42},
		{name: "zero id", input: "0", wantErr: true},
		{name: "negative id", input: "-5", wantErr: true},
		{name: "garbage", input: "abc", wantErr: true},
		{name: "empty", input: "", wantErr: true},
		{name: "foreign type", input: foreign, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ParseLocator(TypeArticle, tt.input)

			if tt.wantErr {
				if errors.Code(err) != codes.InvalidArgument {
					t.Fatalf("error = %v, want InvalidArgument", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseLocator: %v", err)
			}

			if id != tt.want {
				t.Fatalf("id = %d, want %d", id, tt.want)
			}
		})
	}
}
