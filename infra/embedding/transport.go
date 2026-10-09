package embedding

import (
	"fmt"
	"net/url"
	"strings"
)

// serviceURL joins the root of a service and a route on the URL path.
func serviceURL(root, route string) (string, error) {
	u, err := url.Parse(root)
	if err != nil {
		return "", fmt.Errorf("embedding: endpoint url: %w", err)
	}

	path := strings.TrimRight(u.Path, "/")
	parts := strings.Split(route, "/")

	for n := len(parts); n > 0; n-- {
		if lead := "/" + strings.Join(parts[:n], "/"); strings.HasSuffix(path, lead) {
			path = strings.TrimSuffix(path, lead)

			break
		}
	}

	u.Path = path + "/" + route
	u.RawPath = ""

	return u.String(), nil
}

// byIndex puts the items a provider listed in any order back in request order;
// each of the inputs must be answered exactly once.
func byIndex[T any](items []T, inputs int, index func(T) int) ([]T, error) {
	if len(items) != inputs {
		return nil, fmt.Errorf("embedding: provider answered %d of %d inputs", len(items), inputs)
	}

	ordered := make([]T, inputs)
	seen := make([]bool, inputs)

	for _, item := range items {
		i := index(item)
		if i < 0 || i >= inputs {
			return nil, fmt.Errorf("embedding: provider answered unknown input %d", i)
		}

		if seen[i] {
			return nil, fmt.Errorf("embedding: provider answered input %d twice", i)
		}

		seen[i] = true
		ordered[i] = item
	}

	return ordered, nil
}
