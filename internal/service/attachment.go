package service

import (
	"context"
	"log/slog"
	"slices"

	"github.com/webitel/webitel-kb/infra/storage"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
)

// urlField is the attachment field carrying the signed download link.
const urlField = "url"

// AttachmentService lists and removes the Storage files bound to an article.
type AttachmentService struct {
	uow   store.UnitOfWork
	links storage.Links
	log   *slog.Logger
}

func NewAttachmentService(uow store.UnitOfWork, links storage.Links, log *slog.Logger) *AttachmentService {
	return &AttachmentService{uow: uow, links: links, log: log}
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

	links, err := s.links.Download(ctx, opts.GetAuthOpts().GetDomainID(), ids)
	if err != nil {
		s.log.WarnContext(ctx, "attachment links are not signed", slog.Any("error", err))

		return items, next, nil
	}

	for _, item := range items {
		item.URL = links[item.ID]
	}

	return items, next, nil
}

// Delete removes a file from the article.
func (s *AttachmentService) Delete(
	ctx context.Context, opts options.Deleter, articleID int64,
) (*model.Attachment, error) {
	return s.uow.AttachmentStore().Delete(ctx, opts, articleID)
}

// requested reports whether the field is part of the answer: an empty
// selection means every field.
func requested(fields []string, field string) bool {
	return len(fields) == 0 || slices.Contains(fields, field)
}
