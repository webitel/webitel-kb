// Package crypto provides application-level encryption for sensitive stored
// values such as embedding-model credentials. Callers depend on the Encryptor
// seam defined here rather than on the underlying cipher library, so the backing
// implementation can be swapped without touching business logic.
package crypto

import "context"

// Encryptor seals and opens opaque byte payloads. Implementations are safe for
// concurrent use.
type Encryptor interface {
	// Encrypt seals plaintext into a self-describing blob: the blob marks itself
	// as ciphertext and carries the key identifier used, so key rotation needs no
	// companion column. Sealing empty plaintext still yields a non-empty
	// authenticated blob; callers that must distinguish "no value" from
	// "encrypted empty" should skip encryption and store nothing instead.
	Encrypt(ctx context.Context, plain []byte) ([]byte, error)

	// Decrypt opens a blob produced by Encrypt back into plaintext. An empty
	// blob yields nil; a non-empty value that does not carry the ciphertext
	// marker is corruption, not legacy plaintext, and is an error.
	Decrypt(ctx context.Context, blob []byte) ([]byte, error)
}
