package util

import "strings"

// SplitSort normalizes an API sort expression into the "+field"/"-field"
// criteria the query objects accept: criteria are comma-separated, and a bare
// field name sorts ascending.
func SplitSort(sort string) []string {
	parts := strings.Split(sort, ",")

	criteria := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if part[0] != '+' && part[0] != '-' {
			part = "+" + part
		}

		criteria = append(criteria, part)
	}

	return criteria
}
