// Package bodyconv derives the stored representations of a rich-text article
// body from its canonical editor document (ProseMirror-compatible JSON): a
// markdown serialization that keeps the structure chunking relies on, and a
// flat plain text that feeds full-text search.
package bodyconv

import (
	"errors"
	"sort"
)

// ErrNotDocument reports that the parsed root node is not a document.
var ErrNotDocument = errors.New("bodyconv: root node is not a document")

// Result carries both derived representations of one document.
type Result struct {
	// Markdown keeps headings, lists, quotes and code structure.
	Markdown string

	// Plain is flat text without any markup.
	Plain string

	// Unknown lists the node and mark types the converter did not recognize,
	// deduplicated and sorted. Their text content is still preserved in both
	// outputs; the list exists so the caller can log what the editor sent.
	Unknown []string
}

// Convert parses a ProseMirror-compatible JSON document and renders both
// derived representations. The same input always yields byte-identical output.
func Convert(doc []byte) (Result, error) {
	root, err := parseDocument(doc)
	if err != nil {
		return Result{}, err
	}

	seen := make(map[string]bool)
	collectUnknown(root, seen)

	var unknown []string

	if len(seen) > 0 {
		unknown = make([]string, 0, len(seen))
		for name := range seen {
			unknown = append(unknown, name)
		}

		sort.Strings(unknown)
	}

	return Result{
		Markdown: renderMarkdown(root),
		Plain:    renderPlain(root),
		Unknown:  unknown,
	}, nil
}
