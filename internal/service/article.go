package service

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/event"
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

// errSubjectRequired rejects an article left without a subject.
var errSubjectRequired = errors.InvalidArgument(
	"a subject is required",
	errors.WithID("kb.article.subject_required"),
)

// mergeReadFields is what the locked read must carry for the merge: the store
// rewrites every column, so an unread one would be written back empty.
var mergeReadFields = []string{
	"id", "space", "parent_id", "subject", "tags", "type", "state", "ver", "home",
}

// reindexReadFields adds the indexing state the reindex checks to the merge read.
var reindexReadFields = append(slices.Clone(mergeReadFields), "index_state")

// errStateTransition refuses to move an article back to draft.
var errStateTransition = errors.InvalidArgument(
	"an article cannot return to draft",
	errors.WithID("kb.article.state_transition"),
)

// The home page is a top-level article of type article outside the tree: it
// takes no parent and no children and does not move.
var (
	errHomeNested = errors.InvalidArgument(
		"the home page is a top-level article",
		errors.WithID("kb.article.home_nested"),
	)
	errHomeType = errors.InvalidArgument(
		"the home page is an article, not a FAQ",
		errors.WithID("kb.article.home_type"),
	)
	errHomeParent = errors.InvalidArgument(
		"the home page holds no articles",
		errors.WithID("kb.article.home_parent"),
	)
	errHomeMove = errors.InvalidArgument(
		"the home page does not move",
		errors.WithID("kb.article.home_move"),
	)
	errMoveLanguage = errors.InvalidArgument(
		"an article moves only to a space of the same language",
		errors.WithID("kb.article.move_language"),
	)
	errMoveParentSpace = errors.InvalidArgument(
		"the new parent belongs to another space",
		errors.WithID("kb.article.move_parent_space"),
	)
	errMovedMeanwhile = errors.Aborted(
		"the article moved to another space meanwhile",
		errors.WithID("kb.article.space_changed"),
	)
	errHomeImmutable = errors.InvalidArgument(
		"an article becomes the home page only on create",
		errors.WithID("kb.article.home_immutable"),
	)
	errHomeExists = errors.New(
		"the space has a home page already",
		errors.WithCode(codes.FailedPrecondition),
		errors.WithID("kb.article.home_exists"),
	)
)

var errReindexNotFailed = errors.InvalidArgument(
	"only an article that failed indexing can be reindexed",
	errors.WithID("kb.article.reindex_not_failed"),
)

var errReindexNoVersion = errors.InvalidArgument(
	"the article has no version to index",
	errors.WithID("kb.article.no_version"),
)

// A list never carries bodies: a page of them outgrows a response, and a body
// is read one item at a time.
var (
	articleBodyFields = []string{"published"}
	versionBodyFields = []string{"body_rich_text", "body_markdown", "body_plain"}
)

// versionListFields is what the history carries when the caller asks for nothing.
var versionListFields = []string{
	"id", "article_id", "version_number", "subject", "restored_from", "notes",
	"created_at", "created_by",
}

// listOptions are list options with the requested fields replaced.
type listOptions struct {
	options.Searcher

	fields []string
}

func (o listOptions) GetFields() []string { return o.fields }

// withoutBodies drops the body fields from the request; fallback applies when
// nothing is left.
func withoutBodies(opts options.Searcher, bodies, fallback []string) options.Searcher {
	fields := slices.DeleteFunc(slices.Clone(opts.GetFields()), func(field string) bool {
		return slices.Contains(bodies, field)
	})
	if len(fields) == 0 {
		fields = fallback
	}

	return listOptions{Searcher: opts, fields: fields}
}

func (s *ArticleService) List(
	ctx context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	return s.uow.ArticleStore().List(ctx, withoutBodies(opts, articleBodyFields, nil), filter)
}

func (s *ArticleService) Locate(ctx context.Context, opts options.Searcher) (*model.Article, error) {
	return s.uow.ArticleStore().Locate(ctx, opts)
}

