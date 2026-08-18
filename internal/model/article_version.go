package model

import "time"

// TextSearchDefault is the configuration article versions build their search
// vector with. The built-in language agnostic one for now: accent folding and
// per-language dictionaries are decisions for the retrieval stage, and the
// stored vectors are cheap to rebuild until then.
const TextSearchDefault = "simple"

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
