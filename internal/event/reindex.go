// Package event defines the wire contract for messages the KB service emits
// through the message broker. The producer builds and validates an envelope
// here and stores its JSON in the outbox row; the relay hands the stored value
// to the broker without parsing it back. The outbox column is jsonb, which
// normalizes the representation (key order is not preserved), so the wire
// contract is the JSON content, never its byte layout.
//
// Ordering precondition: an outbox row must be inserted in the same database
// transaction that updates the article row itself (the compare-and-set on its
// version counter). The row lock taken by that update serializes concurrent
// writers, which is what makes ascending outbox ids equal commit order — the
// property the relay's per-article ordering relies on.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TypeArticleReindex marks an envelope that asks the indexing worker to
// rebuild the search artifacts of one article version.
const TypeArticleReindex = "article.reindex"

// ReindexSchema is the current article.reindex envelope schema. Adding fields
// keeps the number; changing or removing a field bumps it.
const ReindexSchema = 1

// ArticleReindex is the article.reindex envelope. It carries identifiers
// only, never document bodies — the worker reads everything else from the
// database.
type ArticleReindex struct {
	Type       string    `json:"type"`
	Schema     int       `json:"schema"`
	OccurredAt time.Time `json:"occurred_at"`
	ArticleID  int64     `json:"article_id"`
	VersionID  int64     `json:"version_id"`
	SpaceID    int64     `json:"space_id"`
	DomainID   int64     `json:"domain_id"`
}

// NewArticleReindex builds an envelope for one article version.
func NewArticleReindex(occurredAt time.Time, articleID, versionID, spaceID, domainID int64) ArticleReindex {
	return ArticleReindex{
		Type:       TypeArticleReindex,
		Schema:     ReindexSchema,
		OccurredAt: occurredAt.UTC(),
		ArticleID:  articleID,
		VersionID:  versionID,
		SpaceID:    spaceID,
		DomainID:   domainID,
	}
}

var errEnvelope = errors.New("event: invalid envelope")

// Validate reports whether the envelope is complete enough to publish.
func (e ArticleReindex) Validate() error {
	switch {
	case e.Type != TypeArticleReindex:
		return fmt.Errorf("%w: type %q", errEnvelope, e.Type)
	case e.Schema < 1:
		return fmt.Errorf("%w: schema %d", errEnvelope, e.Schema)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("%w: zero occurred_at", errEnvelope)
	case e.ArticleID <= 0:
		return fmt.Errorf("%w: article_id %d", errEnvelope, e.ArticleID)
	case e.VersionID <= 0:
		return fmt.Errorf("%w: version_id %d", errEnvelope, e.VersionID)
	case e.SpaceID <= 0:
		return fmt.Errorf("%w: space_id %d", errEnvelope, e.SpaceID)
	case e.DomainID <= 0:
		return fmt.Errorf("%w: domain_id %d", errEnvelope, e.DomainID)
	}

	return nil
}

// Marshal validates the envelope and renders its wire JSON. Timestamps render
// in UTC so equal envelopes are byte-identical regardless of producer locale.
func (e ArticleReindex) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}

	e.OccurredAt = e.OccurredAt.UTC()

	return json.Marshal(e)
}

// UnmarshalArticleReindex parses and validates a wire envelope. The relay does
// not use it — it forwards stored bytes untouched; this is for the producer's
// own tests and tooling.
func UnmarshalArticleReindex(data []byte) (ArticleReindex, error) {
	var e ArticleReindex

	if err := json.Unmarshal(data, &e); err != nil {
		return ArticleReindex{}, fmt.Errorf("event: parse envelope: %w", err)
	}

	if err := e.Validate(); err != nil {
		return ArticleReindex{}, err
	}

	return e, nil
}
