package service

import (
	"bytes"
	"context"
	"log/slog"
	"strings"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	"github.com/webitel/webitel-kb/pkg/bodyconv"
)

// ArticleService owns the article business rules: input validation, body
// conversion, the versioned save flow and hierarchy validation with honest
// errors.
type ArticleService struct {
	uow store.UnitOfWork
	log *slog.Logger
}

func NewArticleService(uow store.UnitOfWork, log *slog.Logger) *ArticleService {
	return &ArticleService{uow: uow, log: log}
}

// mergeReadFields is what the locked read must carry for the merge: the store
// rewrites every column, so an unread one would be written back empty.
var mergeReadFields = []string{
	"id", "space", "parent_id", "subject", "tags", "type", "state", "ver",
}

func (s *ArticleService) List(
	ctx context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	return s.uow.ArticleStore().List(ctx, opts, filter)
}

func (s *ArticleService) Locate(ctx context.Context, opts options.Searcher) (*model.Article, error) {
	return s.uow.ArticleStore().Locate(ctx, opts)
}

// Create inserts an article and, when a body came along, its first version in
// the same transaction.
func (s *ArticleService) Create(
	ctx context.Context, opts options.Creator, in *model.Article, rawBody []byte,
) (*model.Article, error) {
	if in.SpaceID <= 0 {
		return nil, errors.InvalidArgument(
			"a space is required",
			errors.WithID("kb.article.space_required"),
		)
	}

	normalizeInput(in)

	if in.Subject == "" {
		return nil, errors.InvalidArgument(
			"a subject is required",
			errors.WithID("kb.article.subject_required"),
		)
	}

	if err := validateArticleCodes(in.Type, in.State); err != nil {
		return nil, err
	}

	body, err := s.convertBody(ctx, rawBody)
	if err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()

	var created *model.Article

	err = s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		if in.ParentID != 0 {
			if err := checkDepth(ctx, tx, session, in.ParentID, 0, "kb.article.create_depth"); err != nil {
				return err
			}
		}

		var err error

		created, err = tx.ArticleStore().Create(ctx, opts, in)
		if err != nil {
			return err
		}

		if len(rawBody) == 0 {
			return nil
		}

		_, err = tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    created.ID,
			Subject:      in.Subject,
			BodyRichText: rawBody,
			BodyMarkdown: body.Markdown,
			BodyPlain:    body.Plain,
		}, model.TextSearchDefault)

		return err
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// Update merges the input over the stored article under a row lock, rewrites
// it guarded by the client's version, and appends a version when a body came
// along. Without a body only the metadata change: no version records it.
func (s *ArticleService) Update(
	ctx context.Context, opts options.Updator, in *model.Article, rawBody []byte, expectedVer int32,
) (*model.Article, error) {
	if err := validateArticleCodes(in.Type, in.State); err != nil {
		return nil, err
	}

	normalizeInput(in)

	body, err := s.convertBody(ctx, rawBody)
	if err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()

	var updated *model.Article

	err = s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: mergeReadFields,
		})
		if err != nil {
			return err
		}

		if err := rejectHierarchyInput(in, current); err != nil {
			return err
		}

		merged := current.Merge(in)

		updated, err = tx.ArticleStore().Update(ctx, opts, merged, expectedVer)
		if err != nil {
			return err
		}

		if len(rawBody) == 0 {
			return nil
		}

		_, err = tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    opts.GetID(),
			Subject:      merged.Subject,
			BodyRichText: rawBody,
			BodyMarkdown: body.Markdown,
			BodyPlain:    body.Plain,
		}, model.TextSearchDefault)

		return err
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

func (s *ArticleService) Delete(
	ctx context.Context, opts options.Deleter, expectedVer int32,
) (*model.Article, error) {
	return s.uow.ArticleStore().Delete(ctx, opts, expectedVer)
}

