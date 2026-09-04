package service

import (
	"context"
	stderrors "errors"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/model"
)

func indexingService(store *fakeSpaceStore, sealer fakeSealer) *IndexingService {
	return NewIndexingService(&fakeUow{spaces: store}, sealer)
}

func TestResolveSpaceEmbedding(t *testing.T) {
	store := &fakeSpaceStore{resolved: &model.SpaceEmbedding{
		VectorSearchEnabled: true,
		ModelID:             9,
		Provider:            "gemini",
		ModelRef:            "gemini-embedding-001",
		Dimensions:          768,
		Config:              []byte("enc:secret"),
		Validated:           true,
	}}

	found, err := indexingService(store, fakeSealer{}).ResolveSpaceEmbedding(context.Background(), 7)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if store.resolvedID != 7 {
		t.Errorf("resolved space = %d, want 7", store.resolvedID)
	}

	if found.APIKey != "secret" {
		t.Errorf("api key = %q, want the opened credential", found.APIKey)
	}

	if found.Config != nil {
		t.Errorf("config = %q, want it cleared", found.Config)
	}

	if found.ModelID != 9 || found.Dimensions != 768 || !found.Validated {
		t.Errorf("resolved = %+v", found)
	}
}

func TestResolveSpaceEmbeddingRefusals(t *testing.T) {
	tests := []struct {
		name     string
		spaceID  int64
		store    *fakeSpaceStore
		wantCode codes.Code
		wantID   string
	}{
		{
			name:     "a missing space id is rejected before the store",
			spaceID:  0,
			store:    &fakeSpaceStore{},
			wantCode: codes.InvalidArgument,
			wantID:   "kb.space.id_required",
		},
		{
			name:     "a space without a model is refused by reason",
			spaceID:  7,
			store:    &fakeSpaceStore{resolved: &model.SpaceEmbedding{VectorSearchEnabled: true}},
			wantCode: codes.Aborted,
			wantID:   "kb.space.model_unset",
		},
		{
			name:    "a cloud model without a credential is refused",
			spaceID: 7,
			store: &fakeSpaceStore{resolved: &model.SpaceEmbedding{
				VectorSearchEnabled: true, ModelID: 9, Provider: "gemini",
			}},
			wantCode: codes.Aborted,
			wantID:   "kb.model.credential_missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := indexingService(tt.store, fakeSealer{}).ResolveSpaceEmbedding(context.Background(), tt.spaceID)
			if got := errors.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}

			if got := errors.ID(err); got != tt.wantID {
				t.Fatalf("error id = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestResolveSpaceEmbeddingWithdrawsCredentialFromSelfHosted(t *testing.T) {
	// Self-hosted providers take no credential. A stored one is a registration
	// mistake, and this call must not be the way it escapes.
	store := &fakeSpaceStore{resolved: &model.SpaceEmbedding{
		VectorSearchEnabled: true,
		ModelID:             9,
		Provider:            "e5",
		Endpoint:            "http://embed.local",
		Config:              []byte("enc:secret"),
	}}

	found, err := indexingService(store, fakeSealer{}).ResolveSpaceEmbedding(context.Background(), 7)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if found.APIKey != "" {
		t.Fatalf("api key = %q, want none for a self-hosted provider", found.APIKey)
	}
}

func TestResolveSpaceEmbeddingPropagatesStoreAndCryptoFailures(t *testing.T) {
	t.Run("store failure", func(t *testing.T) {
		store := &fakeSpaceStore{resolveErr: errors.NotFound("gone", errors.WithID("kb.space.not_found"))}

		_, err := indexingService(store, fakeSealer{}).ResolveSpaceEmbedding(context.Background(), 7)
		if errors.Code(err) != codes.NotFound {
			t.Fatalf("error code = %v, want not found (err: %v)", errors.Code(err), err)
		}
	})

	t.Run("credential failure", func(t *testing.T) {
		store := &fakeSpaceStore{resolved: &model.SpaceEmbedding{
			VectorSearchEnabled: true,
			ModelID:             9,
			Provider:            "gemini",
			Config:              []byte("enc:secret"),
		}}
		sealer := fakeSealer{decryptErr: stderrors.New("key retired")}

		_, err := indexingService(store, sealer).ResolveSpaceEmbedding(context.Background(), 7)
		if errors.Code(err) != codes.Internal {
			t.Fatalf("error code = %v, want internal (err: %v)", errors.Code(err), err)
		}
	})
}

func TestResolveSpaceEmbeddingAnswersASpaceWithoutVectorSearch(t *testing.T) {
	// Vector search off is a normal state, not a failure.
	store := &fakeSpaceStore{resolved: &model.SpaceEmbedding{
		ModelID:  9,
		Provider: "gemini",
		Config:   []byte("enc:secret"),
	}}

	found, err := indexingService(store, fakeSealer{}).ResolveSpaceEmbedding(context.Background(), 7)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if found.VectorSearchEnabled {
		t.Error("vector search reported as enabled")
	}

	if found.ModelID != 0 || found.APIKey != "" || found.Provider != "" {
		t.Fatalf("resolved = %+v, want an empty answer", found)
	}
}
