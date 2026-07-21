package util

// ResolvePaging trims a lookahead result page. Stores fetch size+1 rows (see
// queryobject.WithPaging); when more than size arrived, the extra row is
// dropped and next reports a further page exists. A non-positive size means
// paging was disabled: items pass through unchanged.
func ResolvePaging[T any](size int, items []T) ([]T, bool) {
	if size > 0 && len(items) > size {
		return items[:size], true
	}

	return items, false
}
