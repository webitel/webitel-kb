package postgres

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"

	"github.com/webitel/webitel-kb/internal/auth"
	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/store"
	queryobject "github.com/webitel/webitel-kb/internal/store/query_object"
)

// fakeRows plays back preset rows through the pgx.Rows contract, enough for
// the by-name collect helpers: field descriptions name the columns, Scan
// assigns the preset values to the scan targets.
type fakeRows struct {
	cols []string
	vals [][]any
	idx  int
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	descs := make([]pgconn.FieldDescription, len(r.cols))
	for i, col := range r.cols {
		descs[i] = pgconn.FieldDescription{Name: col}
	}

	return descs
}

func (r *fakeRows) Next() bool {
	r.idx++

	return r.idx <= len(r.vals)
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.vals[r.idx-1]
	for i, d := range dest {
		if row[i] == nil {
			continue
		}

		reflect.ValueOf(d).Elem().Set(reflect.ValueOf(row[i]))
	}

	return nil
}

func (r *fakeRows) Close()                        {}
func (r *fakeRows) Err() error                    { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *fakeRows) Values() ([]any, error)        { return nil, nil }
func (r *fakeRows) RawValues() [][]byte           { return nil }
func (r *fakeRows) Conn() *pgx.Conn               { return nil }

// fakeRow is the single-row counterpart for QueryRow.
type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}

	for i, d := range dest {
		if r.vals[i] == nil {
			continue
		}

		reflect.ValueOf(d).Elem().Set(reflect.ValueOf(r.vals[i]))
	}

	return nil
}

// fakeQuerier records the statement it received and plays back preset rows.
type fakeQuerier struct {
	gotSQL  string
	gotArgs []any

	rows pgx.Rows
	row  fakeRow
	err  error
}

func (f *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.gotSQL, f.gotArgs = sql, args

	return pgconn.CommandTag{}, f.err
}

func (f *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.gotSQL, f.gotArgs = sql, args

	if f.err != nil {
		return nil, f.err
	}

	if f.rows == nil {
		return &fakeRows{}, nil
	}

	return f.rows, nil
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.gotSQL, f.gotArgs = sql, args

	return f.row
}

// fakeAuther is a minimal caller session.
type fakeAuther struct {
	domainID int64
	userID   int64
}

func (a fakeAuther) GetRoles() []int64                            { return nil }
func (a fakeAuther) GetUserId() int64                             { return a.userID }
func (a fakeAuther) GetUserIp() string                            { return "" }
func (a fakeAuther) GetDomainId() int64                           { return a.domainID }
func (a fakeAuther) GetPermissions() []string                     { return nil }
func (a fakeAuther) GetObjectScope(string) auth.ObjectScoper      { return nil }
func (a fakeAuther) GetAllObjectScopes() []auth.ObjectScoper      { return nil }
func (a fakeAuther) CheckLicenseAccess(string) bool               { return true }
func (a fakeAuther) CheckObacAccess(string, auth.AccessMode) bool { return true }
func (a fakeAuther) IsRbacCheckRequired(string, auth.AccessMode) bool {
	return false
}
func (a fakeAuther) HasPermission(string) bool                    { return true }
func (a fakeAuther) HasSuperPermission(auth.SuperPermission) bool { return false }
func (a fakeAuther) GetMainAccessMode() auth.AccessMode           { return auth.NONE }
func (a fakeAuther) GetMainObjClassName() string                  { return "" }

// fakeSearchOpts implements options.Searcher.
type fakeSearchOpts struct {
	auth   auth.Auther
	fields []string
	search string
	sort   string
	ids    []int64
	page   int
	size   int
}

func (o *fakeSearchOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *fakeSearchOpts) GetFields() []string      { return o.fields }
func (o *fakeSearchOpts) GetSearch() string        { return o.search }
func (o *fakeSearchOpts) GetPage() int             { return o.page }
func (o *fakeSearchOpts) GetSize() int             { return o.size }
func (o *fakeSearchOpts) GetSort() string          { return o.sort }
func (o *fakeSearchOpts) GetIDs() []int64          { return o.ids }

