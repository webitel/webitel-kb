package queryobject

import (
	"strings"
	"testing"

	"github.com/webitel/webitel-kb/internal/model"
)

func TestVectorLiteral(t *testing.T) {
	tests := []struct {
		name string
		in   []float32
		want string
	}{
		{name: "empty", in: nil, want: "[]"},
		{name: "one", in: []float32{0.5}, want: "[0.5]"},
		{name: "several", in: []float32{1, -0.25, 0.125}, want: "[1,-0.25,0.125]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VectorLiteral(tt.in); got != tt.want {
				t.Fatalf("VectorLiteral = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHybridHitsLexicalOnly(t *testing.T) {
	q := model.HybridQuery{
		Term:   "router",
		Filter: model.SearchFilter{SpaceIDs: []int64{1, 2}, Tags: []string{"net"}},
		TopK:   10,
	}

	sql, args := mustSQLArgs(t, NewHybridHits(q).WithScope(5))

	for _, want := range []string{
		"WITH tq AS MATERIALIZED (SELECT config, websearch_to_tsquery(config::regconfig, $1) AS query FROM " +
			"(SELECT DISTINCT s.text_search_config AS config FROM kb.space s WHERE s.domain_id = $2 AND s.id = ANY($3)) AS configs)",
		", lex AS (SELECT id, row_number() OVER (ORDER BY rank_key DESC, id) AS rank FROM (",
		"FROM tq JOIN kb.space s ON s.text_search_config = tq.config JOIN kb.article m ON m.space_id = s.id" +
			" JOIN kb.chunk c ON c.version_id = m.published_version_id",
		"ts_rank_cd(c.tsv, tq.query) AS rank_key",
		"c.tsv @@ tq.query",
		"s.domain_id = $4",
		"m.state = $5",
		"s.id = ANY($6)",
		"m.tags && $7",
		"ORDER BY rank_key DESC, c.id LIMIT 50) t)",
		"h AS (SELECT id, sum(1.0 / (60 + rank)) AS score FROM (SELECT * FROM lex) branches" +
			" GROUP BY id ORDER BY score DESC, id LIMIT 10)",
		"FROM h JOIN kb.chunk c ON c.id = h.id JOIN kb.article m ON m.published_version_id = c.version_id",
		"h.score::float8 AS score",
		"ORDER BY h.score DESC, c.id",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q\n does not contain %q", sql, want)
		}
	}

	if strings.Contains(sql, "vec") || strings.Contains(sql, "'simple'") || strings.Contains(sql, "m.space_id = ANY") ||
		strings.Contains(sql, "word_similarity") {
		t.Errorf("SQL renders a vector branch or a fixed configuration: %q", sql)
	}

	// The term is parsed once.
	if len(args) != 7 || args[0] != "router" || args[1] != int64(5) {
		t.Fatalf("args = %v", args)
	}
}

func TestHybridHitsParsesTheTermPerDomainConfig(t *testing.T) {
	sql, _ := mustSQLArgs(t, NewHybridHits(model.HybridQuery{Term: "vpn", TopK: 5}).WithScope(5))

	if !strings.Contains(sql, "FROM kb.space s WHERE s.domain_id = $2) AS configs") {
		t.Fatalf("SQL %q does not collect the configurations of the domain", sql)
	}
}

func TestHybridHitsVectorBranches(t *testing.T) {
	q := model.HybridQuery{
		Term:   "router",
		Filter: model.SearchFilter{SpaceIDs: []int64{1, 2, 3}},
		Vectors: []model.ModelVector{
			{ModelID: 9, Dimensions: 768, SpaceIDs: []int64{1, 2}, Vector: []float32{0.5, 0.5}},
			{ModelID: 11, Dimensions: 1024, SpaceIDs: []int64{3}, Vector: []float32{1, 0}},
		},
		TopK: 5,
	}

	sql, args := mustSQLArgs(t, NewHybridHits(q).WithScope(5))

	for _, want := range []string{
		"vec AS (SELECT id, row_number() OVER (ORDER BY rank_key, id) AS rank FROM (",
		"FROM kb.chunk_embedding e JOIN kb.chunk c ON c.id = e.chunk_id JOIN kb.article m ON m.published_version_id = c.version_id WHERE",
		"e.model_id = $",
		"e.domain_id = $",
		"e.space_id = ANY($",
		"e.embedding_768 <=> $",
		"ORDER BY e.embedding_768 <=> $",
		"e.embedding_1024 <=> $",
		"ORDER BY e.embedding_1024 <=> $",
		"::vector AS rank_key",
		"::vector, c.id LIMIT 50)",
		"UNION ALL",
		"(SELECT * FROM lex UNION ALL SELECT * FROM vec) branches",
		"LIMIT 5)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q\n does not contain %q", sql, want)
		}
	}

	if strings.Contains(sql, "e.embedding <=>") {
		t.Errorf("a vector branch ranks by a column of no size: %q", sql)
	}

	if strings.Count(sql, "e.model_id = $") != 2 || strings.Count(sql, "e.space_id = ANY($") != 2 {
		t.Errorf("want one vector branch per model, each on its own spaces, SQL: %q", sql)
	}

	if _, vec, _ := strings.Cut(sql, "vec AS"); strings.Contains(vec, "kb.space s") {
		t.Errorf("a vector side joins the space although the scope is on the row: %q", sql)
	}

	var literals, models int

	for _, a := range args {
		switch v := a.(type) {
		case string:
			if strings.HasPrefix(v, "[") {
				literals++
			}
		case int64:
			if v == 9 || v == 11 {
				models++
			}
		}
	}

	if literals != 4 || models != 2 {
		t.Fatalf("vector literals = %d (want 4: rank and order per branch), model ids = %d (want 2); args = %v", literals, models, args)
	}
}
