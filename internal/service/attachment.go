package service

import (
	"context"
	"log/slog"
	"slices"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/infra/storage"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// urlField is the attachment field carrying the signed download link.
const urlField = "url"

// AttachmentService binds the files uploaded to Storage to articles. The bytes
// and their lifecycle stay in Storage; kb keeps the binding.
type AttachmentService struct {
	uow   store.UnitOfWork
	files storage.Files
	log   *slog.Logger
}

func NewAttachmentService(uow store.UnitOfWork, files storage.Files, log *slog.Logger) *AttachmentService {
	return &AttachmentService{uow: uow, files: files, log: log}
}

// List returns a page of files with their download links. A link that could
// not be signed stays empty.
func (s *AttachmentService) List(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.Attachment, bool, error) {
	items, next, err := s.uow.AttachmentStore().List(ctx, opts, articleID)
	if err != nil {
		return nil, false, err
	}

	if len(items) == 0 || !requested(opts.GetFields(), urlField) {
		return items, next, nil
	}

	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	links, err := s.files.Links(ctx, opts.GetAuthOpts().GetDomainID(), ids)
	if err != nil {
		s.log.WarnContext(ctx, "attachment links are not signed", slog.Any("error", err))

		return items, next, nil
	}

	for _, item := range items {
		item.URL = links[item.ID]
	}

	return items, next, nil
}

// Attach binds a file the caller uploaded to Storage. Its metadata is read
// from Storage rather than taken on trust, which also proves the file is there
// and belongs to the caller's domain.
func (s *AttachmentService) Attach(
	ctx context.Context, opts options.Creator, articleID, fileID int64,
) (*model.Attachment, error) {
	if fileID <= 0 {
		return nil, errors.InvalidArgument(
			"a file is required",
			errors.WithID("kb.attachment.file_required"),
		)
	}

	file, err := s.files.Describe(ctx, opts.GetAuthOpts().GetDomainID(), fileID)
	if err != nil {
		if errors.Code(err) == codes.NotFound {
			return nil, errors.NotFound(
				"file is not found in storage",
				errors.WithID("kb.attachment.file_not_found"),
				errors.WithCause(err),
			)
		}

		return nil, err
	}

	attached, err := s.uow.AttachmentStore().Attach(ctx, opts, articleID, &model.Attachment{
		ID:   file.ID,
		Name: file.Name,
		Mime: file.Mime,
		Size: file.Size,
	})
	if err != nil {
		return nil, err
	}

	attached.URL = file.URL

	return attached, nil
}

// Delete unbinds the file and, once no article binds it any more, asks Storage
// to remove it. The unbinding is what the article owner controls, so a removal
// Storage refuses is reported and left to its retention policy rather than
// failing the call.
func (s *AttachmentService) Delete(
	ctx context.Context, opts options.Deleter, articleID int64,
) (*model.Attachment, error) {
	files := s.uow.AttachmentStore()

	deleted, err := files.Delete(ctx, opts, articleID)
	if err != nil {
		return nil, err
	}

	bound, err := files.Bound(ctx, deleted.ID)
	if err != nil {
		return nil, err
	}

	if bound {
		return deleted, nil
	}

	if err := s.files.Remove(ctx, []int64{deleted.ID}); err != nil {
		s.log.WarnContext(ctx, "storage kept the file of an unbound attachment",
			slog.Int64("file_id", deleted.ID), slog.Any("error", err))
	}

	return deleted, nil
}

// requested reports whether the field is part of the answer: an empty
// selection means every field.
func requested(fields []string, field string) bool {
	return len(fields) == 0 || slices.Contains(fields, field)
}