// fakeWriteOpts implements options.Creator, Updator and Deleter.
type fakeWriteOpts struct {
	auth   auth.Auther
	fields []string
	id     int64
}

func (o *fakeWriteOpts) GetAuthOpts() auth.Auther { return o.auth }
func (o *fakeWriteOpts) GetFields() []string      { return o.fields }
func (o *fakeWriteOpts) GetID() int64             { return o.id }

func ptrTo[T any](v T) *T { return &v }

func TestEmbeddingModelListRendersScopedQuery(t *testing.T) {
	f := &fakeQuerier{}
	s := &embeddingModelStore{db: f}

	opts := &fakeSearchOpts{
		auth:   fakeAuther{domainID: 5},
		fields: []string{"id", "name"},
		search: "gem",
		size:   10,
		page:   2,
	}

	items, next, err := s.List(context.Background(), opts, model.EmbeddingModelFilter{Type: "embedding"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(items) != 0 || next {
		t.Fatalf("items=%d next=%v, want empty first", len(items), next)
	}

	for _, want := range []string{
		"(m.domain_id=$1 OR m.domain_id IS NULL)",
		"m.type=$2",
		"m.name ILIKE $3",
		"ORDER BY m.name ASC,m.id ASC", // default sort + unique tiebreaker
		"LIMIT 11",                     // size+1 lookahead
		"OFFSET 10",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	if f.gotArgs[0] != int64(5) {
		t.Errorf("args[0] = %v, want the domain id", f.gotArgs[0])
	}
}

func TestEmbeddingModelListScansAndPages(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{
		cols: []string{"id", "name", "created_by_id", "created_by_name"},
		vals: [][]any{
			{int64(7), "gemini", ptrTo(int64(3)), ptrTo("admin")},
			{int64(8), "e5", nil, nil},
		},
	}}
	s := &embeddingModelStore{db: f}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 1, page: 1}

	items, next, err := s.List(context.Background(), opts, model.EmbeddingModelFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Two rows on size 1: the lookahead row is trimmed and reported as next.
	if len(items) != 1 || !next {
		t.Fatalf("items=%d next=%v, want 1/true", len(items), next)
	}

	got := items[0]
	if got.ID != 7 || got.Name != "gemini" {
		t.Fatalf("item = %+v", got)
	}

	if got.CreatedBy == nil || got.CreatedBy.ID != 3 || got.CreatedBy.Name != "admin" {
		t.Fatalf("created_by = %+v, want lookup 3/admin", got.CreatedBy)
	}
}

func TestEmbeddingModelLocateNotFound(t *testing.T) {
	s := &embeddingModelStore{db: &fakeQuerier{}}

	opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: []int64{404}}

	_, err := s.Locate(context.Background(), opts)
	if errors.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestEmbeddingModelLocateRequiresExactlyOneID(t *testing.T) {
	// The store must not trust the options constructor alone: no ids would
	// fetch an arbitrary row, several ids would turn into an internal error.
	tests := []struct {
		name string
		ids  []int64
	}{
		{"no ids", nil},
		{"several ids", []int64{1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{}
			s := &embeddingModelStore{db: f}

			_, err := s.Locate(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: tt.ids})
			if errors.Code(err) != codes.InvalidArgument {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}

			if f.gotSQL != "" {
				t.Fatalf("query must not run: %s", f.gotSQL)
			}
		})
	}
}

func TestEmbeddingModelListSortPassthroughAndTiebreaker(t *testing.T) {
	tests := []struct {
		name      string
		sort      string
		wantOrder string
	}{
		{"explicit multi-criteria sort keeps order and gets the tiebreaker", "-created_at,name", "ORDER BY m.created_at DESC,m.name ASC,m.id ASC"},
		{"sort dropped by validation still pages deterministically", "bogus", "ORDER BY m.id ASC"},
		{"unsortable field dropped, tiebreaker remains", "domain_id", "ORDER BY m.id ASC"},
		{"id sort is not doubled", "-id", "ORDER BY m.id DESC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{}
			s := &embeddingModelStore{db: f}

			opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 2, page: 2, sort: tt.sort}
			if _, _, err := s.List(context.Background(), opts, model.EmbeddingModelFilter{}); err != nil {
				t.Fatalf("List: %v", err)
			}

			if !strings.Contains(f.gotSQL, tt.wantOrder) {
				t.Fatalf("SQL %q does not contain %q", f.gotSQL, tt.wantOrder)
			}

			if c := strings.Count(f.gotSQL, "m.id ASC"); tt.sort == "-id" && c != 0 {
				t.Fatalf("tiebreaker duplicated over explicit id sort: %s", f.gotSQL)
			}
		})
	}
}