// Create inserts an article and, when a body came along, its first version in
// the same transaction.
func (s *ArticleService) Create(
	ctx context.Context, opts options.Creator, in *model.Article, rawBody []byte,
) (*model.Article, error) {
	normalizeInput(in)

	if in.Subject == "" {
		return nil, errSubjectRequired
	}

	if err := validateHome(in); err != nil {
		return nil, err
	}

	body, err := s.convertBody(ctx, rawBody)
	if err != nil {
		return nil, err
	}

	session := opts.GetAuthOpts()

	var created *model.Article

	err = s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		if err := tx.ArticleStore().LockSpaceKey(ctx, in.SpaceID, session.GetDomainID()); err != nil {
			return err
		}

		if in.ParentID != 0 {
			if _, err := checkDepth(ctx, tx, session, in.ParentID, 0, "kb.article.create_depth"); err != nil {
				return err
			}
		}

		var err error

		created, err = tx.ArticleStore().Create(ctx, opts, in)
		if err != nil {
			return err
		}

		if in.Home {
			claimed, err := tx.SpaceStore().SetHome(ctx, in.SpaceID, created.ID, session.GetDomainID())
			if err != nil {
				return err
			}

			if !claimed {
				return errHomeExists
			}

			created.Home = true
		}

		if len(rawBody) == 0 {
			return nil
		}

		version, err := tx.ArticleVersionStore().Create(ctx, opts, &model.ArticleVersion{
			ArticleID:    created.ID,
			Subject:      in.Subject,
			BodyRichText: rawBody,
			BodyMarkdown: body.Markdown,
			BodyPlain:    body.Plain,
		})
		if err != nil {
			return err
		}

		return requestReindex(ctx, tx, session, created.ID, version.ID, in.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// Update merges the input over the stored article under a row lock, rewrites
// it guarded by the client's version, and appends a version when the body or
// the subject changes: the subject is part of the indexed content. Other
// metadata changes record no version.
func (s *ArticleService) Update(
	ctx context.Context, opts options.Updator, in *model.Article, rawBody []byte, expectedVer int32,
) (*model.Article, error) {
	normalizeInput(in)

	if in.Subject == "" && slices.Contains(opts.GetMask(), "subject") {
		return nil, errSubjectRequired
	}

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

		if err := rejectImmutableInput(in, current); err != nil {
			return err
		}

		merged := current.Merge(in, opts.GetMask())

		if !model.CanChangeState(current.State, merged.State) {
			return errStateTransition
		}

		if merged.Home && merged.Type == model.ArticleTypeFAQ {
			return errHomeType
		}

		var next *model.ArticleVersion

		switch {
		case len(rawBody) != 0:
			next = &model.ArticleVersion{
				ArticleID:    current.ID,
				Subject:      merged.Subject,
				BodyRichText: rawBody,
				BodyMarkdown: body.Markdown,
				BodyPlain:    body.Plain,
			}
		case merged.Subject != current.Subject:
			retitled, ok, err := retitleLatest(ctx, tx, session, current.ID, merged.Subject)
			if err != nil {
				return err
			}

			if ok {
				next = &retitled
			}
		}

		// New content invalidates what the pipeline indexed.
		if next != nil {
			merged.IndexState = model.IndexStatePending
		}

		updated, err = tx.ArticleStore().Update(ctx, opts, merged, expectedVer)
		if err != nil {
			return err
		}

		if next == nil {
			return nil
		}

		version, err := tx.ArticleVersionStore().Create(ctx, opts, next)
		if err != nil {
			return err
		}

		return requestReindex(ctx, tx, session, opts.GetID(), version.ID, current.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// retitleLatest copies the latest version under a new subject; ok is false for
// an article without versions.
func retitleLatest(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, articleID int64, subject string,
) (model.ArticleVersion, bool, error) {
	// The version store lists the newest version first.
	latest, _, err := tx.ArticleVersionStore().List(ctx, readOptions{
		auth: session, fields: []string{"id", "body_rich_text", "body_markdown", "body_plain"},
	}, articleID)
	if err != nil || len(latest) == 0 {
		return model.ArticleVersion{}, false, err
	}

	return model.ArticleVersion{
		ArticleID:    articleID,
		Subject:      subject,
		BodyRichText: latest[0].BodyRichText,
		BodyMarkdown: latest[0].BodyMarkdown,
		BodyPlain:    latest[0].BodyPlain,
	}, true, nil
}

func (s *ArticleService) Delete(
	ctx context.Context, opts options.Deleter, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	var deleted *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		located, err := tx.ArticleStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space"},
		})
		if err != nil {
			return err
		}

		if err := tx.ArticleStore().LockSpaceTree(ctx, located.SpaceID, session.GetDomainID()); err != nil {
			return err
		}

		if err := requireSameSpace(ctx, tx, session, located); err != nil {
			return err
		}

		deleted, err = tx.ArticleStore().Delete(ctx, opts, expectedVer)

		return err
	})
	if err != nil {
		return nil, err
	}

	return deleted, nil
}

