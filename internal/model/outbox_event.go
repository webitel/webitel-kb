package model

// OutboxEvent is one pending row of the transactional outbox.
type OutboxEvent struct {
	ID        int64
	ArticleID int64
	Payload   []byte
}