// selectAliases extracts the output column names of the rendered SELECT list,
// so the scan fidelity test feeds pgx exactly the columns production will see.
func selectAliases(t *testing.T, sql string) []string {
	t.Helper()

	list, _, ok := strings.Cut(sql, " FROM ")
	if !ok {
		t.Fatalf("no FROM in %q", sql)
	}

	matches := regexp.MustCompile(`AS (\w+)`).FindAllStringSubmatch(list, -1)
	if len(matches) == 0 {
		t.Fatalf("no aliases in %q", list)
	}

	aliases := make([]string, 0, len(matches))
	for _, m := range matches {
		aliases = append(aliases, m[1])
	}

	return aliases
}

func TestEmbeddingModelFullDefaultSelectionScans(t *testing.T) {
	// Drive the complete default column set through real pgx scanning: every
	// alias the query object renders must resolve to a record field, so an
	// alias or db-tag rename fails here instead of in production.
	now := time.Now()

	values := map[string]any{
		"id": int64(7), "domain_id": ptrTo(int64(5)), "type": "embedding",
		"name": "gem", "provider": "gemini", "is_self_hosted": true,
		"model_ref": ptrTo("gemini-embedding-001"), "dimensions": ptrTo(int32(768)),
		"endpoint": ptrTo("http://e"), "validated_at": ptrTo(now), "created_at": now,
		"created_by_id": ptrTo(int64(3)), "created_by_name": ptrTo("admin"),
	}

	sql, _, err := queryobject.NewEmbeddingModelQuery(queryobject.EmbeddingModelFrom).ToSQL()
	if err != nil {
		t.Fatal(err)
	}

	cols := selectAliases(t, sql)
	if len(cols) != len(values) {
		t.Fatalf("rendered %d aliases, fixture has %d — update both together: %v", len(cols), len(values), cols)
	}

	row := make([]any, 0, len(cols))
	for _, col := range cols {
		v, ok := values[col]
		if !ok {
			t.Fatalf("no fixture value for rendered alias %q", col)
		}

		row = append(row, v)
	}

	f := &fakeQuerier{rows: &fakeRows{cols: cols, vals: [][]any{row}}}
	s := &embeddingModelStore{db: f}

	items, _, err := s.List(context.Background(), &fakeSearchOpts{auth: fakeAuther{domainID: 5}, size: 10, page: 1}, model.EmbeddingModelFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	got := *items[0]
	want := model.EmbeddingModel{
		ID: 7, DomainID: 5, Type: "embedding", Name: "gem", Provider: "gemini",
		IsSelfHosted: true, ModelRef: "gemini-embedding-001", Dimensions: 768,
		Endpoint: "http://e", ValidatedAt: now, CreatedAt: now,
		CreatedBy: &model.Lookup{ID: 3, Name: "admin"},
	}

	if *got.CreatedBy != *want.CreatedBy {
		t.Fatalf("created_by = %+v, want %+v", got.CreatedBy, want.CreatedBy)
	}

	got.CreatedBy, want.CreatedBy = nil, nil
	if got != want {
		t.Fatalf("model = %+v, want %+v", got, want)
	}
}

func TestEmbeddingModelCreateRendersCTE(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &embeddingModelStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5, userID: 9}, fields: []string{"id"}}
	in := &model.EmbeddingModel{
		Type: "embedding", Name: "gem", Provider: "gemini",
		ModelRef: "gemini-embedding-001", Dimensions: 768,
	}

	created, err := s.Create(context.Background(), opts, in, []byte("enc"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID != 1 {
		t.Fatalf("created = %+v", created)
	}

	for _, want := range []string{
		"WITH m AS (INSERT INTO kb.embedding_model",
		"config",
		"RETURNING *",
		"SELECT m.id AS id FROM m",
	} {
		if !strings.Contains(f.gotSQL, want) {
			t.Errorf("SQL %q does not contain %q", f.gotSQL, want)
		}
	}

	// Column order: domain_id, type, name, provider, is_self_hosted, model_ref,
	// dimensions, endpoint, config, created_by.
	if f.gotArgs[0] != int64(5) {
		t.Errorf("args[0] = %v, want the domain id", f.gotArgs[0])
	}

	if got, ok := f.gotArgs[8].([]byte); !ok || string(got) != "enc" {
		t.Errorf("args[8] = %v, want the credential bytes", f.gotArgs[8])
	}

	if got, ok := f.gotArgs[7].(*string); !ok || got != nil {
		t.Errorf("args[7] = %v, want NULL for the empty endpoint", f.gotArgs[7])
	}

	if got, ok := f.gotArgs[9].(*int64); !ok || got == nil || *got != 9 {
		t.Errorf("args[9] = %v, want created_by 9", f.gotArgs[9])
	}
}

func TestEmbeddingModelUpdateConfigColumn(t *testing.T) {
	tests := []struct {
		name       string
		keepConfig bool
		wantConfig bool
	}{
		{"replacing writes the config column", false, true},
		{"keeping leaves the config column untouched", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
			s := &embeddingModelStore{db: f}

			opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 1, fields: []string{"id"}}

			_, err := s.Update(context.Background(), opts, &model.EmbeddingModel{
				Type: "embedding", Name: "gem", Provider: "gemini", Dimensions: 768,
			}, []byte("new"), tt.keepConfig)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}

			if got := strings.Contains(f.gotSQL, "config"); got != tt.wantConfig {
				t.Errorf("SQL %q: config touched = %v, want %v", f.gotSQL, got, tt.wantConfig)
			}

			// Any update invalidates the previous validation.
			if !strings.Contains(f.gotSQL, "validated_at = $8") {
				t.Errorf("SQL %q does not reset validated_at", f.gotSQL)
			}

			if idx := len(f.gotArgs) - 2; f.gotArgs[idx] != int64(1) || f.gotArgs[idx+1] != int64(5) {
				t.Errorf("write not scoped to id and domain: args %v", f.gotArgs)
			}

			if !strings.Contains(f.gotSQL, "WHERE id = $") || !strings.Contains(f.gotSQL, "AND domain_id = $") {
				t.Errorf("SQL %q is not scoped to id and domain", f.gotSQL)
			}
		})
	}
}

