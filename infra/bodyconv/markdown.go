package bodyconv

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// maxOrderedStart is the largest list start CommonMark accepts (nine digits).
const maxOrderedStart = 999_999_999

// renderMarkdown serializes the document as CommonMark: ATX headings, fenced
// code and marker-width list indentation, so a markdown-aware splitter sees
// the same structure the editor showed.
func renderMarkdown(root *node) string {
	return strings.TrimSpace(mdBlocks(root.Content))
}

// mdBlocks renders a block sequence; empty blocks are dropped, the rest join
// with a blank line.
func mdBlocks(nodes []node) string {
	blocks := make([]string, 0, len(nodes))

	for i := range nodes {
		if b := mdBlock(&nodes[i]); b != "" {
			blocks = append(blocks, b)
		}
	}

	return strings.Join(blocks, "\n\n")
}

func mdBlock(n *node) string {
	switch n.Type {
	case nodeParagraph:
		return escapeLineStarts(mdInline(n.Content))
	case nodeHeading:
		// An ATX heading is single-line: a hard break inside it renders as a
		// space so the heading text stays whole.
		text := mdInlineBreak(n.Content, " ")
		if text == "" {
			return ""
		}

		return strings.Repeat("#", headingLevel(n)) + " " + text
	case nodeBulletList:
		return mdList(n.Content, func(int) string { return "- " })
	case nodeOrderedList:
		start := n.attrInt("start", 1)
		if start < 1 {
			start = 1
		}

		if start > maxOrderedStart {
			start = maxOrderedStart
		}

		return mdList(n.Content, func(i int) string { return strconv.Itoa(start+i) + ". " })
	case nodeCodeBlock:
		return mdCodeBlock(n)
	case nodeBlockquote:
		return mdQuote(mdBlocks(n.Content))
	case nodeHorizontalRule:
		return "---"
	case nodeImage:
		return mdImage(n)
	case nodeText, nodeHardBreak:
		// An inline node directly at block level: render it as a bare line.
		return escapeLineStarts(mdInline([]node{*n}))
	default:
		// Unknown node: a transparent container; both its own text and its
		// children are kept, so no content is lost.
		parts := make([]string, 0, 2)

		if t := escapeLineStarts(escapeText(n.Text)); t != "" {
			parts = append(parts, t)
		}

		if c := mdBlocks(n.Content); c != "" {
			parts = append(parts, c)
		}

		return strings.Join(parts, "\n\n")
	}
}

func headingLevel(n *node) int {
	level := n.attrInt("level", 1)
	if level < 1 {
		level = 1
	}

	if level > 6 {
		level = 6
	}

	return level
}

// mdList renders the items of one list; marker yields the item prefix by
// index ("- ", "3. ").
func mdList(items []node, marker func(int) string) string {
	rendered := make([]string, 0, len(items))

	for i := range items {
		rendered = append(rendered, mdItem(marker(i), mdBlocks(items[i].Content)))
	}

	return strings.Join(rendered, "\n")
}

// mdItem prefixes the first line of body with the marker and indents the
// remaining lines to the marker width, which is what makes nested blocks
// belong to the item.
func mdItem(marker, body string) string {
	indent := strings.Repeat(" ", len(marker))
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		switch {
		case i == 0:
			lines[i] = strings.TrimRight(marker+line, " ")
		case line == "":
			// Blank separator lines stay blank.
		default:
			lines[i] = indent + line
		}
	}

	return strings.Join(lines, "\n")
}

func mdCodeBlock(n *node) string {
	text := rawText(n)
	fence := codeFence(text)

	// A backtick fence cannot carry backticks in its info string, and a
	// newline would inject arbitrary lines; such a language is dropped.
	lang := n.attrString("language")
	if strings.ContainsAny(lang, "`\n\r") {
		lang = ""
	}

	var b strings.Builder

	b.WriteString(fence)
	b.WriteString(lang)
	b.WriteString("\n")

	if text != "" {
		b.WriteString(text)

		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
	}

	b.WriteString(fence)

	return b.String()
}

// codeFence returns a fence longer than any backtick run inside the code, so
// the content can never terminate the block early.
func codeFence(text string) string {
	longest, current := 0, 0

	for _, r := range text {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}

	return strings.Repeat("`", max(3, longest+1))
}

func mdQuote(body string) string {
	if body == "" {
		return ""
	}

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}

	return strings.Join(lines, "\n")
}

func mdImage(n *node) string {
	alt := escapeText(n.attrString("alt"))
	src := mdDestination(n.attrString("src"))

	if title := n.attrString("title"); title != "" {
		return "![" + alt + "](" + src + ` "` + titleEscaper.Replace(title) + `")`
	}

	return "![" + alt + "](" + src + ")"
}

// mdDestination renders a link or image target. A target with whitespace or
// parentheses only parses inside the angle-bracket form.
func mdDestination(dest string) string {
	if strings.ContainsAny(dest, " \t\n\r()<>") {
		return "<" + destEscaper.Replace(dest) + ">"
	}

	return dest
}

