package model

import "time"

// Attachment is the metadata of a Storage file bound to an article. The bytes
// stay in Storage; URL is the signed download link when one was issued.
type Attachment struct {
	ID   int64
	Name string
	Size int64
	Mime string
	// Source is the Storage channel the file was uploaded through.
	Source    string
	URL       string
	CreatedAt time.Time
	CreatedBy *Lookup
}