func TestEmbeddingModelUpdateBindsValuesInOrder(t *testing.T) {
	// Pin every SET value to its column position: swapping two Set calls (or a
	// value against its key) must fail here, not corrupt rows in production.
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &embeddingModelStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 1, fields: []string{"id"}}
	in := &model.EmbeddingModel{
		Type: "reranker", Name: "n1", Provider: "p1", IsSelfHosted: true,
		ModelRef: "mr1", Dimensions: 42, Endpoint: "ep1",
	}

	if _, err := s.Update(context.Background(), opts, in, []byte("cfg"), false); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// SET order: type, name, provider, is_self_hosted, model_ref, dimensions,
	// endpoint, validated_at(NULL), config; WHERE: id, domain_id.
	assertArg := func(i int, want any) {
		t.Helper()

		got := f.gotArgs[i]

		switch w := want.(type) {
		case *string:
			g, ok := got.(*string)
			if !ok || g == nil || *g != *w {
				t.Errorf("args[%d] = %v, want %v", i, got, *w)
			}
		case *int32:
			g, ok := got.(*int32)
			if !ok || g == nil || *g != *w {
				t.Errorf("args[%d] = %v, want %v", i, got, *w)
			}
		case []byte:
			g, ok := got.([]byte)
			if !ok || string(g) != string(w) {
				t.Errorf("args[%d] = %v, want %s", i, got, w)
			}
		default:
			if got != want {
				t.Errorf("args[%d] = %v, want %v", i, got, want)
			}
		}
	}

	if len(f.gotArgs) != 11 {
		t.Fatalf("args = %d, want 11: %v", len(f.gotArgs), f.gotArgs)
	}

	assertArg(0, "reranker")
	assertArg(1, "n1")
	assertArg(2, "p1")
	assertArg(3, true)
	assertArg(4, ptrTo("mr1"))
	assertArg(5, ptrTo(int32(42)))
	assertArg(6, ptrTo("ep1"))
	assertArg(7, nil)
	assertArg(8, []byte("cfg"))
	assertArg(9, int64(1))
	assertArg(10, int64(5))
}

