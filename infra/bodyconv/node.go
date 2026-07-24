package bodyconv

import (
	"encoding/json"
	"fmt"
)

// Node type names the renderers work with (the TipTap spelling); the
// ProseMirror snake_case spellings normalize to these on parse.
const (
	nodeDoc            = "doc"
	nodeParagraph      = "paragraph"
	nodeText           = "text"
	nodeHeading        = "heading"
	nodeBulletList     = "bulletList"
	nodeOrderedList    = "orderedList"
	nodeListItem       = "listItem"
	nodeCodeBlock      = "codeBlock"
	nodeBlockquote     = "blockquote"
	nodeHorizontalRule = "horizontalRule"
	nodeHardBreak      = "hardBreak"
	nodeImage          = "image"
)

// Mark type names.
const (
	markBold      = "bold"
	markItalic    = "italic"
	markStrike    = "strike"
	markCode      = "code"
	markLink      = "link"
	markUnderline = "underline"
)

// typeAliases maps the ProseMirror schema spellings to the ones above.
var typeAliases = map[string]string{
	"bullet_list":     nodeBulletList,
	"ordered_list":    nodeOrderedList,
	"list_item":       nodeListItem,
	"code_block":      nodeCodeBlock,
	"horizontal_rule": nodeHorizontalRule,
	"hard_break":      nodeHardBreak,
	"strong":          markBold,
	"em":              markItalic,
}

var knownNodes = map[string]bool{
	nodeDoc:            true,
	nodeParagraph:      true,
	nodeText:           true,
	nodeHeading:        true,
	nodeBulletList:     true,
	nodeOrderedList:    true,
	nodeListItem:       true,
	nodeCodeBlock:      true,
	nodeBlockquote:     true,
	nodeHorizontalRule: true,
	nodeHardBreak:      true,
	nodeImage:          true,
}

// knownMarks includes underline: it has no markdown equivalent, so the text
// renders unstyled, but it is a recognized mark, not a surprise to report.
var knownMarks = map[string]bool{
	markBold:      true,
	markItalic:    true,
	markStrike:    true,
	markCode:      true,
	markLink:      true,
	markUnderline: true,
}

// node is one vertex of the editor document tree.
type node struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []mark         `json:"marks"`
	Content []node         `json:"content"`
}

// mark is an inline formatting annotation on a text node.
type mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

func parseDocument(doc []byte) (*node, error) {
	var root node

	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("bodyconv: parse document: %w", err)
	}

	normalize(&root)

	if root.Type != nodeDoc {
		return nil, ErrNotDocument
	}

	return &root, nil
}

func normalize(n *node) {
	if alias, ok := typeAliases[n.Type]; ok {
		n.Type = alias
	}

	for i := range n.Marks {
		if alias, ok := typeAliases[n.Marks[i].Type]; ok {
			n.Marks[i].Type = alias
		}
	}

	for i := range n.Content {
		normalize(&n.Content[i])
	}
}

// collectUnknown gathers unrecognized node and mark types; a missing type is
// not worth reporting, the node still renders as a transparent container.
func collectUnknown(n *node, seen map[string]bool) {
	if n.Type != "" && !knownNodes[n.Type] {
		seen[n.Type] = true
	}

	for _, m := range n.Marks {
		if m.Type != "" && !knownMarks[m.Type] {
			seen[m.Type] = true
		}
	}

	for i := range n.Content {
		collectUnknown(&n.Content[i], seen)
	}
}

// rawText concatenates the text of the whole subtree, with hard breaks as
// newlines and no formatting at all.
func rawText(n *node) string {
	if n.Type == nodeHardBreak {
		return "\n"
	}

	out := n.Text
	for i := range n.Content {
		out += rawText(&n.Content[i])
	}

	return out
}

func (n *node) attrString(key string) string {
	if v, ok := n.Attrs[key].(string); ok {
		return v
	}

	return ""
}

// attrInt reads a numeric attribute; JSON numbers decode as float64.
func (n *node) attrInt(key string, def int) int {
	if v, ok := n.Attrs[key].(float64); ok {
		return int(v)
	}

	return def
}

func (n *node) hasMark(name string) bool {
	for _, m := range n.Marks {
		if m.Type == name {
			return true
		}
	}

	return false
}

func (n *node) linkHref() string {
	for _, m := range n.Marks {
		if m.Type == markLink {
			if href, ok := m.Attrs["href"].(string); ok {
				return href
			}

			return ""
		}
	}

	return ""
}
