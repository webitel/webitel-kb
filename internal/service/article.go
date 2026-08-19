package service

import (
	"bytes"
	"context"
	"strings"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/model/options"
	"github.com/webitel/webitel-kb/internal/store"
	"github.com/webitel/webitel-kb/internal/store/util"
	"github.com/webitel/webitel-kb/pkg/bodyconv"
)

// noteLimit bounds the restore note.
const noteLimit = 50

// ArticleService owns the article business rules: input validation, body
// conversion, the versioned save flow and hierarchy validation with honest
// errors.
type ArticleService struct {
	uow store.UnitOfWork
}

func NewArticleService(uow store.UnitOfWork) *ArticleService {
	return &ArticleService{uow: uow}
}

// Every article response carries an etag built from the identifier and the
// version, and the save flow needs the identifier to attach a version.
var requiredArticleFields = []string{"id", "ver"}

// mergeReadFields is what the locked read must carry for the merge: the store
// rewrites every column, so an unread one would be written back empty.
var mergeReadFields = []string{
	"id", "space", "parent_id", "subject", "tags", "type", "state", "ver",
}

// createOptions and updateOptions widen the caller's projection to what the
// flow needs, leaving the rest untouched.
type createOptions struct {
	options.Creator

	fields []string
}

func (o createOptions) GetFields() []string { return o.fields }

type updateOptions struct {
	options.Updator

	fields []string
}

func (o updateOptions) GetFields() []string { return o.fields }

type deleteOptions struct {
	options.Deleter

	fields []string
}

func (o deleteOptions) GetFields() []string { return o.fields }

type searchOptions struct {
	options.Searcher

	fields []string
}

func (o searchOptions) GetFields() []string { return o.fields }

// articleRead widens a read that returns articles: the response etag is built
// from the identifier and the version.
func articleRead(opts options.Searcher) options.Searcher {
	return searchOptions{Searcher: opts, fields: withRequired(opts.GetFields())}
}

// withRequired appends the fields the flow needs to the caller's selection. An
// empty selection is left alone: the store then applies its own defaults,
// which already carry them.
func withRequired(fields []string) []string {
	asked := util.InlineFields(fields)
	if len(asked) == 0 {
		return nil
	}

	return util.DeduplicateFields(append(asked, requiredArticleFields...))
}

// articleBody is a converted editor document ready to become a version.
type articleBody struct {
	raw      []byte
	markdown string
	plain    string
}

func (s *ArticleService) List(
	ctx context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	return s.uow.ArticleStore().List(ctx, articleRead(opts), filter)
}

