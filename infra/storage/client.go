// Package storage talks to the Webitel Storage service, the owner of the files
// attached to articles: it stores them, signs their links and removes them.
package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/webitel/webitel-go-kit/infra/discovery"
	rpc "github.com/webitel/webitel-go-kit/infra/transport/gRPC"
	resolver "github.com/webitel/webitel-go-kit/infra/transport/gRPC/resolver/discovery"
	"github.com/webitel/webitel-go-kit/pkg/errors"

	storagepb "github.com/webitel/webitel-kb/api/storage"
)

// ServiceName is the name Storage registers in service discovery under.
const ServiceName = "storage"

// callTimeout bounds one round trip to Storage.
const callTimeout = 5 * time.Second

const (
	// actionDownload is the Storage route action a link is signed for.
	actionDownload = "download"
	// sourceFile reads the metadata of an uploaded file rather than a media
	// file.
	sourceFile = "file"
)

// File is the metadata Storage keeps for an uploaded file.
type File struct {
	ID   int64
	Name string
	Mime string
	Size int64
	URL  string
}

// Files is the part of the Storage file service the attachments need.
type Files interface {
	// Describe returns the metadata of a file of the domain, with a signed
	// download link; an unknown file is an error.
	Describe(ctx context.Context, domainID, fileID int64) (*File, error)

	// Links returns a signed download link per file id for the files Storage
	// could sign; the others are absent from the map.
	Links(ctx context.Context, domainID int64, ids []int64) (map[int64]string, error)

	// Remove deletes the files on behalf of the caller, whose credentials the
	// context carries.
	Remove(ctx context.Context, ids []int64) error
}

// caller runs one call on a pooled connection; the pooled client of the
// transport package is the production implementation.
type caller interface {
	Execute(ctx context.Context, fn func(storagepb.FileServiceClient) error) error
	Close() error
}

// Client calls the Storage file service through service discovery.
type Client struct {
	rpc caller
}

var _ Files = (*Client)(nil)

// New dials Storage through the discovery provider, with a connection pool and
// retries of the transient codes.
func New(dp discovery.DiscoveryProvider) (*Client, error) {
	factory := func(conn *grpc.ClientConn) storagepb.FileServiceClient {
		return storagepb.NewFileServiceClient(conn)
	}

	client, err := rpc.NewClient(
		context.Background(),
		factory,
		rpc.WithTarget(fmt.Sprintf("discovery:///%s", ServiceName)),
		rpc.WithDialOptions(
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithResolvers(resolver.NewBuilder(dp, resolver.WithInsecure(true))),
		),
		rpc.WithRetry(rpc.DefaultRetryConfig()),
		rpc.WithKeepalive(keepalive.ClientParameters{
			Time:                10 * time.Minute,
			Timeout:             20 * time.Second,
			PermitWithoutStream: false,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: client: %w", err)
	}

	return &Client{rpc: client}, nil
}

// Describe reads the metadata and signs the link in one round trip.
func (c *Client) Describe(ctx context.Context, domainID, fileID int64) (*File, error) {
	ctx, cancel := c.call(ctx)
	defer cancel()

	var resp *storagepb.GenerateFileLinkResponse

	err := c.rpc.Execute(ctx, func(api storagepb.FileServiceClient) error {
		var err error

		resp, err = api.GenerateFileLink(ctx, &storagepb.GenerateFileLinkRequest{
			DomainId: domainID,
			FileId:   fileID,
			Source:   sourceFile,
			Action:   actionDownload,
			Metadata: true,
		})

		return err
	})
	if err != nil {
		return nil, wrap(err, "storage.client.describe_file")
	}

	meta := resp.GetMetadata()
	if meta == nil {
		return nil, errors.NotFound(
			"storage returned no metadata for the file",
			errors.WithID("storage.client.describe_file.no_metadata"),
		)
	}

	return &File{
		ID:   fileID,
		Name: meta.GetName(),
		Mime: meta.GetMimeType(),
		Size: meta.GetSize(),
		URL:  joinURL(resp.GetBaseUrl(), resp.GetUrl()),
	}, nil
}

// Links signs the links in one round trip. Storage answers position by
// position and leaves a file it could not sign empty.
func (c *Client) Links(ctx context.Context, domainID int64, ids []int64) (map[int64]string, error) {
	links := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return links, nil
	}

	files := make([]*storagepb.GenerateFileLinkRequest, 0, len(ids))
	for _, id := range ids {
		files = append(files, &storagepb.GenerateFileLinkRequest{
			DomainId: domainID,
			FileId:   id,
			Action:   actionDownload,
		})
	}

	ctx, cancel := c.call(ctx)
	defer cancel()

	var resp *storagepb.BulkGenerateFileLinkResponse

	err := c.rpc.Execute(ctx, func(api storagepb.FileServiceClient) error {
		var err error

		resp, err = api.BulkGenerateFileLink(ctx, &storagepb.BulkGenerateFileLinkRequest{Files: files})

		return err
	})
	if err != nil {
		return nil, wrap(err, "storage.client.sign_links")
	}

	// Storage answers position by position; a shorter answer would shift the
	// links onto the wrong files, so it is refused rather than guessed.
	if len(resp.GetLinks()) != len(ids) {
		return nil, errors.Internal(
			"storage signed a different number of links",
			errors.WithID("storage.client.sign_links.mismatch"),
		)
	}

	for i, link := range resp.GetLinks() {
		if link.GetUrl() == "" {
			continue
		}

		links[ids[i]] = joinURL(link.GetBaseUrl(), link.GetUrl())
	}

	return links, nil
}

// Remove asks Storage to delete the files. Storage authorizes the caller
// itself, so the credentials of the inbound call are passed along.
func (c *Client) Remove(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	ctx, cancel := c.call(ctx)
	defer cancel()

	err := c.rpc.Execute(ctx, func(api storagepb.FileServiceClient) error {
		_, err := api.DeleteFiles(ctx, &storagepb.DeleteFilesRequest{Id: ids})

		return err
	})
	if err != nil {
		return wrap(err, "storage.client.delete_files")
	}

	return nil
}

// call bounds one round trip and carries the credentials of the inbound call,
// the access token among them: Storage authorizes writes itself.
func (c *Client) call(ctx context.Context) (context.Context, context.CancelFunc) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	return context.WithTimeout(ctx, callTimeout)
}

// wrap keeps the code Storage answered with, so an outage is not reported as a
// missing file. The answer itself stays in the cause: it is not for the client.
func wrap(err error, id string) error {
	wrappers := []errors.Wrapper{errors.WithID(id), errors.WithCause(err)}
	if st, ok := status.FromError(err); ok {
		wrappers = append(wrappers, errors.WithCode(st.Code()))
	}

	return errors.New("storage request failed", wrappers...)
}

// Close shuts the connection pool down.
func (c *Client) Close() error {
	if c.rpc == nil {
		return nil
	}

	return c.rpc.Close()
}

// joinURL appends the signed path to the public base of Storage.
func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return path
	}

	return base + "/" + strings.TrimLeft(path, "/")
}