// Move reparents an article, into another space of the same language when
// newSpaceID names one. The whole flow runs in one transaction and moves
// within a space are serialized: two concurrent moves validated against their
// own snapshots could otherwise weave a cycle neither of them sees.
func (s *ArticleService) Move(
	ctx context.Context, opts options.Updator, newParentID, newSpaceID int64, expectedVer int32,
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

		targetSpaceID := located.SpaceID
		if newSpaceID != 0 {
			targetSpaceID = newSpaceID
		}

		if err := lockSpaceTrees(ctx, tx, session, located.SpaceID, targetSpaceID); err != nil {
			return err
		}

		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space", "depth", "home"},
		})
		if err != nil {
			return err
		}

		if current.SpaceID != located.SpaceID {
			return errMovedMeanwhile
		}

		if current.Home {
			return errHomeMove
		}

		var source, target *model.Space

		if targetSpaceID != current.SpaceID {
			source, target, err = moveSpaces(ctx, tx, session, current.SpaceID, targetSpaceID)
			if err != nil {
				return err
			}
		}

		subtree, err := tx.ArticleStore().Subtree(ctx, readOptions{auth: session}, current.ID)
		if err != nil {
			return err
		}

		if newParentID != 0 {
			if err := validateMoveTarget(ctx, tx, session, current, subtree, newParentID, targetSpaceID); err != nil {
				return err
			}
		}

		moved, err = tx.ArticleStore().Move(ctx, opts, newParentID, targetSpaceID, expectedVer)
		if err != nil {
			return err
		}

		if target == nil {
			return nil
		}

		return moveIndex(ctx, tx, session, source, target, current.ID, subtreeIDs(subtree))
	})
	if err != nil {
		return nil, err
	}

	return moved, nil
}

// requireSameSpace refuses an article a move carried into another space
// after it was read: the tree lock taken is then not the one of its space.
func requireSameSpace(ctx context.Context, tx store.UnitOfWork, session auth.Auther, located *model.Article) error {
	now, err := tx.ArticleStore().Locate(ctx, readOptions{
		auth: session, ids: []int64{located.ID}, fields: []string{"id", "space"},
	})
	if err != nil {
		return err
	}

	if now.SpaceID != located.SpaceID {
		return errMovedMeanwhile
	}

	return nil
}

// lockSpaceTrees takes the tree locks of the spaces a move touches in id
// order, so two moves between the same spaces in opposite directions cannot
// deadlock.
func lockSpaceTrees(ctx context.Context, tx store.UnitOfWork, session auth.Auther, spaceIDs ...int64) error {
	ids := slices.Clone(spaceIDs)
	slices.Sort(ids)

	for _, id := range slices.Compact(ids) {
		if err := tx.ArticleStore().LockSpaceTree(ctx, id, session.GetDomainID()); err != nil {
			return err
		}
	}

	return nil
}

// moveSpaces reads both spaces of a move between them and refuses spaces of
// different languages.
func moveSpaces(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, sourceID, targetID int64,
) (*model.Space, *model.Space, error) {
	fields := []string{"id", "language", "embedding_model_id", "vector_search_enabled"}

	source, err := tx.SpaceStore().Locate(ctx, readOptions{auth: session, ids: []int64{sourceID}, fields: fields})
	if err != nil {
		return nil, nil, err
	}

	target, err := tx.SpaceStore().Locate(ctx, readOptions{auth: session, ids: []int64{targetID}, fields: fields})
	if err != nil {
		return nil, nil, err
	}

	if model.BaseLanguage(source.Language) != model.BaseLanguage(target.Language) {
		return nil, nil, errMoveLanguage
	}

	return source, target, nil
}

// moveIndex carries the vectors of the moved branch over to the target space.
// Vectors of the model the target keeps only change their scope; the rest of
// the live articles go, and a change of the search model queues them again.
func moveIndex(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	source, target *model.Space, rootID int64, articleIDs []int64,
) error {
	if err := tx.SpaceStore().RescopeEmbeddings(ctx, rootID, session.GetDomainID()); err != nil {
		return err
	}

	if err := tx.SpaceStore().DropForeignEmbeddings(
		ctx, target.ID, session.GetDomainID(), target.EmbeddingModelID, articleIDs,
	); err != nil {
		return err
	}

	if source.SearchModelID() == target.SearchModelID() {
		return nil
	}

	return requestSpaceReindex(ctx, tx, session, target.ID, articleIDs)
}

