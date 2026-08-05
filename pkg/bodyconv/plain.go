package bodyconv

import "strings"

// renderPlain serializes the document as flat text: no markers, no escaping,
// blocks separated by a blank line and list items each on their own line.
func renderPlain(root *node) string {
	return strings.TrimSpace(collapseBlankLines(plainBlocks(root.Content)))
}

func plainBlocks(nodes []node) string {
	blocks := make([]string, 0, len(nodes))

	for i := range nodes {
		if b := plainBlock(&nodes[i]); b != "" {
			blocks = append(blocks, b)
		}
	}

	return strings.Join(blocks, "\n\n")
}

func plainBlock(n *node) string {
	switch n.Type {
	case nodeParagraph, nodeHeading:
		return plainInline(n.Content)
	case nodeBulletList, nodeOrderedList:
		items := make([]string, 0, len(n.Content))

		for i := range n.Content {
			if item := plainBlocks(n.Content[i].Content); item != "" {
				items = append(items, strings.ReplaceAll(item, "\n\n", "\n"))
			}
		}

		return strings.Join(items, "\n")
	case nodeCodeBlock:
		return strings.TrimRight(rawText(n), "\n")
	case nodeBlockquote:
		return plainBlocks(n.Content)
	case nodeHorizontalRule:
		return ""
	case nodeImage:
		return n.attrString("alt")
	case nodeText, nodeHardBreak:
		return plainInline([]node{*n})
	default:
		// Unknown node: a transparent container; both its own text and its
		// children are kept, so no content is lost.
		parts := make([]string, 0, 2)

		if n.Text != "" {
			parts = append(parts, n.Text)
		}

		if c := plainBlocks(n.Content); c != "" {
			parts = append(parts, c)
		}

		return strings.Join(parts, "\n\n")
	}
}

func plainInline(nodes []node) string {
	var b strings.Builder

	for i := range nodes {
		n := &nodes[i]

		switch n.Type {
		case nodeText:
			b.WriteString(n.Text)
		case nodeHardBreak:
			b.WriteString("\n")
		case nodeImage:
			b.WriteString(n.attrString("alt"))
		default:
			b.WriteString(n.Text)
			b.WriteString(plainInline(n.Content))
		}
	}

	return b.String()
}

func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}

	return s
}
