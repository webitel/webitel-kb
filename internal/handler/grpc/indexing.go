package grpc

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/peer"

	kbindexer "github.com/webitel/webitel-kb/api/kb/indexer"
	"github.com/webitel/webitel-kb/internal/service"
)

// IndexingServer handles the Indexing gRPC service: the API the indexer worker
// calls. Registered only where a service token guards it, never bound to HTTP.
type IndexingServer struct {
	kbindexer.UnimplementedIndexingServer

	service *service.IndexingService
	log     *slog.Logger
}

func NewIndexingServer(service *service.IndexingService, log *slog.Logger) *IndexingServer {
	return &IndexingServer{service: service, log: log}
}

func (s *IndexingServer) ResolveSpaceEmbedding(
	ctx context.Context, req *kbindexer.ResolveSpaceEmbeddingRequest,
) (*kbindexer.SpaceEmbedding, error) {
	found, err := s.service.ResolveSpaceEmbedding(ctx, req.GetSpaceId())
	if err != nil {
		return nil, err
	}

	// Audit the handover; never the credential itself.
	if found.APIKey != "" {
		s.log.InfoContext(ctx, "embedding credential issued",
			slog.Int64("space_id", req.GetSpaceId()),
			slog.Int64("model_id", found.ModelID),
			slog.String("provider", found.Provider),
			slog.Bool("validated", found.Validated),
			slog.String("peer", callerAddr(ctx)),
		)
	}

	return &kbindexer.SpaceEmbedding{
		VectorSearchEnabled: found.VectorSearchEnabled,
		ModelId:             found.ModelID,
		Provider:            found.Provider,
		ModelRef:            found.ModelRef,
		Dimensions:          found.Dimensions,
		Endpoint:            found.Endpoint,
		ApiKey:              found.APIKey,
		Validated:           found.Validated,
	}, nil
}

// callerAddr names the caller for the audit record.
func callerAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}

	return "unknown"
}
