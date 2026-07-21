// Package util holds helpers shared by the store layer and the request options
// that feed it.
package util

import "strings"

// InlineFields expands a field selection into individual names. Callers may pass
// one field per element, or pack several into one element separated by commas or
// spaces ("id,name" / "id name"), which is what REST query strings produce.
// Names are lowercased and blanks dropped.
func InlineFields(fields []string) []string {
	out := make([]string, 0, len(fields))

	for _, field := range fields {
		for _, name := range strings.FieldsFunc(field, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
				out = append(out, name)
			}
		}
	}

	return out
}

// DeduplicateFields drops repeated names, keeping first-seen order.
func DeduplicateFields(fields []string) []string {
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))

	for _, field := range fields {
		if _, dup := seen[field]; dup {
			continue
		}

		seen[field] = struct{}{}
		out = append(out, field)
	}

	return out
}
