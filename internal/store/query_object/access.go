package queryobject

import (
	"fmt"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
)

// Rule tables of the classes.
const (
	spaceACL   = "kb.space_acl"
	articleACL = "kb.article_acl"
)

// changeModes open to the caller the drafts their rules allow.
var changeModes = []auth.AccessMode{auth.Edit, auth.Delete}

// DraftRule says how a read treats drafts.
type DraftRule int

const (
	// DraftsAll keeps drafts.
	DraftsAll DraftRule = iota
	// DraftsNone hides drafts: the caller may only select.
	DraftsNone
	// DraftsByArticle keeps the drafts the article rules let the caller change.
	DraftsByArticle
	// DraftsBySpace keeps the drafts the space rules let the caller change.
	DraftsBySpace
)

// AccessPlan is the set of access checks one statement applies.
type AccessPlan struct {
	Roles []int64
	Mode  auth.AccessMode
	// Space checks the rules of the space in SpaceMode.
	Space     bool
	SpaceMode auth.AccessMode
	// Article checks the rules of the article in Mode.
	Article bool
	Drafts  DraftRule
}

// PlanArticleAccess plans the checks of an article read or change in mode.
// With article rules the space is checked for reading; without them the space
// rules stand for the articles in the same mode.
func PlanArticleAccess(session auth.Auther, mode auth.AccessMode) AccessPlan {
	if mode == auth.NONE {
		mode = auth.Read
	}

	plan := AccessPlan{
		Roles:     session.GetRoles(),
		Mode:      mode,
		Article:   session.IsRbacCheckRequired(model.ScopeArticles, mode),
		SpaceMode: mode,
	}

	if plan.Article {
		plan.SpaceMode = auth.Read
	}

	plan.Space = session.IsRbacCheckRequired(model.ScopeSpaces, plan.SpaceMode)

	if mode == auth.Read {
		plan.Drafts = draftRule(session)
	}

	return plan
}

// PlanSpaceAccess plans the checks of a space read or change in mode.
func PlanSpaceAccess(session auth.Auther, mode auth.AccessMode) AccessPlan {
	if mode == auth.NONE {
		mode = auth.Read
	}

	return AccessPlan{
		Roles:     session.GetRoles(),
		Mode:      mode,
		Space:     session.IsRbacCheckRequired(model.ScopeSpaces, mode),
		SpaceMode: mode,
	}
}

// draftRule says which drafts a read keeps. The create right keeps the drafts
// of every article read, as record rules carry no create column; edit and
// delete keep those their rules allow.
func draftRule(session auth.Auther) DraftRule {
	if session.CheckObacAccess(model.ScopeArticles, auth.Add) {
		return DraftsAll
	}

	canWrite, byArticle, bySpace := false, true, true

	for _, mode := range changeModes {
		if !session.CheckObacAccess(model.ScopeArticles, mode) {
			continue
		}

		canWrite = true
		byArticle = byArticle && session.IsRbacCheckRequired(model.ScopeArticles, mode)
		bySpace = bySpace && session.IsRbacCheckRequired(model.ScopeSpaces, mode)
	}

	switch {
	case !canWrite:
		return DraftsNone
	case byArticle:
		return DraftsByArticle
	case bySpace:
		return DraftsBySpace
	default:
		return DraftsAll
	}
}

// ArticlePredicate keeps the article rows of alias m; nil when nothing applies.
func (p AccessPlan) ArticlePredicate() squirrel.Sqlizer {
	parts := make(squirrel.And, 0, 3)

	if p.Space {
		parts = append(parts, aclHas(spaceACL, "m.space_id", p.Roles, p.SpaceMode))
	}

	if p.Article {
		parts = append(parts, aclHas(articleACL, "m.id", p.Roles, p.Mode))
	}

	if drafts := p.draftPredicate(); drafts != nil {
		parts = append(parts, drafts)
	}

	if len(parts) == 0 {
		return nil
	}

	return parts
}

func (p AccessPlan) draftPredicate() squirrel.Sqlizer {
	notDraft := squirrel.Expr("m.state <> ?", model.ArticleStateDraft)

	switch p.Drafts {
	case DraftsNone:
		return notDraft
	case DraftsByArticle:
		return squirrel.Or{notDraft, aclAny(articleACL, "m.id", p.Roles, changeModes)}
	case DraftsBySpace:
		return squirrel.Or{notDraft, aclAny(spaceACL, "m.space_id", p.Roles, changeModes)}
	case DraftsAll:
	}

	return nil
}

// SpacePredicate keeps the space rows of alias m; nil when nothing applies.
func (p AccessPlan) SpacePredicate() squirrel.Sqlizer {
	if !p.Space {
		return nil
	}

	return aclHas(spaceACL, "m.id", p.Roles, p.SpaceMode)
}

// aclHas keeps the objects whose rules grant every bit of mode to a role.
func aclHas(table, object string, roles []int64, mode auth.AccessMode) squirrel.Sqlizer {
	return squirrel.Expr(
		fmt.Sprintf("EXISTS (SELECT 1 FROM %s acl WHERE acl.object = %s"+
			" AND acl.subject = ANY(?::int8[]) AND acl.access & ? = ?)", table, object),
		roles, int16(mode), int16(mode),
	)
}

// aclAny keeps the objects whose rules grant any of modes to a role.
func aclAny(table, object string, roles []int64, modes []auth.AccessMode) squirrel.Sqlizer {
	var mask auth.AccessMode
	for _, mode := range modes {
		mask |= mode
	}

	return squirrel.Expr(
		fmt.Sprintf("EXISTS (SELECT 1 FROM %s acl WHERE acl.object = %s"+
			" AND acl.subject = ANY(?::int8[]) AND acl.access & ? <> 0)", table, object),
		roles, int16(mask),
	)
}