var (
	// destEscaper keeps a target valid inside angle brackets: brackets are
	// backslash-escaped, line breaks and tabs become percent escapes.
	destEscaper = strings.NewReplacer(
		`\`, `\\`, "<", `\<`, ">", `\>`,
		"\n", "%0A", "\r", "%0D", "\t", "%09",
	)

	// titleEscaper keeps a double-quoted image title from terminating early.
	titleEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
)

func mdInline(nodes []node) string {
	return mdInlineBreak(nodes, "\\\n")
}

// mdInlineBreak renders inline content with the given hard-break form.
func mdInlineBreak(nodes []node, br string) string {
	merged := mergeTextRuns(nodes)

	// A break with nothing after it renders as nothing.
	for len(merged) > 0 && merged[len(merged)-1].Type == nodeHardBreak {
		merged = merged[:len(merged)-1]
	}

	var b strings.Builder

	for i := range merged {
		n := &merged[i]

		switch n.Type {
		case nodeText:
			b.WriteString(mdText(n))
		case nodeHardBreak:
			b.WriteString(br)
		case nodeImage:
			b.WriteString(mdImage(n))
		default:
			// Unknown inline node: keep its text and children.
			b.WriteString(escapeText(n.Text))
			b.WriteString(mdInlineBreak(n.Content, br))
		}
	}

	return b.String()
}

// mergeTextRuns joins adjacent text nodes carrying identical marks: rendering
// them separately would double the delimiters ("**a****b**"), which markdown
// reads back differently.
func mergeTextRuns(nodes []node) []node {
	out := make([]node, 0, len(nodes))

	for _, n := range nodes {
		if n.Type == nodeText && len(out) > 0 {
			last := &out[len(out)-1]
			if last.Type == nodeText && reflect.DeepEqual(last.Marks, n.Marks) {
				last.Text += n.Text

				continue
			}
		}

		out = append(out, n)
	}

	return out
}

// mdText wraps one text run into its mark delimiters. The wrap order is
// fixed, so equal documents render byte-identically no matter how the editor
// ordered the marks; underline has no markdown form and renders unstyled.
func mdText(n *node) string {
	if n.Text == "" {
		return ""
	}

	text := n.Text
	lead, trail := "", ""

	if n.hasMark(markBold) || n.hasMark(markItalic) || n.hasMark(markStrike) {
		// Emphasis delimiters touching whitespace neither open nor close per
		// the flanking rules; enclosing whitespace moves outside them.
		core := strings.TrimLeft(text, " \t")
		lead = text[:len(text)-len(core)]
		trimmed := strings.TrimRight(core, " \t")
		trail = core[len(trimmed):]
		text = trimmed

		// A whitespace-only run has nothing to emphasize.
		if text == "" {
			return n.Text
		}
	}

	var s string
	if n.hasMark(markCode) {
		s = codeSpan(text)
	} else {
		s = escapeText(text)
	}

	if n.hasMark(markBold) {
		s = "**" + s + "**"
	}

	if n.hasMark(markItalic) {
		s = "*" + s + "*"
	}

	if n.hasMark(markStrike) {
		s = "~~" + s + "~~"
	}

	s = lead + s + trail

	if href := n.linkHref(); href != "" {
		s = "[" + s + "](" + mdDestination(href) + ")"
	}

	return s
}

// codeSpan wraps text into a span delimiter longer than any backtick run
// inside it. Padding spaces are added when the content touches the delimiter
// or is space-edged on both sides (the reader strips one space back).
func codeSpan(text string) string {
	delim := "`"
	for strings.Contains(text, delim) {
		delim += "`"
	}

	spaceEdged := strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") &&
		strings.TrimSpace(text) != ""
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") || spaceEdged {
		return delim + " " + text + " " + delim
	}

	return delim + text + delim
}

// escapeText neutralizes the characters that would toggle inline formatting,
// raw HTML, autolinks or entity references wherever they stand; line-start
// markers are handled positionally instead.
var escapeText = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	`*`, `\*`,
	`_`, `\_`,
	`[`, `\[`,
	`]`, `\]`,
	`~`, `\~`,
	`<`, `\<`,
	`&`, `\&`,
).Replace

// Line-start patterns that would read as block markers; each allows the
// up-to-3-space indent the reader still honors.
var (
	setextLineRe  = regexp.MustCompile(`^( {0,3})[-=][-=\t ]*$`)
	atxLineRe     = regexp.MustCompile(`^( {0,3})#{1,6}($|[ \t])`)
	quoteLineRe   = regexp.MustCompile(`^( {0,3})>`)
	bulletLineRe  = regexp.MustCompile(`^( {0,3})[+-]($|[ \t])`)
	orderedLineRe = regexp.MustCompile(`^( {0,3})(\d{1,9})([.)])($|[ \t])`)
)

// escapeLineStarts keeps a line of ordinary text from reading as block
// structure: a list or quote marker, an ATX heading run, or a line of dashes
// or equals signs that would turn into a thematic break or promote the
// previous line to a setext heading.
func escapeLineStarts(s string) string {
	if s == "" {
		return ""
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = escapeLineStart(line)
	}

	return strings.Join(lines, "\n")
}

// escapeLineStart neutralizes one leading block marker: a backslash before
// the marker char (or before the punctuation of an ordered marker) keeps the
// line ordinary text.
func escapeLineStart(line string) string {
	for _, re := range [...]*regexp.Regexp{setextLineRe, atxLineRe, quoteLineRe, bulletLineRe} {
		if m := re.FindStringSubmatchIndex(line); m != nil {
			at := m[3] // end of the indent group

			return line[:at] + `\` + line[at:]
		}
	}

	return orderedLineRe.ReplaceAllString(line, `$1$2\$3$4`)
}