func (s *ArticleService) Locate(ctx context.Context, opts options.Searcher) (*model.Article, error) {
	return s.uow.ArticleStore().Locate(ctx, articleRead(opts))
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

	if strings.TrimSpace(in.Subject) == "" {
		return nil, errors.InvalidArgument(
			"a subject is required",
			errors.WithID("kb.article.subject_required"),
		)
	}

	if err := validateArticleCodes(in.Type, in.State); err != nil {
		return nil, err
	}

	in.Subject = strings.TrimSpace(in.Subject)
	in.Tags = normalizeTags(in.Tags)

	var body *articleBody

	if len(rawBody) > 0 {
		var err error

		body, err = convertBody(rawBody)
		if err != nil {
			return nil, err
		}
	}

	if in.ParentID != 0 {
		if err := s.checkCreateDepth(ctx, opts, in.ParentID); err != nil {
			return nil, err
		}
	}

	var created *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		var err error

		created, err = tx.ArticleStore().Create(ctx, createOptions{
			Creator: opts, fields: withRequired(opts.GetFields()),
		}, in)
		if err != nil {
			return err
		}

		if body == nil {
			return nil
		}

		_, err = tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    created.ID,
			Subject:      in.Subject,
			BodyRichText: body.raw,
			BodyMarkdown: body.markdown,
			BodyPlain:    body.plain,
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

	var body *articleBody

	if len(rawBody) > 0 {
		var err error

		body, err = convertBody(rawBody)
		if err != nil {
			return nil, err
		}
	}

	session := opts.GetAuthOpts()

	var updated *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: mergeReadFields,
		})
		if err != nil {
			return err
		}

		if err := rejectHierarchyInput(in, current); err != nil {
			return err
		}

		merged := mergeArticle(in, current)

		updated, err = tx.ArticleStore().Update(ctx, updateOptions{
			Updator: opts, fields: withRequired(opts.GetFields()),
		}, merged, expectedVer)
		if err != nil {
			return err
		}

		if body == nil {
			return nil
		}

		_, err = tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    opts.GetID(),
			Subject:      merged.Subject,
			BodyRichText: body.raw,
			BodyMarkdown: body.markdown,
			BodyPlain:    body.plain,
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
	return s.uow.ArticleStore().Delete(ctx, deleteOptions{
		Deleter: opts, fields: withRequired(opts.GetFields()),
	}, expectedVer)
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
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space", "depth"},
		})
		if err != nil {
			return err
		}

		if err := tx.ArticleStore().AcquireSpaceMoveLock(ctx, current.SpaceID); err != nil {
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

		moved, err = tx.ArticleStore().Move(ctx, updateOptions{
			Updator: opts, fields: withRequired(opts.GetFields()),
		}, newParentID, expectedVer)

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
	return s.uow.ArticleStore().Ancestors(ctx, articleRead(opts), articleID)
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
	notes = strings.TrimSpace(notes)
	if len([]rune(notes)) > noteLimit {
		notes = string([]rune(notes)[:noteLimit])
	}

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

		merged := *current
		merged.Subject = source.Subject

		_, err = tx.ArticleStore().Update(ctx, updateOptions{
			Updator: opts, fields: requiredArticleFields,
		}, &merged, current.Ver)

		return err
	})
	if err != nil {
		return nil, err
	}

	return restored, nil
}

// checkCreateDepth turns the depth ceiling into an honest error before the
// schema constraint would report it as a generic conflict.
func (s *ArticleService) checkCreateDepth(ctx context.Context, opts options.Creator, parentID int64) error {
	parent, err := s.uow.ArticleStore().Locate(ctx, readOptions{
		auth: opts.GetAuthOpts(), ids: []int64{parentID}, fields: []string{"id", "depth"},
	})
	if err != nil {
		return err
	}

	if parent.Depth+1 > model.MaxArticleDepth {
		return errors.InvalidArgument(
			"maximum hierarchy depth is 5",
			errors.WithID("kb.article.create_depth"),
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

	target, err := tx.ArticleStore().Locate(ctx, readOptions{
		auth: session, ids: []int64{newParentID}, fields: []string{"id", "depth"},
	})
	if err != nil {
		return err
	}

	if target.Depth+1+height > model.MaxArticleDepth {
		return errors.InvalidArgument(
			"maximum hierarchy depth is 5",
			errors.WithID("kb.article.move_depth"),
		)
	}

	return nil
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

// mergeArticle overlays the set input fields on the stored article: the store
// writes every column unconditionally, so unset input must not erase state.
func mergeArticle(in, current *model.Article) *model.Article {
	merged := *current

	if subject := strings.TrimSpace(in.Subject); subject != "" {
		merged.Subject = subject
	}

	if in.Type != 0 {
		merged.Type = in.Type
	}

	if in.State != 0 {
		merged.State = in.State
	}

	if in.Tags != nil {
		merged.Tags = normalizeTags(in.Tags)
	}

	return &merged
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

// convertBody turns a non-empty editor document into its stored
// representations. NUL is checked on the decoded output: the wire encoding
// escapes it, so raw bytes never show it.
func convertBody(raw []byte) (*articleBody, error) {
	result, err := bodyconv.Convert(raw)
	if err != nil {
		return nil, errors.InvalidArgument(
			"the body is not a valid editor document",
			errors.WithID("kb.article.body_invalid"),
			errors.WithCause(err),
		)
	}

	if strings.Contains(result.Plain, "\x00") || strings.Contains(result.Markdown, "\x00") ||
		containsNulEscape(raw) {
		return nil, errBodyInvalid
	}

	return &articleBody{raw: raw, markdown: result.Markdown, plain: result.Plain}, nil
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
