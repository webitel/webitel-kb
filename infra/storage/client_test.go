package storage

import (
	"context"
	stderrors "errors"
	"testing"

	"google.golang.org/grpc"

	storagepb "github.com/webitel/webitel-kb/api/storage"
)

// fakeFileAPI answers the bulk link call only; the embedded nil interface
// makes every other method a loud failure.
type fakeFileAPI struct {
	storagepb.FileServiceClient

	got      *storagepb.BulkGenerateFileLinkRequest
	deadline bool
	resp     *storagepb.BulkGenerateFileLinkResponse
	err      error
}

func (f *fakeFileAPI) BulkGenerateFileLink(
	ctx context.Context, in *storagepb.BulkGenerateFileLinkRequest, _ ...grpc.CallOption,
) (*storagepb.BulkGenerateFileLinkResponse, error) {
	f.got = in
	_, f.deadline = ctx.Deadline()

	return f.resp, f.err
}

func TestDownloadSignsEveryFileOfTheDomain(t *testing.T) {
	api := &fakeFileAPI{resp: &storagepb.BulkGenerateFileLinkResponse{
		Links: []*storagepb.GenerateFileLinkResponse{
			{Url: "/any/file/4/download?domain_id=5&expires=1&signature=a", BaseUrl: "https://kb.example/"},
			nil,
			{Url: "", BaseUrl: "https://kb.example/"},
		},
	}}

	links, err := New(api).Download(context.Background(), 5, []int64{4, 6, 8})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if len(api.got.GetFiles()) != 3 {
		t.Fatalf("request carries %d files, want 3", len(api.got.GetFiles()))
	}

	for i, id := range []int64{4, 6, 8} {
		file := api.got.GetFiles()[i]
		if file.GetFileId() != id || file.GetDomainId() != 5 || file.GetAction() != actionDownload {
			t.Fatalf("file[%d] = %+v, want id %d of domain 5 for download", i, file, id)
		}
	}

	if !api.deadline {
		t.Fatal("the call ran without a deadline")
	}

	// The unsigned positions are absent, the signed one is joined to the base.
	if len(links) != 1 || links[4] != "https://kb.example/any/file/4/download?domain_id=5&expires=1&signature=a" {
		t.Fatalf("links = %v", links)
	}
}

func TestDownloadWithoutFilesMakesNoCall(t *testing.T) {
	api := &fakeFileAPI{}

	links, err := New(api).Download(context.Background(), 5, nil)
	if err != nil || len(links) != 0 {
		t.Fatalf("links = %v, err = %v", links, err)
	}

	if api.got != nil {
		t.Fatal("Storage was called for an empty page")
	}
}

func TestDownloadPropagatesTheStorageError(t *testing.T) {
	boom := stderrors.New("unavailable")

	_, err := New(&fakeFileAPI{err: boom}).Download(context.Background(), 5, []int64{4})

	if !stderrors.Is(err, boom) {
		t.Fatalf("error = %v, want the storage error", err)
	}
}

func TestDownloadIgnoresSurplusLinks(t *testing.T) {
	// Storage answers by position; an answer longer than the question must
	// not index past the ids.
	api := &fakeFileAPI{resp: &storagepb.BulkGenerateFileLinkResponse{
		Links: []*storagepb.GenerateFileLinkResponse{{Url: "/a"}, {Url: "/b"}},
	}}

	links, err := New(api).Download(context.Background(), 5, []int64{4})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if len(links) != 1 || links[4] != "/a" {
		t.Fatalf("links = %v", links)
	}
}

func TestJoinURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{name: "base with a trailing slash", base: "https://kb.example/", path: "/any/file/1", want: "https://kb.example/any/file/1"},
		{name: "base without a slash", base: "https://kb.example", path: "any/file/1", want: "https://kb.example/any/file/1"},
		{name: "base with a prefix", base: "https://kb.example/api/v2/", path: "/any/file/1?x=1", want: "https://kb.example/api/v2/any/file/1?x=1"},
		{name: "no base keeps the path", base: "", path: "/any/file/1", want: "/any/file/1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinURL(tt.base, tt.path); got != tt.want {
				t.Fatalf("joinURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}
