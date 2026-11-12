package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
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

// A save compares a new body with the latest version; a save without a body
// carries the latest one over.
var (
	versionCompareFields = []string{"id", "subject", "body_rich_text"}
	versionCarryFields   = []string{"id", "subject", "body_rich_text", "body_markdown", "body_plain"}
)

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
		"the new space is not the space of the new parent",
		errors.WithID("kb.article.move_parent_space"),
	)
	errMovedMeanwhile = errors.Aborted(
		"the article moved to another space meanwhile",
		errors.WithID("kb.article.space_changed"),
	)
	errMoveCreateDenied = errors.Forbidden(
		"a move to another space needs the right to create articles",
		errors.WithID("kb.article.move_create_denied"),
	)
	errParentMovedMeanwhile = errors.Aborted(
		"the new parent moved to another space meanwhile",
		errors.WithID("kb.article.parent_space_changed"),
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

var errRestoreDraft = errors.InvalidArgument(
	"a draft has no history to restore from",
	errors.WithID("kb.article.restore_draft"),
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
	articleBodyFields = []string{"published", "latest"}
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

// List narrows the spaces to the team binding and the language filter to
// base languages, so one tag finds the articles of every region of it.
func (s *ArticleService) List(
	ctx context.Context, opts options.Searcher, filter model.ArticleFilter,
) ([]*model.Article, bool, error) {
	bases, err := baseLanguages(filter.Languages)
	if err != nil {
		return nil, false, err
	}

	ids, reach, err := teamSpaces(ctx, s.uow.SpaceStore(), opts, filter.TeamID, filter.SpaceIDs)
	if err != nil {
		return nil, false, err
	}

	if !reach {
		return make([]*model.Article, 0), false, nil
	}

	filter.SpaceIDs = ids
	filter.Languages = bases

	return s.uow.ArticleStore().List(ctx, withoutBodies(opts, articleBodyFields, nil), filter)
}

// Locate reads an article; a team whose binding leaves its space out does not
// find it.
func (s *ArticleService) Locate(ctx context.Context, opts options.Searcher, teamID int64) (*model.Article, error) {
	bound, _, err := teamSpaces(ctx, s.uow.SpaceStore(), opts, teamID, nil)
	if err != nil {
		return nil, err
	}

	if len(bound) == 0 {
		return s.uow.ArticleStore().Locate(ctx, opts)
	}

	return s.uow.ArticleStore().LocateIn(ctx, opts, bound)
}

// Create inserts an article and, when a body came along, its first version in
// the same transaction. A draft waits for its publication to be indexed.
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

	if in.State == 0 {
		in.State = model.ArticleStateDraft
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
		} else if err := requireSpace(ctx, tx, session, in.SpaceID, auth.Edit); err != nil {
			return err
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

		if in.IsDraft() {
			return nil
		}

		return requestReindex(ctx, tx, session, created.ID, version.ID, in.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// Update merges the input over the stored article under a row lock, rewrites
// it guarded by the client's version, and saves a version when the body or the
// subject changes: the subject is part of the indexed content. A draft keeps
// one version, rewritten until it leaves draft and is indexed; a published
// article appends one and is indexed on every change. Other metadata changes
// record no version.
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
			auth: session, ids: []int64{opts.GetID()}, fields: mergeReadFields, access: auth.Edit,
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

		var latest *model.ArticleVersion

		if fields := versionReadFields(current, merged, rawBody); fields != nil {
			latest, err = latestVersion(ctx, tx, session, current.ID, fields...)
			if err != nil {
				return err
			}
		}

		next := changedContent(current.ID, latest, merged.Subject, rawBody, body)
		index := needsIndex(current, merged, latest, next)

		// Content to index invalidates what the pipeline indexed.
		if index {
			merged.IndexState = model.IndexStatePending
		}

		updated, err = tx.ArticleStore().Update(ctx, opts, merged, expectedVer)
		if err != nil {
			return err
		}

		saved := latest

		if next != nil {
			saved, err = saveVersion(ctx, tx, opts, current, latest, next)
			if err != nil {
				return err
			}
		}

		if !index {
			return nil
		}

		return requestReindex(ctx, tx, session, opts.GetID(), saved.ID, current.SpaceID)
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// versionReadFields is what a save needs from the latest version, nil when a
// change of metadata needs nothing.
func versionReadFields(current, merged *model.Article, rawBody []byte) []string {
	switch {
	case len(rawBody) == 0 && merged.Subject != current.Subject:
		return versionCarryFields
	case len(rawBody) != 0 || (current.IsDraft() && !merged.IsDraft()):
		return versionCompareFields
	default:
		return nil
	}
}

// needsIndex reports whether a save leaves a published article with content
// the index has not seen yet.
func needsIndex(current, merged *model.Article, latest, next *model.ArticleVersion) bool {
	switch {
	case merged.IsDraft():
		return false // a draft is indexed once it is published
	case next != nil:
		return true // new content
	default:
		return current.IsDraft() && latest != nil // a draft published as it is
	}
}

// latestVersion returns the newest version of an article, nil without versions.
func latestVersion(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, articleID int64, fields ...string,
) (*model.ArticleVersion, error) {
	// The version store lists the newest version first.
	versions, _, err := tx.ArticleVersionStore().List(ctx, readOptions{auth: session, fields: fields}, articleID)
	if err != nil || len(versions) == 0 {
		return nil, err
	}

	return versions[0], nil
}

// changedContent builds the version a save holds, nil when the subject and the
// document stay as the latest version has them. A save without a body keeps
// the latest one.
func changedContent(
	articleID int64, latest *model.ArticleVersion, subject string, rawBody []byte, body bodyconv.Result,
) *model.ArticleVersion {
	next := &model.ArticleVersion{
		ArticleID:    articleID,
		Subject:      subject,
		BodyRichText: rawBody,
		BodyMarkdown: body.Markdown,
		BodyPlain:    body.Plain,
	}

	if latest == nil {
		if len(rawBody) == 0 {
			return nil
		}

		return next
	}

	if len(rawBody) == 0 {
		next.BodyRichText = latest.BodyRichText
		next.BodyMarkdown = latest.BodyMarkdown
		next.BodyPlain = latest.BodyPlain
	}

	if subject == latest.Subject && sameDocument(next.BodyRichText, latest.BodyRichText) {
		return nil
	}

	return next
}

// sameDocument compares two editor documents by value: jsonb keeps its own key
// order.
func sameDocument(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}

	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}

	return reflect.DeepEqual(left, right)
}

// saveVersion rewrites the version of a draft and appends one otherwise.
func saveVersion(
	ctx context.Context, tx store.UnitOfWork, opts options.Updator, current *model.Article, latest, next *model.ArticleVersion,
) (*model.ArticleVersion, error) {
	if current.IsDraft() && latest != nil {
		next.ID = latest.ID

		return tx.ArticleVersionStore().Rewrite(ctx, opts, next)
	}

	return tx.ArticleVersionStore().Create(ctx, opts, next)
}

func (s *ArticleService) Delete(
	ctx context.Context, opts options.Deleter, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	var deleted *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		located, err := tx.ArticleStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space"}, access: auth.Delete,
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

		subtree, err := tx.ArticleStore().Subtree(ctx, readOptions{auth: session}, located.ID)
		if err != nil {
			return err
		}

		if err := requireBranch(ctx, tx, session, subtree, auth.Delete); err != nil {
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

// Move reparents an article. The target space is the one of the new parent,
// or newSpaceID for a move to the top level; another space must have the same
// language and needs the right to create articles. The whole flow runs in one
// transaction and moves within a space are serialized: two concurrent moves
// validated against their own snapshots could otherwise weave a cycle neither
// of them sees.
func (s *ArticleService) Move(
	ctx context.Context, opts options.Updator, newParentID, newSpaceID int64, expectedVer int32,
) (*model.Article, error) {
	session := opts.GetAuthOpts()

	var moved *model.Article

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		located, err := tx.ArticleStore().Locate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space"}, access: auth.Edit,
		})
		if err != nil {
			return err
		}

		targetSpaceID, err := moveTargetSpace(ctx, tx, session, located.SpaceID, newParentID, newSpaceID)
		if err != nil {
			return err
		}

		if targetSpaceID != located.SpaceID && !session.CheckObacAccess(session.GetMainObjClassName(), auth.Add) {
			return errMoveCreateDenied
		}

		if err := lockSpaceTrees(ctx, tx, session, located.SpaceID, targetSpaceID); err != nil {
			return err
		}

		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{opts.GetID()}, fields: []string{"id", "space", "depth", "home"}, access: auth.Edit,
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

		if err := requireBranch(ctx, tx, session, subtree, auth.Edit); err != nil {
			return err
		}

		if newParentID != 0 {
			if err := validateMoveTarget(ctx, tx, session, current, subtree, newParentID, targetSpaceID); err != nil {
				return err
			}
		} else if err := requireSpace(ctx, tx, session, targetSpaceID, auth.Edit); err != nil {
			return err
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

// moveTargetSpace resolves the space a move goes to: the one of the new
// parent, which newSpaceID may only repeat, else newSpaceID, else the current one.
func moveTargetSpace(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther, currentSpaceID, newParentID, newSpaceID int64,
) (int64, error) {
	if newParentID == 0 {
		if newSpaceID != 0 {
			return newSpaceID, nil
		}

		return currentSpaceID, nil
	}

	parent, err := tx.ArticleStore().Locate(ctx, readOptions{
		auth: session, ids: []int64{newParentID}, fields: []string{"id", "space"},
	})
	if err != nil {
		return 0, err
	}

	if newSpaceID != 0 && newSpaceID != parent.SpaceID {
		return 0, errMoveParentSpace
	}

	return parent.SpaceID, nil
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

// Tree reads the hierarchy of a space; a team whose binding leaves the space
// out reads nothing.
func (s *ArticleService) Tree(
	ctx context.Context, opts options.Searcher, spaceID, teamID int64,
) ([]*model.TreeNode, error) {
	_, reach, err := teamSpaces(ctx, s.uow.SpaceStore(), opts, teamID, []int64{spaceID})
	if err != nil || !reach {
		return nil, err
	}

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
	if err := requireArticle(ctx, s.uow, opts.GetAuthOpts(), articleID, auth.Read); err != nil {
		return nil, false, err
	}

	return s.uow.ArticleVersionStore().List(ctx, withoutBodies(opts, versionBodyFields, versionListFields), articleID)
}

func (s *ArticleService) GetVersion(
	ctx context.Context, opts options.Searcher, articleID int64, number int32,
) (*model.ArticleVersion, error) {
	if err := requireArticle(ctx, s.uow, opts.GetAuthOpts(), articleID, auth.Read); err != nil {
		return nil, err
	}

	return s.uow.ArticleVersionStore().Locate(ctx, opts, articleID, number)
}

// RestoreVersion appends a new version carrying the content of a past one and
// aligns the article subject with it. The version guard uses the stored value:
// the contract carries no client version here. A draft has nothing to restore.
func (s *ArticleService) RestoreVersion(
	ctx context.Context, opts options.Updator, articleID int64, number int32, notes string,
) (*model.ArticleVersion, error) {
	session := opts.GetAuthOpts()

	var restored *model.ArticleVersion

	err := s.uow.WithinTransaction(ctx, func(ctx context.Context, tx store.UnitOfWork) error {
		current, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
			auth: session, ids: []int64{articleID}, fields: mergeReadFields, access: auth.Edit,
		})
		if err != nil {
			return err
		}

		if current.IsDraft() {
			return errRestoreDraft
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
			auth: session, ids: []int64{opts.GetID()}, fields: reindexReadFields, access: auth.Edit,
		})
		if err != nil {
			return err
		}

		if current.IndexState != model.IndexStateFailed {
			return errReindexNotFailed
		}

		latest, err := latestVersion(ctx, tx, session, current.ID, "id")
		if err != nil {
			return err
		}

		if latest == nil {
			return errReindexNoVersion
		}

		pending := *current
		pending.IndexState = model.IndexStatePending

		queued, err = tx.ArticleStore().Update(ctx, opts, &pending, expectedVer)
		if err != nil {
			return err
		}

		return requestReindex(ctx, tx, session, current.ID, latest.ID, current.SpaceID)
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

// requireArticle refuses an article the caller may not reach in mode.
func requireArticle(
	ctx context.Context, uow store.UnitOfWork, session auth.Auther, articleID int64, mode auth.AccessMode,
) error {
	_, err := uow.ArticleStore().Locate(ctx, readOptions{
		auth: session, ids: []int64{articleID}, fields: []string{"id"}, access: mode,
	})

	return err
}

// errBranchDenied refuses a change that would carry articles of the branch the
// caller may not change.
var errBranchDenied = errors.Forbidden(
	"the branch holds articles the rules do not let you change",
	errors.WithID("kb.article.branch_denied"),
)

// requireBranch refuses a branch holding an article the caller may not reach in mode.
func requireBranch(
	ctx context.Context, uow store.UnitOfWork, session auth.Auther, branch []model.SubtreeNode, mode auth.AccessMode,
) error {
	ids := make([]int64, 0, len(branch))
	for _, node := range branch {
		ids = append(ids, node.ID)
	}

	reached, err := uow.ArticleStore().Reachable(ctx, readOptions{auth: session, access: mode}, ids)
	if err != nil {
		return err
	}

	if len(reached) < len(ids) {
		return errBranchDenied
	}

	return nil
}

// requireSpace refuses a space the caller may not reach in mode.
func requireSpace(
	ctx context.Context, uow store.UnitOfWork, session auth.Auther, spaceID int64, mode auth.AccessMode,
) error {
	_, err := uow.SpaceStore().Locate(ctx, readOptions{
		auth: session, ids: []int64{spaceID}, fields: []string{"id"}, access: mode,
	})

	return err
}

// checkDepth refuses a placement past the depth ceiling; height is how far the
// placed subtree reaches below its own root.
func checkDepth(
	ctx context.Context, tx store.UnitOfWork, session auth.Auther,
	parentID int64, height int32, errID string,
) (*model.Article, error) {
	// The parent holds the placed article, so placing under it changes it.
	parent, err := tx.ArticleStore().LocateForUpdate(ctx, readOptions{
		auth: session, ids: []int64{parentID}, fields: []string{"id", "space", "depth", "home"}, access: auth.Edit,
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
// a parent that left the target space after it was read.
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
		return errParentMovedMeanwhile
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
