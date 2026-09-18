package storage

import (
	"context"
	stderrors "errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	storagepb "github.com/webitel/webitel-kb/api/storage"
)

// fakeFileAPI answers the calls the attachments make; the embedded nil
// interface makes every other method a loud failure.
type fakeFileAPI struct {
	storagepb.FileServiceClient

	bulk     *storagepb.BulkGenerateFileLinkRequest
	link     *storagepb.GenerateFileLinkRequest
	deleted  *storagepb.DeleteFilesRequest
	outgoing metadata.MD
	deadline bool

	bulkResp *storagepb.BulkGenerateFileLinkResponse
	linkResp *storagepb.GenerateFileLinkResponse
	err      error
}

func (f *fakeFileAPI) BulkGenerateFileLink(
	ctx context.Context, in *storagepb.BulkGenerateFileLinkRequest, _ ...grpc.CallOption,
) (*storagepb.BulkGenerateFileLinkResponse, error) {
	f.bulk = in
	_, f.deadline = ctx.Deadline()

	return f.bulkResp, f.err
}

func (f *fakeFileAPI) GenerateFileLink(
	ctx context.Context, in *storagepb.GenerateFileLinkRequest, _ ...grpc.CallOption,
) (*storagepb.GenerateFileLinkResponse, error) {
	f.link = in
	_, f.deadline = ctx.Deadline()

	return f.linkResp, f.err
}

func (f *fakeFileAPI) DeleteFiles(
	ctx context.Context, in *storagepb.DeleteFilesRequest, _ ...grpc.CallOption,
) (*storagepb.DeleteFilesResponse, error) {
	f.deleted = in
	f.outgoing, _ = metadata.FromOutgoingContext(ctx)
	_, f.deadline = ctx.Deadline()

	return &storagepb.DeleteFilesResponse{}, f.err
}

// directCaller runs the call on the fake instead of a pooled connection.
type directCaller struct {
	api    storagepb.FileServiceClient
	closed bool
}

func (c *directCaller) Execute(_ context.Context, fn func(storagepb.FileServiceClient) error) error {
	return fn(c.api)
}

func (c *directCaller) Close() error {
	c.closed = true

	return nil
}

func clientWith(api *fakeFileAPI) *Client {
	return &Client{rpc: &directCaller{api: api}}
}

func TestLinksSignsEveryFileOfTheDomain(t *testing.T) {
	api := &fakeFileAPI{bulkResp: &storagepb.BulkGenerateFileLinkResponse{
		Links: []*storagepb.GenerateFileLinkResponse{
			{Url: "/any/file/4/download?domain_id=5&expires=1&signature=a", BaseUrl: "https://kb.example/"},
			nil,
			{Url: "", BaseUrl: "https://kb.example/"},
		},
	}}

	links, err := clientWith(api).Links(context.Background(), 5, []int64{4, 6, 8})
	if err != nil {
		t.Fatalf("Links: %v", err)
	}

	if len(api.bulk.GetFiles()) != 3 {
		t.Fatalf("request carries %d files, want 3", len(api.bulk.GetFiles()))
	}

	for i, id := range []int64{4, 6, 8} {
		file := api.bulk.GetFiles()[i]
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

func TestLinksWithoutFilesMakesNoCall(t *testing.T) {
	api := &fakeFileAPI{}

	links, err := clientWith(api).Links(context.Background(), 5, nil)
	if err != nil || len(links) != 0 {
		t.Fatalf("links = %v, err = %v", links, err)
	}

	if api.bulk != nil {
		t.Fatal("Storage was called for an empty page")
	}
}

func TestLinksPropagatesTheStorageError(t *testing.T) {
	boom := stderrors.New("unavailable")

	_, err := clientWith(&fakeFileAPI{err: boom}).Links(context.Background(), 5, []int64{4})

	if !stderrors.Is(err, boom) {
		t.Fatalf("error = %v, want the storage error", err)
	}
}

func TestLinksRefuseAMisalignedAnswer(t *testing.T) {
	// Storage answers by position, so an answer of another length would move
	// the links onto the wrong files.
	tests := []struct {
		name  string
		links []*storagepb.GenerateFileLinkResponse
	}{
		{name: "more links than files", links: []*storagepb.GenerateFileLinkResponse{{Url: "/a"}, {Url: "/b"}}},
		{name: "fewer links than files", links: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeFileAPI{bulkResp: &storagepb.BulkGenerateFileLinkResponse{Links: tt.links}}

			if _, err := clientWith(api).Links(context.Background(), 5, []int64{4}); err == nil {
				t.Fatal("a misaligned answer was accepted")
			}
		})
	}
}

func TestDescribeReadsMetadataAndLink(t *testing.T) {
	api := &fakeFileAPI{linkResp: &storagepb.GenerateFileLinkResponse{
		Url:     "/any/file/4/download?signature=a",
		BaseUrl: "https://kb.example",
		Metadata: &storagepb.GenerateFileLinkResponse_Metadata{
			Id: 4, Name: "guide.pdf", MimeType: "application/pdf", Size: 2048,
		},
	}}

	file, err := clientWith(api).Describe(context.Background(), 5, 4)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}

	if api.link.GetDomainId() != 5 || api.link.GetFileId() != 4 ||
		api.link.GetSource() != sourceFile || !api.link.GetMetadata() {
		t.Fatalf("request = %+v, want the file of domain 5 with its metadata", api.link)
	}

	if file.ID != 4 || file.Name != "guide.pdf" || file.Mime != "application/pdf" || file.Size != 2048 {
		t.Fatalf("file = %+v", file)
	}

	if file.URL != "https://kb.example/any/file/4/download?signature=a" {
		t.Fatalf("url = %q", file.URL)
	}
}

func TestDescribeFailsWithoutMetadata(t *testing.T) {
	tests := []struct {
		name string
		api  *fakeFileAPI
	}{
		{name: "storage refused", api: &fakeFileAPI{err: stderrors.New("not found")}},
		{name: "no metadata came back", api: &fakeFileAPI{linkResp: &storagepb.GenerateFileLinkResponse{Url: "/a"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := clientWith(tt.api).Describe(context.Background(), 5, 4); err == nil {
				t.Fatal("a file without metadata was accepted")
			}
		})
	}
}

func TestRemoveForwardsTheCallerCredentials(t *testing.T) {
	api := &fakeFileAPI{}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-webitel-access", "token-1"))

	if err := clientWith(api).Remove(ctx, []int64{4, 6}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if len(api.deleted.GetId()) != 2 || api.deleted.GetId()[0] != 4 {
		t.Fatalf("deleted = %v, want both files", api.deleted.GetId())
	}

	if got := api.outgoing.Get("x-webitel-access"); len(got) != 1 || got[0] != "token-1" {
		t.Fatalf("outgoing token = %v, want the one of the inbound call", got)
	}
}

func TestRemoveWithoutFilesMakesNoCall(t *testing.T) {
	api := &fakeFileAPI{}

	if err := clientWith(api).Remove(context.Background(), nil); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if api.deleted != nil {
		t.Fatal("Storage was called for no files")
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
