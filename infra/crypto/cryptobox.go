package crypto

import (
	"context"
	"errors"

	"github.com/webitel/crypto/cryptostore"
)

// ErrNotCiphertext reports a non-empty stored value without the ciphertext
// marker. Every value this package writes is framed, so an unframed one can
// only mean corruption or a write that bypassed the Encryptor; the codec's
// legacy-plaintext passthrough would mask that, hence the explicit error.
var ErrNotCiphertext = errors.New("crypto: stored value is not ciphertext")

// codecEncryptor adapts a cryptostore.Codec to Encryptor. All coupling to the
// crypto library is confined to this file: if the library API changes, only
// this adapter needs to move.
type codecEncryptor struct {
	codec *cryptostore.Codec
}

// New returns an Encryptor backed by the given value codec. The codec owns the
// keyring (primary key for sealing, key routing by id for opening) and frames
// each blob as self-marking ciphertext, which is the format the shared key
// rotation tooling recognizes.
func New(codec *cryptostore.Codec) Encryptor {
	return codecEncryptor{codec: codec}
}

func (e codecEncryptor) Encrypt(ctx context.Context, plain []byte) ([]byte, error) {
	return e.codec.Encrypt(ctx, plain)
}

func (e codecEncryptor) Decrypt(ctx context.Context, blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}

	// A bare frame carries no cipher bytes to authenticate; Encrypt never
	// writes one, so it is corruption, not an encrypted empty payload, and must
	// not open as a valid empty value.
	if inner, ok := cryptostore.UnframeBinary(blob); ok && len(inner) == 0 {
		return nil, ErrNotCiphertext
	}

	data, encrypted, err := e.codec.Decrypt(ctx, blob)
	if err != nil {
		return nil, err
	}

	if !encrypted {
		return nil, ErrNotCiphertext
	}

	return data, nil
}
