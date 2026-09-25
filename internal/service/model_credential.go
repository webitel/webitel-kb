package service

import (
	"context"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/crypto"
)

// openModelCredential opens the stored credential of a model.
func openModelCredential(ctx context.Context, enc crypto.Encryptor, provider string, config []byte) (string, error) {
	if _, cloud := cloudProviders[provider]; cloud {
		// Say so here, rather than let the caller retry an authentication failure.
		if len(config) == 0 {
			return "", errors.New(
				"model has no stored credential",
				errors.WithCode(codes.FailedPrecondition),
				errors.WithID("kb.model.credential_missing"),
			)
		}
	} else {
		// Self-hosted providers take no key.
		config = nil
	}

	key, err := enc.Decrypt(ctx, config)
	if err != nil {
		return "", errors.Internal(
			"unable to open the model credential",
			errors.WithID("kb.model.credential"),
			errors.WithCause(err),
		)
	}

	return string(key), nil
}
