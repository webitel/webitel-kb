package queryobject

import (
	"strings"
	"testing"
)

func mustSpaceSQL(t *testing.T, q *SpaceQuery) (string, []any) {
	t.Helper()

	sql, args, err := q.ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}

	return sql, args
}

func TestSpaceDomainScope(t *testing.T) {
	sql, args := mustSpaceSQL(t, NewSpaceQuery(SpaceFrom).WithDomainScope(5))

	if !strings.Contains(sql, "m.domain_id=$1") {
		t.Fatalf("SQL %q misses the domain scope", sql)
	}

	if len(args) != 1 || args[0] != int64(5) {
		t.Fatalf("args = %v, want [5]", args)
	}
}

func TestSpaceTeamsSubquery(t *testing.T) {
	// The binding aggregates in the SELECT list: no GROUP BY, no join that
	// would multiply rows.
	sql, _ := mustSpaceSQL(t, NewSpaceQuery(SpaceFrom).WithFields([]string{"id", "teams"}))

	for _, want := range []string{
		"json_agg(json_build_object('id',t.id,'name',t.name)",
		"FROM kb.team_space ts JOIN call_center.cc_team t ON t.id=ts.team_id WHERE ts.space_id=m.id)AS teams",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}

	if strings.Contains(sql, "GROUP BY") {
		t.Fatalf("teams must not force grouping: %s", sql)
	}
}

func TestSpaceUserJoinsRenderOncePerAlias(t *testing.T) {
	q := NewSpaceQuery(SpaceFrom).
		WithFields([]string{"id", "created_by", "updated_by"}).
		WithSort("+created_by", "-updated_by")

	sql, _ := mustSpaceSQL(t, q)

	for _, join := range []string{
		"LEFT JOIN directory.wbt_user cb ON cb.id=m.created_by",
		"LEFT JOIN directory.wbt_user ub ON ub.id=m.updated_by",
	} {
		if got := strings.Count(sql, join); got != 1 {
			t.Fatalf("join %q rendered %d times, want exactly once: %s", join, got, sql)
		}
	}
}

func TestSpaceDefaultsCoverReadModel(t *testing.T) {
	q := NewSpaceQuery(SpaceFrom)

	if got, want := len(q.DefaultFields()), len(q.FieldsMetadata()); got != want {
		t.Fatalf("defaults name %d fields, metadata has %d", got, want)
	}

	sql, _ := mustSpaceSQL(t, q)

	for _, want := range []string{
		"m.id AS id", "m.language AS language", "m.target_embedding_model_id AS target_embedding_model_id",
		"AS teams", "updated_by_name", "LEFT JOIN directory.wbt_user cb", "LEFT JOIN directory.wbt_user ub",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}

func TestSpaceCTEReadBack(t *testing.T) {
	sql, args := mustSpaceSQL(t, NewSpaceQuery("m").WithFields([]string{"id", "teams", "created_by"}))

	if len(args) != 0 {
		t.Fatalf("read-back rendered arguments: %v", args)
	}

	if !strings.Contains(sql, "FROM m LEFT JOIN directory.wbt_user") {
		t.Fatalf("SQL %q does not select from the CTE", sql)
	}
}

func TestSpaceProjectionAlwaysCarriesTheIdentity(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewSpaceQuery(SpaceFrom).WithFields([]string{"name"}))

	for _, want := range []string{"m.name AS name", "m.id AS id"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL %q does not contain %q", sql, want)
		}
	}
}
