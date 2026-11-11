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
	Domain int64
	Roles  []int64
	Mode   auth.AccessMode
	// Denied keeps no row: the caller has no rights on spaces at all.
	Denied bool
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
		Domain:    session.GetDomainID(),
		Roles:     session.GetRoles(),
		Mode:      mode,
		Article:   session.IsRbacCheckRequired(model.ScopeArticles, mode),
		SpaceMode: mode,
	}

	if plan.Article {
		plan.SpaceMode = auth.Read
	}

	plan.Space = session.IsRbacCheckRequired(model.ScopeSpaces, plan.SpaceMode)
	plan.Denied = withoutSpaces(session, plan.SpaceMode)

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
		Domain:    session.GetDomainID(),
		Roles:     session.GetRoles(),
		Mode:      mode,
		Denied:    withoutSpaces(session, mode),
		Space:     session.IsRbacCheckRequired(model.ScopeSpaces, mode),
		SpaceMode: mode,
	}
}

// superOf names the super permission that passes the rules of mode.
var superOf = map[auth.AccessMode]auth.SuperPermission{
	auth.Read:   auth.SuperSelectPermission,
	auth.Edit:   auth.SuperEditPermission,
	auth.Add:    auth.SuperCreatePermission,
	auth.Delete: auth.SuperDeletePermission,
}

// withoutSpaces reports a caller with no rights on spaces: without the scope
// the space rules are not checked, so such a caller reaches no space at all.
func withoutSpaces(session auth.Auther, mode auth.AccessMode) bool {
	if session.GetObjectScope(model.ScopeSpaces) != nil {
		return false
	}

	super, ok := superOf[mode]

	return !ok || !session.HasSuperPermission(super)
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
	if p.Denied {
		return squirrel.Expr("FALSE")
	}

	parts := make(squirrel.And, 0, 4)

	if p.Space {
		parts = append(parts, p.aclHas(spaceACL, "m.space_id", p.SpaceMode))
	}

	if p.Article {
		parts = append(parts, p.aclHas(articleACL, "m.id", p.Mode), p.ancestorsRead())
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
		return squirrel.Or{notDraft, p.aclAny(articleACL, "m.id", changeModes)}
	case DraftsBySpace:
		return squirrel.Or{notDraft, p.aclAny(spaceACL, "m.space_id", changeModes)}
	case DraftsAll:
	}

	return nil
}

// SpacePredicate keeps the space rows of alias m; nil when nothing applies.
func (p AccessPlan) SpacePredicate() squirrel.Sqlizer {
	switch {
	case p.Denied:
		return squirrel.Expr("FALSE")
	case p.Space:
		return p.aclHas(spaceACL, "m.id", p.SpaceMode)
	default:
		return nil
	}
}

// aclHas keeps the objects whose rules grant every bit of mode to a role.
func (p AccessPlan) aclHas(table, object string, mode auth.AccessMode) squirrel.Sqlizer {
	return squirrel.Expr(
		fmt.Sprintf("EXISTS (SELECT 1 FROM %s acl WHERE acl.dc = ? AND acl.object = %s"+
			" AND acl.subject = ANY(?::int8[]) AND acl.access & ? = ?)", table, object),
		p.Domain, p.Roles, int16(mode), int16(mode),
	)
}

// aclAny keeps the objects whose rules grant any of modes to a role.
func (p AccessPlan) aclAny(table, object string, modes []auth.AccessMode) squirrel.Sqlizer {
	var mask auth.AccessMode
	for _, mode := range modes {
		mask |= mode
	}

	return squirrel.Expr(
		fmt.Sprintf("EXISTS (SELECT 1 FROM %s acl WHERE acl.dc = ? AND acl.object = %s"+
			" AND acl.subject = ANY(?::int8[]) AND acl.access & ? <> 0)", table, object),
		p.Domain, p.Roles, int16(mask),
	)
}

// ancestorsRead keeps the articles whose every ancestor the caller may read,
// so a rule left on a nested article opens nothing once the path is closed.
func (p AccessPlan) ancestorsRead() squirrel.Sqlizer {
	read := func(alias string) string {
		return "EXISTS (SELECT 1 FROM " + articleACL + " acl WHERE acl.dc = ? AND acl.object = " + alias +
			".id AND acl.subject = ANY(?::int8[]) AND acl.access & ? = ?)"
	}

	return squirrel.Expr("(m.parent_id IS NULL OR m.parent_id IN (WITH RECURSIVE open AS ("+
		"SELECT a.id FROM kb.article a WHERE a.domain_id = ? AND a.parent_id IS NULL AND "+read("a")+
		" UNION ALL "+
		"SELECT a.id FROM kb.article a JOIN open o ON a.parent_id = o.id WHERE "+read("a")+
		") SELECT id FROM open))",
		p.Domain, p.Domain, p.Roles, int16(auth.Read), int16(auth.Read),
		p.Domain, p.Roles, int16(auth.Read), int16(auth.Read),
	)
}