// Move reparents an article. The whole flow runs in one transaction and moves
// within a space are serialized: two concurrent moves validated against their
// own snapshots could otherwise weave a cycle neither of them sees.
func (s *ArticleService) Move(
	ctx context.Context, opts options.Updator, newParentID int64, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	var moved *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		located, err := tx.ArticleStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space"},
		})
		if err != nil {
			return err
		}

		if err := tx.ArticleStore().AcquireSpaceMoveLock(ctx, located.SpaceID); err != nil {
			return err
		}

		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space", "depth"},
		})
		if err != nil {
			return err
		}

		subtree, err := tx.ArticleStore().Subtree(ctx, readOptions{auth: session}, current.ID)
		if err != nil {
			return err
		}

		if newParentID != 0 {
			if err := validateMoveTarget(ctx, tx, session, current, subtree, newParentID); err != nil {
				return err
			}
		}

		moved, err = tx.ArticleStore().Move(ctx, opts, newParentID, expectedVer)

		return err
	})
	if err != nil {
		return nil, err
	}

	return moved, nil
}

func (s *ArticleService) Ancestors(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.Article, error) {
	return s.uow.ArticleStore().Ancestors(ctx, opts, articleID)
}

func (s *ArticleService) Tree(
	ctx context.Context, opts options.Searcher, spaceID int64,
) ([]*model.TreeNode, error) {
	return s.uow.ArticleStore().Tree(ctx, opts, spaceID)
}

// SuggestTags completes a tag prefix within a space.
func (s *ArticleService) SuggestTags(
	ctx context.Context, opts options.Searcher, spaceID int64, prefix string, size int,
) ([]string, error) {
	if spaceID <= 0 {
		return nil, errors.InvalidArgument(
			"a space is required",
			errors.WithID("kb.article.space_required"),
		)
	}

	if size > options.MaxSearchSize {
		size = options.MaxSearchSize
	}

	return s.uow.ArticleStore().SuggestTags(ctx, opts, spaceID, prefix, size)
}

func (s *ArticleService) ListVersions(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.ArticleVersion, bool, error) {
	return s.uow.ArticleVersionStore().List(ctx, opts, articleID)
}

func (s *ArticleService) GetVersion(
	ctx context.Context, opts options.Searcher, articleID int64, number int32,
) (*model.ArticleVersion, error) {
	return s.uow.ArticleVersionStore().Locate(ctx, opts, articleID, number)
}

// RestoreVersion appends a new version carrying the content of a past one and
// aligns the article subject with it. The version guard uses the stored value:
// the contract carries no client version here.
func (s *ArticleService) RestoreVersion(
	ctx context.Context, opts options.Updator, articleID int64, number int32, notes string,
) (*model.ArticleVersion, error) {
	session := opts.GetAuthOpts()

	var restored *model.ArticleVersion

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{articleID}, fields: mergeReadFields,
		})
		if err != nil {
			return err
		}

		source, err := tx.ArticleVersionStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{articleID},
		}, articleID, number)
		if err != nil {
			return err
		}

		restored, err = tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    articleID,
			Subject:      source.Subject,
			BodyRichText: source.BodyRichText,
			BodyMarkdown: source.BodyMarkdown,
			BodyPlain:    source.BodyPlain,
			RestoredFrom: source.ID,
			Notes:        notes,
		}, model.TextSearchDefault)
		if err != nil {
			return err
		}

		_, err = tx.ArticleStore().Update(ctx, opts,
			current.Merge(&model.Article{Subject: source.Subject}), current.Ver)

		return err
	})
	if err != nil {
		return nil, err
	}

	return restored, nil
}

// checkDepth refuses a placement past the depth ceiling; height is how far the
// placed subtree reaches below its own root.
func checkDepth(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	parentID int64, height int32, errID string,
) error {
	parent, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
		auth: session, ids: []int64{parentID}, fields: []string{"id", "depth"},
	})
	if err != nil {
		return err
	}

	if parent.Depth+1+height > model.MaxArticleDepth {
		return errors.InvalidArgument(
			"maximum hierarchy depth is 5",
			errors.WithID(errID),
		)
	}

	return nil
}

