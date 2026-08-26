// Package outbox holds the transactional outbox contract shared by the writer
// and the relay. Both sides must address the same tables through the same
// watermill adapters: a mismatch here is silent, the writer would simply fill
// a table nobody reads.
package outbox

import (
	"github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
)

const (
	// Topic names the single outbox stream. All topics share one table, so
	// the value only has to stay stable between writer and relay.
	Topic = "kb.reindex"

	// ConsumerGroup owns the offset the relay advances.
	ConsumerGroup = "webitel.kb-outbox-relay"

	// MessagesTable and OffsetsTable are created by migration, not by
	// watermill: schema initialization stays off on both sides.
	MessagesTable = "kb.outbox_events"
	OffsetsTable  = "kb.outbox_offsets"

	// MetadataRoutingKey carries the broker routing key of an envelope; the
	// relay reads it back when publishing.
	MetadataRoutingKey = "x-routing-key"

	// MetadataType and MetadataSchema make a stored row identifiable without
	// decoding its payload.
	MetadataType   = "x-event-type"
	MetadataSchema = "x-event-schema"
)

// SchemaAdapter maps every topic onto the one outbox table.
func SchemaAdapter() sql.DefaultPostgreSQLSchema {
	return sql.DefaultPostgreSQLSchema{
		GenerateMessagesTableName: func(string) string { return MessagesTable },
		GeneratePayloadType:       func(string) string { return "bytea" },
	}
}

// OffsetsAdapter maps every topic onto the one offsets table.
func OffsetsAdapter() sql.DefaultPostgreSQLOffsetsAdapter {
	return sql.DefaultPostgreSQLOffsetsAdapter{
		GenerateMessagesOffsetsTableName: func(string) string { return OffsetsTable },
	}
}
