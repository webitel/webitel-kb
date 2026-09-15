// Package storage talks to the Webitel Storage service, the keeper of the
// files attached to articles.
package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	storagepb "github.com/webitel/webitel-kb/api/storage"
)

// ServiceName is the name Storage registers in Consul under.
const ServiceName = "storage"

// linkTimeout bounds one link signing round trip.
const linkTimeout = 5 * time.Second

// actionDownload is the Storage route action a download link is signed for.
const actionDownload = "download"

// Links issues download links for stored files.
type Links interface {
	// Download returns a signed download link per file id for the files
	// Storage could sign; the others are absent from the map.
	Download(ctx context.Context, domainID int64, ids []int64) (map[int64]string, error)
}

// Client signs download links through the Storage file service.
type Client struct {
	api storagepb.FileServiceClient
}

var _ Links = (*Client)(nil)

// New wraps the Storage file service client.
func New(api storagepb.FileServiceClient) *Client {
	return &Client{api: api}
}

// Download signs the links in one round trip. Storage answers position by
// position and leaves a file it could not sign empty.
func (c *Client) Download(ctx context.Context, domainID int64, ids []int64) (map[int64]string, error) {
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

	ctx, cancel := context.WithTimeout(ctx, linkTimeout)
	defer cancel()

	resp, err := c.api.BulkGenerateFileLink(ctx, &storagepb.BulkGenerateFileLinkRequest{Files: files})
	if err != nil {
		return nil, fmt.Errorf("storage: sign download links: %w", err)
	}

	for i, link := range resp.GetLinks() {
		if i >= len(ids) || link.GetUrl() == "" {
			continue
		}

		links[ids[i]] = joinURL(link.GetBaseUrl(), link.GetUrl())
	}

	return links, nil
}

// joinURL appends the signed path to the public base of Storage.
func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return path
	}

	return base + "/" + strings.TrimLeft(path, "/")
}
