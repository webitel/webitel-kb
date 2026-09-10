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