// validateMoveTarget explains a refusal the guarded write would only report
// generically: a move under the own subtree and a move past the depth ceiling.
func validateMoveTarget(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	current *model.Article, subtree []model.SubtreeNode, newParentID int64,
) error {
	height := int32(0)

	for _, node := range subtree {
		if node.ID == newParentID {
			return errors.InvalidArgument(
				"an article cannot move under its own subtree",
				errors.WithID("kb.article.move_cycle"),
			)
		}

		if d := node.Depth - current.Depth; d > height {
			height = d
		}
	}

	return checkDepth(ctx, tx, session, newParentID, height, "kb.article.move_depth")
}

// validateArticleCodes accepts the known codes and the unset zero; anything
// else is a caller mistake the schema would report only generically.
func validateArticleCodes(typeCode, stateCode int32) error {
	if typeCode < 0 || typeCode > model.ArticleTypeFAQ {
		return errors.InvalidArgument(
			"unknown article type",
			errors.WithID("kb.article.code_invalid"),
		)
	}

	if stateCode < 0 || stateCode > model.ArticleStateInactive {
		return errors.InvalidArgument(
			"unknown article state",
			errors.WithID("kb.article.code_invalid"),
		)
	}

	return nil
}

// rejectHierarchyInput refuses update fields the write would silently ignore:
// the hierarchy changes through MoveArticle, the space not at all.
func rejectHierarchyInput(in, current *model.Article) error {
	if in.ParentID != 0 && in.ParentID != current.ParentID {
		return errors.InvalidArgument(
			"the parent changes through a move",
			errors.WithID("kb.article.parent_immutable"),
		)
	}

	if in.SpaceID != 0 && in.SpaceID != current.SpaceID {
		return errors.InvalidArgument(
			"an article cannot change its space",
			errors.WithID("kb.article.space_immutable"),
		)
	}

	return nil
}

// normalizeInput cleans the caller's input before it is stored or merged.
func normalizeInput(in *model.Article) {
	in.Subject = strings.TrimSpace(in.Subject)
	in.Tags = normalizeTags(in.Tags)
}

// normalizeTags trims, drops empties and deduplicates, keeping the order.
func normalizeTags(tags []string) []string {
	if tags == nil {
		return nil
	}

	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}

		seen[tag] = true

		out = append(out, tag)
	}

	return out
}

// convertBody turns an editor document into its stored representations and
// logs unknown nodes.
func (s *ArticleService) convertBody(ctx context.Context, raw []byte) (bodyconv.Result, error) {
	if len(raw) == 0 {
		return bodyconv.Result{}, nil
	}

	result, err := bodyconv.Convert(raw)
	if err != nil {
		return bodyconv.Result{}, errors.InvalidArgument(
			"the body is not a valid editor document",
			errors.WithID("kb.article.body_invalid"),
			errors.WithCause(err),
		)
	}

	if strings.Contains(result.Plain, "\x00") || strings.Contains(result.Markdown, "\x00") ||
		containsNulEscape(raw) {
		return bodyconv.Result{}, errBodyInvalid
	}

	if len(result.Unknown) > 0 {
		s.log.WarnContext(ctx, "article body carries unknown editor nodes",
			slog.Any("nodes", result.Unknown))
	}

	return result, nil
}

// nulEscape is how JSON spells a NUL, which jsonb refuses to store.
var nulEscape = []byte(`\u0000`)

// containsNulEscape reports a NUL the document really carries, including one
// hiding in a part the conversion drops. An even number of leading backslashes
// makes the sequence literal text, which stores fine.
func containsNulEscape(raw []byte) bool {
	for i := 0; i+len(nulEscape) <= len(raw); i++ {
		if !bytes.HasPrefix(raw[i:], nulEscape) {
			continue
		}

		slashes := 0
		for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
			slashes++
		}

		if slashes%2 == 0 {
			return true
		}
	}

	return false
}

var errBodyInvalid = errors.InvalidArgument(
	"the body is not a valid editor document",
	errors.WithID("kb.article.body_invalid"),
)