func TestEmbeddingModelDeleteRendersScopedCTE(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &embeddingModelStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 1, fields: []string{"id"}}

	if _, err := s.Delete(context.Background(), opts); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !strings.Contains(f.gotSQL, "WITH m AS (DELETE FROM kb.embedding_model WHERE id = $1 AND domain_id = $2 RETURNING *)") {
		t.Fatalf("SQL %q is not a domain-scoped delete CTE", f.gotSQL)
	}
}

func TestEmbeddingModelMarkValidated(t *testing.T) {
	f := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	s := &embeddingModelStore{db: f}

	opts := &fakeWriteOpts{auth: fakeAuther{domainID: 5}, id: 1, fields: []string{"id"}}

	if _, err := s.MarkValidated(context.Background(), opts); err != nil {
		t.Fatalf("MarkValidated: %v", err)
	}

	// The database clock stamps the validation, scoped like every write.
	if !strings.Contains(f.gotSQL, "SET validated_at = now() WHERE id = $1 AND domain_id = $2") {
		t.Fatalf("SQL %q does not stamp validated_at in place", f.gotSQL)
	}
}

func TestEmbeddingModelGetConfig(t *testing.T) {
	f := &fakeQuerier{row: fakeRow{vals: []any{[]byte("enc")}}}
	s := &embeddingModelStore{db: f}

	config, err := s.GetConfig(context.Background(), 1, 5)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}

	if string(config) != "enc" {
		t.Fatalf("config = %q, want enc", config)
	}

	// Reads include global models: the runtime needs their credential too.
	if !strings.Contains(f.gotSQL, "(domain_id = $2 OR domain_id IS NULL)") {
		t.Fatalf("SQL %q misses the read scope", f.gotSQL)
	}

	// Binding order: a swap would read another row or another domain's config.
	if f.gotArgs[0] != int64(1) || f.gotArgs[1] != int64(5) {
		t.Fatalf("args = %v, want [id domain]", f.gotArgs)
	}
}

func TestEmbeddingModelGetConfigNotFound(t *testing.T) {
	s := &embeddingModelStore{db: &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}}

	_, err := s.GetConfig(context.Background(), 404, 5)
	if errors.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// queryRecordingTx is a transaction whose queries land in a fakeQuerier.
type queryRecordingTx struct {
	pgx.Tx

	q *fakeQuerier
}

func (t *queryRecordingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.q.Query(ctx, sql, args...)
}

func (t *queryRecordingTx) Commit(context.Context) error   { return nil }
func (t *queryRecordingTx) Rollback(context.Context) error { return nil }

type txOnlyBeginner struct {
	tx pgx.Tx
}

func (b *txOnlyBeginner) Begin(context.Context) (pgx.Tx, error) { return b.tx, nil }

func TestEmbeddingModelStoreAccessorFollowsTransaction(t *testing.T) {
	inner := &fakeQuerier{rows: &fakeRows{cols: []string{"id"}, vals: [][]any{{int64(1)}}}}
	uow := NewUnitOfWork(&txOnlyBeginner{tx: &queryRecordingTx{q: inner}})

	err := uow.WithinTransaction(context.Background(), func(ctx context.Context, txUow store.UnitOfWork) error {
		opts := &fakeSearchOpts{auth: fakeAuther{domainID: 5}, ids: []int64{1}}

		_, err := txUow.EmbeddingModelStore().Locate(ctx, opts)

		return err
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	// The accessor must bind to the transaction, not the pool.
	if inner.gotSQL == "" {
		t.Fatal("the store query did not run on the transaction")
	}
}
