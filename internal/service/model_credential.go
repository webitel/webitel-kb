package service

import (
	"context"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/crypto"
	"github.com/webitel/webitel-kb/internal/model"
)

// openModelCredential replaces the stored credential of a model with the opened key.
func openModelCredential(ctx context.Context, enc crypto.Encryptor, found *model.SpaceEmbedding) error {
	if _, cloud := cloudProviders[found.Provider]; cloud {
		// Say so here, rather than let the caller retry an authentication failure.
		if len(found.Config) == 0 {
			return errors.Aborted(
				"model has no stored credential",
				errors.WithID("kb.model.credential_missing"),
			)
		}
	} else {
		// Self-hosted providers take no key.
		found.Config = nil
	}

	key, err := enc.Decrypt(ctx, found.Config)
	if err != nil {
		return errors.Internal(
			"unable to open the model credential",
			errors.WithID("kb.model.credential"),
			errors.WithCause(err),
		)
	}

	found.Config = nil
	found.APIKey = string(key)

	return nil
}
