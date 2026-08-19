package grpc

import (
	"context"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/service"
)

// VersionsServer handles the Versions gRPC service: the immutable history of
// an article and restoring a past version.
type VersionsServer struct {
	kb.UnimplementedVersionsServer

	service *service.ArticleService
}

func NewVersionsServer(service *service.ArticleService) *VersionsServer {
	return &VersionsServer{service: service}
}

func (s *VersionsServer) ListVersions(ctx context.Context, req *kb.ListVersionsRequest) (*kb.ArticleVersionList, error) {
	// The display limit is the options-layer default page size.
	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithFields(req),
		options.WithSort(req),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.ListVersions(ctx, opts, req.GetArticleId())
	if err != nil {
		return nil, err
	}

	versions := make([]*kb.ArticleVersion, 0, len(items))

	for _, item := range items {
		version, verr := versionToProto(item)
		if verr != nil {
			return nil, verr
		}

		versions = append(versions, version)
	}

	return &kb.ArticleVersionList{Items: versions, Next: next}, nil
}

func (s *VersionsServer) GetVersion(ctx context.Context, req *kb.GetVersionRequest) (*kb.ArticleVersion, error) {
	opts, err := options.NewSearchOptions(ctx)
	if err != nil {
		return nil, err
	}

	item, err := s.service.GetVersion(ctx, opts, req.GetArticleId(), req.GetVersionNumber())
	if err != nil {
		return nil, err
	}

	return versionToProto(item)
}

func (s *VersionsServer) RestoreVersion(ctx context.Context, req *kb.RestoreVersionRequest) (*kb.ArticleVersion, error) {
	opts, err := options.NewUpdateOptions(ctx, options.WithUpdateID(req.GetArticleId()))
	if err != nil {
		return nil, err
	}

	restored, err := s.service.RestoreVersion(ctx, opts,
		req.GetArticleId(), req.GetVersionNumber(), req.GetNotes())
	if err != nil {
		return nil, err
	}

	return versionToProto(restored)
}
