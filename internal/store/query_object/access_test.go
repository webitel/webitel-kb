package queryobject

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/squirrel"

	"github.com/webitel/webitel-kb/internal/auth"
	user_session "github.com/webitel/webitel-kb/internal/auth/session/user_session"
	"github.com/webitel/webitel-kb/internal/model"
)

// session builds a caller with the given object access per class; a class
// listed in records is managed by records.
func session(access map[string]string, records ...string) *user_session.UserAuthSession {
	scopes := make(map[string]*user_session.Scope, len(access))
	for class, mode := range access {
		scopes[class] = &user_session.Scope{
			Class: class, Obac: true, Access: mode, Rbac: slices.Contains(records, class),
		}
	}

	return &user_session.UserAuthSession{
		User:   &user_session.User{ID: 7},
		Roles:  []*user_session.Role{{ID: 3}},
		Scopes: scopes,
	}
}

func TestPlanArticleAccess(t *testing.T) {
	both := []string{model.ScopeSpaces, model.ScopeArticles}

	tests := []struct {
		name    string
		session *user_session.UserAuthSession
		mode    auth.AccessMode
		want    AccessPlan
	}{
		{
			name:    "select only hides drafts",
			session: session(map[string]string{model.ScopeArticles: "r"}),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, SpaceMode: auth.Read, Drafts: DraftsNone},
		},
		{
			name:    "an edit right keeps drafts",
			session: session(map[string]string{model.ScopeArticles: "rw"}),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, SpaceMode: auth.Read, Drafts: DraftsAll},
		},
		{
			name:    "a change never filters drafts",
			session: session(map[string]string{model.ScopeArticles: "r"}),
			mode:    auth.Edit,
			want:    AccessPlan{Mode: auth.Edit, SpaceMode: auth.Edit, Drafts: DraftsAll},
		},
		{
			name:    "records rules on both classes check the article and read the space",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rw"}, both...),
			mode:    auth.Edit,
			want:    AccessPlan{Mode: auth.Edit, Space: true, SpaceMode: auth.Read, Article: true, Drafts: DraftsAll},
		},
		{
			name:    "records rules on both classes keep drafts by the article rules",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rw"}, both...),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, Space: true, SpaceMode: auth.Read, Article: true, Drafts: DraftsByArticle},
		},
		{
			name:    "records rules with select only hide drafts outright",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "r"}, both...),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, Space: true, SpaceMode: auth.Read, Article: true, Drafts: DraftsNone},
		},
		{
			name:    "rules on spaces only stand for the article change",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rw"}, model.ScopeSpaces),
			mode:    auth.Edit,
			want:    AccessPlan{Mode: auth.Edit, Space: true, SpaceMode: auth.Edit, Drafts: DraftsAll},
		},
		{
			name:    "the create right keeps drafts under records rules",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rx"}, both...),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, Space: true, SpaceMode: auth.Read, Article: true, Drafts: DraftsAll},
		},
		{
			name:    "rules on spaces only keep drafts by the space rules",
			session: session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rw"}, model.ScopeSpaces),
			mode:    auth.Read,
			want:    AccessPlan{Mode: auth.Read, Space: true, SpaceMode: auth.Read, Drafts: DraftsBySpace},
		},
		{
			name: "a super right passes the rules of its mode",
			session: func() *user_session.UserAuthSession {
				s := session(map[string]string{model.ScopeSpaces: "r", model.ScopeArticles: "rw"}, both...)
				s.SuperSelect, s.SuperEdit = true, true

				return s
			}(),
			mode: auth.Read,
			want: AccessPlan{Mode: auth.Read, SpaceMode: auth.Read, Drafts: DraftsAll},
		},
		{
			name:    "no mode means reading",
			session: session(map[string]string{model.ScopeArticles: "rw"}),
			mode:    auth.NONE,
			want:    AccessPlan{Mode: auth.Read, SpaceMode: auth.Read, Drafts: DraftsAll},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanArticleAccess(tt.session, tt.mode)
			if !slices.Equal(got.Roles, []int64{7, 3}) {
				t.Errorf("roles = %v, want the user and its role", got.Roles)
			}

			got.Roles = nil

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("plan = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPlanSpaceAccess(t *testing.T) {
	tests := []struct {
		name    string
		session *user_session.UserAuthSession
		mode    auth.AccessMode
		want    bool
	}{
		{name: "without records rules nothing is checked", session: session(map[string]string{model.ScopeSpaces: "rw"}), mode: auth.Edit},
		{name: "records rules are checked", session: session(map[string]string{model.ScopeSpaces: "rw"}, model.ScopeSpaces), mode: auth.Edit, want: true},
		{
			name: "a super right of the mode passes them",
			session: func() *user_session.UserAuthSession {
				s := session(map[string]string{model.ScopeSpaces: "rd"}, model.ScopeSpaces)
				s.SuperDelete = true

				return s
			}(),
			mode: auth.Delete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanSpaceAccess(tt.session, tt.mode)
			if got.Space != tt.want || got.SpaceMode != tt.mode {
				t.Errorf("space check = %v in %v, want %v in %v", got.Space, got.SpaceMode, tt.want, tt.mode)
			}
		})
	}
}

