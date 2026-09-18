package model

import "time"

// Attachment is a file of an article: the bytes stay in Storage, this is the
// binding with the metadata a listing needs. URL is the signed download link
// when one was issued.
type Attachment struct {
	// ID is the Storage file id.
	ID        int64
	Name      string
	Size      int64
	Mime      string
	URL       string
	CreatedAt time.Time
	CreatedBy *Lookup
}
