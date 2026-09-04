package grpc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	kbindexer "github.com/webitel/webitel-kb/api/kb/indexer"
	"github.com/webitel/webitel-kb/config"
	grpcsrv "github.com/webitel/webitel-kb/infra/server/grpc"
	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

// sealer opens what the store handed over.
type sealer struct{}

func (sealer) Encrypt(_ context.Context, plain []byte) ([]byte, error) { return plain, nil }

func (sealer) Decrypt(_ context.Context, blob []byte) ([]byte, error) {
	return bytes.TrimPrefix(blob, []byte("enc:")), nil
}

func indexingServer(embedding *model.SpaceEmbedding, log *slog.Logger) *IndexingServer {
	uow := &fakeUow{embedding: embedding}

	return NewIndexingServer(service.NewIndexingService(uow, sealer{}), log)
}

func TestResolveSpaceEmbeddingMapsTheModel(t *testing.T) {
	server := indexingServer(&model.SpaceEmbedding{
		VectorSearchEnabled: true,
		ModelID:             9,
		Provider:            "gemini",
		ModelRef:            "gemini-embedding-001",
		Dimensions:          768,
		Endpoint:            "https://embed.local",
		Config:              []byte("enc:secret"),
		Validated:           true,
	}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	got, err := server.ResolveSpaceEmbedding(
		context.Background(),
		&kbindexer.ResolveSpaceEmbeddingRequest{SpaceId: 7},
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	want := &kbindexer.SpaceEmbedding{
		VectorSearchEnabled: true,
		ModelId:             9, Provider: "gemini", ModelRef: "gemini-embedding-001",
		Dimensions: 768, Endpoint: "https://embed.local", ApiKey: "secret", Validated: true,
	}

	if got.GetVectorSearchEnabled() != want.GetVectorSearchEnabled() ||
		got.GetModelId() != want.GetModelId() || got.GetProvider() != want.GetProvider() ||
		got.GetModelRef() != want.GetModelRef() || got.GetDimensions() != want.GetDimensions() ||
		got.GetEndpoint() != want.GetEndpoint() || got.GetApiKey() != want.GetApiKey() ||
		got.GetValidated() != want.GetValidated() {
		t.Fatalf("resolved = %+v, want %+v", got, want)
	}
}

func TestResolveSpaceEmbeddingRejectsAMissingSpaceID(t *testing.T) {
	server := indexingServer(nil, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	_, err := server.ResolveSpaceEmbedding(context.Background(), &kbindexer.ResolveSpaceEmbeddingRequest{})
	if got := errors.Code(err); got != codes.InvalidArgument {
		t.Fatalf("error code = %v, want invalid argument (err: %v)", got, err)
	}
}

func TestResolveSpaceEmbeddingKeepsTheCredentialOutOfTheLog(t *testing.T) {
	var logged bytes.Buffer

	server := indexingServer(&model.SpaceEmbedding{
		VectorSearchEnabled: true,
		ModelID:             9,
		Provider:            "gemini",
		Config:              []byte("enc:top-secret-credential"),
	}, slog.New(slog.NewTextHandler(&logged, nil)))

	if _, err := server.ResolveSpaceEmbedding(
		context.Background(),
		&kbindexer.ResolveSpaceEmbeddingRequest{SpaceId: 7},
	); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if strings.Contains(logged.String(), "top-secret-credential") {
		t.Fatalf("the credential reached the log: %s", logged.String())
	}

	for _, want := range []string{"embedding credential issued", "space_id=7", "model_id=9"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log does not record %q: %s", want, logged.String())
		}
	}
}

// authNoOne stands in for the session manager.
type authNoOne struct{}

func (authNoOne) AuthorizeFromContext(context.Context, string, auth.AccessMode) (auth.Auther, error) {
	return nil, errors.Unauthenticated("no session")
}

func TestInternalAPIIsServedOnlyWhenGuarded(t *testing.T) {
	const service = "webitel.kb.indexer.Indexing"

	tests := []struct {
		name  string
		token string

		want bool
	}{
		{name: "no token leaves the api unregistered", token: ""},
		{name: "a token registers the api", token: "0123456789abcdef0123456789abcdef", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := grpcsrv.New("127.0.0.1:0", func(c *grpcsrv.Config) error {
				c.AuthManager = authNoOne{}

				return nil
			})
			if err != nil {
				t.Fatalf("server: %v", err)
			}

			t.Cleanup(func() { _ = server.Shutdown() })

			conf := &config.Config{}
			conf.Service.Internal.Token = tt.token
			log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

			RegisterService(
				conf, log, server,
				NewSpacesServer(nil), NewEmbeddingModelsServer(nil), NewArticlesServer(nil),
				NewVersionsServer(nil), NewTagsServer(nil), NewAttachmentsServer(), NewRetrievalServer(),
				indexingServer(nil, log),
			)

			_, registered := server.GetServiceInfo()[service]
			if registered != tt.want {
				t.Fatalf("%s registered = %v, want %v", service, registered, tt.want)
			}
		})
	}
}

func TestResolveSpaceEmbeddingReportsASpaceWithoutVectorSearch(t *testing.T) {
	var logged bytes.Buffer

	server := indexingServer(&model.SpaceEmbedding{ModelID: 9, Provider: "gemini"},
		slog.New(slog.NewTextHandler(&logged, nil)))

	got, err := server.ResolveSpaceEmbedding(
		context.Background(),
		&kbindexer.ResolveSpaceEmbeddingRequest{SpaceId: 7},
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got.GetVectorSearchEnabled() || got.GetModelId() != 0 {
		t.Fatalf("resolved = %+v, want an empty answer", got)
	}

	if strings.Contains(logged.String(), "credential issued") {
		t.Errorf("recorded a credential that was never issued: %s", logged.String())
	}
}
