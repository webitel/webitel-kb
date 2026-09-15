package grpc

import (
	"context"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/etag"
	"github.com/webitel/webitel-kb/internal/handler/grpc/options"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/service"
)

// uploaderType marks the uploader lookup as a Webitel user.
const uploaderType = "webitel"

// AttachmentsServer handles the Attachments gRPC service: the Storage files
// bound to an article.
type AttachmentsServer struct {
	kb.UnimplementedAttachmentsServer

	service *service.AttachmentService
}

func NewAttachmentsServer(service *service.AttachmentService) *AttachmentsServer {
	return &AttachmentsServer{service: service}
}

func (s *AttachmentsServer) ListFiles(ctx context.Context, req *kb.ListFilesRequest) (*kb.FileList, error) {
	articleID, err := etag.ParseLocator(etag.TypeArticle, req.GetArticleEtag())
	if err != nil {
		return nil, err
	}

	opts, err := options.NewSearchOptions(ctx,
		options.WithPagination(req),
		options.WithFields(req),
		options.WithSort(req),
		options.WithSearch(req),
		options.WithIDs(req.GetIds()),
	)
	if err != nil {
		return nil, err
	}

	items, next, err := s.service.List(ctx, opts, articleID)
	if err != nil {
		return nil, err
	}

	files := make([]*kb.File, 0, len(items))
	for _, item := range items {
		files = append(files, fileToProto(item))
	}

	return &kb.FileList{Items: files, Next: next}, nil
}

func (s *AttachmentsServer) DeleteFile(ctx context.Context, req *kb.DeleteFileRequest) (*kb.File, error) {
	articleID, err := etag.ParseLocator(etag.TypeArticle, req.GetArticleEtag())
	if err != nil {
		return nil, err
	}

	opts, err := options.NewDeleteOptions(ctx, options.WithDeleteID(req.GetId()))
	if err != nil {
		return nil, err
	}

	deleted, err := s.service.Delete(ctx, opts, articleID)
	if err != nil {
		return nil, err
	}

	return fileToProto(deleted), nil
}

func fileToProto(in *model.Attachment) *kb.File {
	return &kb.File{
		Id:        in.ID,
		CreatedBy: uploaderToProto(in.CreatedBy),
		CreatedAt: unixMilli(in.CreatedAt),
		Size:      in.Size,
		Mime:      in.Mime,
		Name:      in.Name,
		Url:       in.URL,
		Source:    in.Source,
	}
}

func uploaderToProto(l *model.Lookup) *kb.ExtendedLookup {
	if l == nil {
		return nil
	}

	return &kb.ExtendedLookup{Id: l.ID, Name: l.Name, Type: uploaderType}
}
