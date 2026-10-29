package model

import "time"

// MaxBodyBytes bounds the editor document of a version, in bytes. It keeps an
// article within what the database, the transport and the index can carry.
const MaxBodyBytes = 512 << 10

// ArticleVersion is an immutable snapshot of article content.
type ArticleVersion struct {
	ID        int64
	ArticleID int64
	// VersionNumber is monotonic within the article, starting at one.
	VersionNumber int32
	Subject       string
	// BodyRichText is the canonical editor document.
	BodyRichText []byte
	// BodyMarkdown is the chunking input, BodyPlain the full-text source.
	BodyMarkdown string
	BodyPlain    string
	// RestoredFrom is the version this one was restored from; 0 when the
	// content was authored directly.
	RestoredFrom int64
	Notes        string
	CreatedAt    time.Time
	CreatedBy    *Lookup
}