func subtreeIDs(subtree []model.SubtreeNode) []int64 {
	ids := make([]int64, 0, len(subtree))
	for _, node := range subtree {
		ids = append(ids, node.ID)
	}

	return ids
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
	if size > options.MaxSearchSize {
		size = options.MaxSearchSize
	}

	return s.uow.ArticleStore().SuggestTags(ctx, opts, spaceID, prefix, size)
}

func (s *ArticleService) ListVersions(
	ctx context.Context, opts options.Searcher, articleID int64,
) ([]*model.ArticleVersion, bool, error) {
	return s.uow.ArticleVersionStore().List(ctx, withoutBodies(opts, versionBodyFields, versionListFields), articleID)
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
		})
		if err != nil {
			return err
		}

		aligned := current.Merge(&model.Article{Subject: source.Subject}, nil)
		aligned.IndexState = model.IndexStatePending

		if _, err := tx.ArticleStore().Update(ctx, opts, aligned, current.Ver); err != nil {
			return err
		}

		return requestReindex(ctx, tx, session, articleID, restored.ID, current.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return restored, nil
}

// Reindex queues the latest version of a failed article for indexing again.
func (s *ArticleService) Reindex(
	ctx context.Context, opts options.Updator, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	var queued *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: reindexReadFields,
		})
		if err != nil {
			return err
		}

		if current.IndexState != model.IndexStateFailed {
			return errReindexNotFailed
		}

		// The version store lists the newest version first.
		versions, _, err := tx.ArticleVersionStore().List(ctx, readOptions{
			auth: session, fields: []string{"id"},
		}, current.ID)
		if err != nil {
			return err
		}

		if len(versions) == 0 {
			return errReindexNoVersion
		}

		pending := *current
		pending.IndexState = model.IndexStatePending

		queued, err = tx.ArticleStore().Update(ctx, opts, &pending, expectedVer)
		if err != nil {
			return err
		}

		return requestReindex(ctx, tx, session, current.ID, versions[0].ID, current.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return queued, nil
}

func requestReindex(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	articleID, versionID, spaceID int64,
) error {
	return tx.OutboxStore().PublishReindex(ctx, reindexEvent(session, articleID, versionID, spaceID))
}

func reindexEvent(session auth.Auther, articleID, versionID, spaceID int64) event.ArticleReindex {
	return event.NewArticleReindex(time.Now(), articleID, versionID, spaceID, session.GetDomainID())
}

// checkDepth refuses a placement past the depth ceiling; height is how far the
// placed subtree reaches below its own root.
func checkDepth(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	parentID int64, height int32, errID string,
) (*model.Article, error) {
	parent, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
		auth: session, ids: []int64{parentID}, fields: []string{"id", "space", "depth", "home"},
	})
	if err != nil {
		return nil, err
	}

	if parent.Home {
		return nil, errHomeParent
	}

	if parent.Depth+1+height > model.MaxArticleDepth {
		return nil, errors.InvalidArgument(
			"maximum hierarchy depth is 5",
			errors.WithID(errID),
		)
	}

	return parent, nil
}

// validateMoveTarget explains a refusal the guarded write would only report
// generically: a move under the own subtree, past the depth ceiling or under
// an article of another space.
func validateMoveTarget(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	current *model.Article, subtree []model.SubtreeNode, newParentID, targetSpaceID int64,
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

	parent, err := checkDepth(ctx, tx, session, newParentID, height, "kb.article.move_depth")
	if err != nil {
		return err
	}

	if parent.SpaceID != targetSpaceID {
		return errMoveParentSpace
	}

	return nil
}

// rejectImmutableInput refuses update fields an update may not change: the
// hierarchy changes through MoveArticle, the space, the type and the home
// page not at all.
func rejectImmutableInput(in, current *model.Article) error {
	if in.Home && !current.Home {
		return errHomeImmutable
	}

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

	if in.Type != 0 && in.Type != current.Type {
		return errors.InvalidArgument(
			"an article cannot change its type",
			errors.WithID("kb.article.type_immutable"),
		)
	}

	return nil
}

// validateHome keeps a new home page a top-level article of type article.
func validateHome(in *model.Article) error {
	if !in.Home {
		return nil
	}

	if in.ParentID != 0 {
		return errHomeNested
	}

	if in.Type == model.ArticleTypeFAQ {
		return errHomeType
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

	if len(raw) > model.MaxBodyBytes {
		return bodyconv.Result{}, errors.InvalidArgument(
			"the body exceeds the size limit",
			errors.WithID("kb.article.body_too_large"),
			errors.WithValue("limit", model.MaxBodyBytes),
			errors.WithValue("size", len(raw)),
		)
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
