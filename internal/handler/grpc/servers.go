package grpc

import (
	"github.com/webitel/webitel-kb/api/kb"
)

type AttachmentsServer struct {
	kb.UnimplementedAttachmentsServer
}

func NewAttachmentsServer() *AttachmentsServer {
	return &AttachmentsServer{}
}

type RetrievalServer struct {
	kb.UnimplementedRetrievalServer
}

func NewRetrievalServer() *RetrievalServer {
	return &RetrievalServer{}
}