// ruleChecks lists the rule tables a predicate reads with the mode of each.
func ruleChecks(t *testing.T, pred squirrel.Sqlizer) map[string]int16 {
	t.Helper()

	got := map[string]int16{}
	if pred == nil {
		return got
	}

	var walk func(squirrel.Sqlizer)

	walk = func(part squirrel.Sqlizer) {
		switch v := part.(type) {
		case squirrel.And:
			for _, p := range v {
				walk(p)
			}
		case squirrel.Or:
			for _, p := range v {
				walk(p)
			}
		default:
			sql, args, err := part.ToSql()
			if err != nil {
				t.Fatalf("ToSql: %v", err)
			}

			for _, table := range []string{spaceACL, articleACL} {
				if strings.Contains(sql, table) {
					got[table] = args[1].(int16)
				}
			}
		}
	}
	walk(pred)

	return got
}

func TestArticlePredicateChecksTheRulesOfThePlan(t *testing.T) {
	writes := int16(auth.Edit | auth.Delete)

	tests := []struct {
		name string
		plan AccessPlan
		want map[string]int16
	}{
		{name: "nothing to check", plan: AccessPlan{Mode: auth.Read}, want: map[string]int16{}},
		{
			name: "article rules in the mode and the space for reading",
			plan: AccessPlan{Mode: auth.Delete, Article: true, Space: true, SpaceMode: auth.Read},
			want: map[string]int16{articleACL: int16(auth.Delete), spaceACL: int16(auth.Read)},
		},
		{
			name: "space rules standing for the articles",
			plan: AccessPlan{Mode: auth.Edit, Space: true, SpaceMode: auth.Edit},
			want: map[string]int16{spaceACL: int16(auth.Edit)},
		},
		{
			name: "drafts by the article rules",
			plan: AccessPlan{Mode: auth.Read, Drafts: DraftsByArticle},
			want: map[string]int16{articleACL: writes},
		},
		{
			name: "drafts by the space rules",
			plan: AccessPlan{Mode: auth.Read, Drafts: DraftsBySpace},
			want: map[string]int16{spaceACL: writes},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ruleChecks(t, tt.plan.ArticlePredicate()); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rule checks = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccessPredicates(t *testing.T) {
	tests := []struct {
		name        string
		plan        AccessPlan
		wantArticle bool
		wantSpace   bool
	}{
		{name: "nothing to check renders nothing", plan: AccessPlan{Mode: auth.Read}},
		{name: "hidden drafts filter articles only", plan: AccessPlan{Mode: auth.Read, Drafts: DraftsNone}, wantArticle: true},
		{name: "space rules filter both", plan: AccessPlan{Mode: auth.Read, Space: true, SpaceMode: auth.Read}, wantArticle: true, wantSpace: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plan.ArticlePredicate() != nil; got != tt.wantArticle {
				t.Errorf("article predicate present = %v, want %v", got, tt.wantArticle)
			}

			if got := tt.plan.SpacePredicate() != nil; got != tt.wantSpace {
				t.Errorf("space predicate present = %v, want %v", got, tt.wantSpace)
			}
		})
	}
}
