// Package queryobject builds SELECT statements for entity stores. An entity
// declares its field metadata once — SQL expression, joins the field needs,
// sortability — and the base query object turns validated field, sort and
// paging input into squirrel SQL.
package queryobject

import (
	"fmt"

	"github.com/Masterminds/squirrel"
)

// QueryObject is a built query ready to render to SQL.
type QueryObject interface {
	ToSQL() (string, []any, error)
}

// Entity describes a queryable entity to the base query object.
type Entity interface {
	// DefaultFields returns the fields selected when the caller asks for none.
	DefaultFields() []string

	// FieldsMetadata maps a field name to how it is selected, sorted and joined.
	// Implementations must memoize: the base consults it on every field.
	FieldsMetadata() map[string]fieldMetadata

	// EnsureJoins activates the joins a selected field requires. The base may
	// call it more than once for the same mask, so implementations must be
	// idempotent: record the requirement (bitmask OR), never append a JOIN
	// clause directly here.
	EnsureJoins(requiredJoin int)
}

// fieldMetadata describes one selectable field.
type fieldMetadata struct {
	// sqlExpr is the bare SQL expression, used in ORDER BY.
	sqlExpr string
	// aliasedExpr is the SELECT-list expression including its alias.
	aliasedExpr string
	// requiresJoin is the join bitmask this field needs; 0 for none.
	requiresJoin int
	// sortable reports whether the field may be used in ORDER BY.
	sortable bool
}

// baseQueryObject implements the shared field/sort/paging mechanics for an
// entity query object embedding it (the entity passes itself as ent, so the
// With* methods can chain on the concrete type).
type baseQueryObject[T Entity] struct {
	builder squirrel.SelectBuilder
	fields  []string
	sorts   []string

	entity T
}

func newBaseQueryObject[T Entity](from string, ent T) *baseQueryObject[T] {
	return &baseQueryObject[T]{
		builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Select().From(from),
		entity:  ent,
	}
}

// WithFields selects the requested fields. Unknown names are dropped silently;
// the joins of accepted fields are activated.
func (q *baseQueryObject[T]) WithFields(fields []string) T {
	if len(fields) == 0 {
		return q.entity
	}

	meta := q.entity.FieldsMetadata()

	valid := make([]string, 0, len(fields))
	for _, field := range fields {
		fm, ok := meta[field]
		if !ok {
			continue
		}

		valid = append(valid, field)

		q.entity.EnsureJoins(fm.requiresJoin)
	}

	q.fields = valid

	return q.entity
}

// WithSort applies sort criteria of the form "+field" (ascending) or "-field"
// (descending). Criteria naming an unknown or unsortable field, or missing the
// direction prefix, are dropped silently.
func (q *baseQueryObject[T]) WithSort(sortFields ...string) T {
	if len(sortFields) == 0 {
		return q.entity
	}

	meta := q.entity.FieldsMetadata()

	valid := make([]string, 0, len(sortFields))
	for _, sortField := range sortFields {
		if len(sortField) < 2 {
			continue
		}

		direction, fieldName := sortField[:1], sortField[1:]
		if direction != "+" && direction != "-" {
			continue
		}

		fm, ok := meta[fieldName]
		if !ok || !fm.sortable {
			continue
		}

		q.entity.EnsureJoins(fm.requiresJoin)

		valid = append(valid, sortField)
	}

	q.sorts = valid

	return q.entity
}

// WithPaging applies the size and 1-based page exactly as given — defaults and
// caps are the options layer's job. A positive size fetches one extra row, so
// the store can report whether a next page exists (see util.ResolvePaging); a
// non-positive size disables paging entirely.
func (q *baseQueryObject[T]) WithPaging(size, page int) T {
	if size <= 0 {
		return q.entity
	}

	q.builder = q.builder.Limit(uint64(size) + 1)

	if page > 1 {
		q.builder = q.builder.Offset(uint64((page - 1) * size))
	}

	return q.entity
}

// ToSQL renders the query. Fields default to the entity's DefaultFields when
// none were selected.
func (q *baseQueryObject[T]) ToSQL() (string, []any, error) {
	fields := q.fields
	if len(fields) == 0 {
		fields = q.entity.DefaultFields()
	}

	meta := q.entity.FieldsMetadata()

	exprs := make([]string, 0, len(fields))
	for _, field := range fields {
		fm, ok := meta[field]
		if !ok {
			continue
		}

		// Default fields still need their joins: WithFields never saw them.
		q.entity.EnsureJoins(fm.requiresJoin)
		exprs = append(exprs, fm.aliasedExpr)
	}

	builder := q.builder.Columns(exprs...)

	for _, sortField := range q.sorts {
		direction, fieldName := sortField[:1], sortField[1:]

		// WithSort validated the field, but against a metadata snapshot the
		// entity is free to have rebuilt since; skip a miss rather than render
		// an empty ORDER BY expression.
		fm, ok := meta[fieldName]
		if !ok {
			continue
		}

		order := "ASC"
		if direction == "-" {
			order = "DESC"
		}

		builder = builder.OrderBy(fmt.Sprintf("%s %s", fm.sqlExpr, order))
	}

	sql, args, err := builder.ToSql()

	return CompactSQL(sql), args, err
}
