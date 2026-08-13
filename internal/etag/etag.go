// Package etag encodes and decodes the public entity tokens of the KB API
// over the shared go-kit etag format.
package etag

import (
	"github.com/webitel/webitel-go-kit/pkg/errors"
	kitetag "github.com/webitel/webitel-go-kit/pkg/etag"
)

// TypeArticle is the token type of knowledge-base articles, registered in the
// shared go-kit registry.
const TypeArticle = kitetag.EtagKbArticle

var errInvalid = errors.InvalidArgument(
	"invalid etag",
	errors.WithID("kb.etag.invalid"),
)

// Encode builds the token of an entity from its id and version.
func Encode(typ kitetag.EtagType, id int64, ver int32) (string, error) {
	tag, err := kitetag.EncodeEtag(typ, id, ver)
	if err != nil {
		return "", errors.Internal(
			"etag encoding failed",
			errors.WithID("kb.etag.encode"),
			errors.WithCause(err),
		)
	}

	return tag, nil
}

// Parse decodes a full token of the given type into id and version; mutations
// need the version, so a bare id is rejected.
func Parse(typ kitetag.EtagType, s string) (id int64, ver int32, err error) {
	tag, err := kitetag.ExpectEtag(typ, s)
	if err != nil || !tag.HasOid() {
		return 0, 0, errInvalid
	}

	return tag.GetOid(), tag.GetVer(), nil
}

// ParseLocator resolves a read locator: a full token of the given type or a
// bare numeric id.
func ParseLocator(typ kitetag.EtagType, s string) (int64, error) {
	tag, err := kitetag.EtagOrId(typ, s)
	if err != nil || !tag.HasOid() {
		return 0, errInvalid
	}

	return tag.GetOid(), nil
}
