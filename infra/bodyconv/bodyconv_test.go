package bodyconv

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// JSON fixture builders: content is passed already JSON-escaped.

func docJSON(blocks ...string) string {
	return `{"type":"doc","content":[` + strings.Join(blocks, ",") + `]}`
}

func para(inline ...string) string {
	return `{"type":"paragraph","content":[` + strings.Join(inline, ",") + `]}`
}

func text(s string) string {
	return `{"type":"text","text":"` + s + `"}`
}

func textMarked(s string, marks ...string) string {
	return `{"type":"text","text":"` + s + `","marks":[` + strings.Join(marks, ",") + `]}`
}

type convertCase struct {
	name        string
	doc         string
	wantMD      string
	wantPlain   string
	wantUnknown []string
}

func runConvertCases(t *testing.T, tests []convertCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Convert([]byte(tt.doc))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}

			if got.Markdown != tt.wantMD {
				t.Fatalf("markdown = %q, want %q", got.Markdown, tt.wantMD)
			}

			if got.Plain != tt.wantPlain {
				t.Fatalf("plain = %q, want %q", got.Plain, tt.wantPlain)
			}

			if !slices.Equal(got.Unknown, tt.wantUnknown) {
				t.Fatalf("unknown = %v, want %v", got.Unknown, tt.wantUnknown)
			}
		})
	}
}

func TestConvertBlocks(t *testing.T) {
	runConvertCases(t, []convertCase{
		{
			name: "empty document",
			doc:  docJSON(),
		},
		{
			name: "document without content key",
			doc:  `{"type":"doc"}`,
		},
		{
			name:      "paragraphs and heading",
			doc:       docJSON(`{"type":"heading","attrs":{"level":2},"content":[`+text("Заголовок")+`]}`, para(text("перший")), para(text("другий"))),
			wantMD:    "## Заголовок\n\nперший\n\nдругий",
			wantPlain: "Заголовок\n\nперший\n\nдругий",
		},
		{
			name:      "heading level clamps to 1..6",
			doc:       docJSON(`{"type":"heading","attrs":{"level":9},"content":[`+text("Глибокий")+`]}`, `{"type":"heading","attrs":{"level":0},"content":[`+text("Нульовий")+`]}`, `{"type":"heading","content":[`+text("Без атрибута")+`]}`),
			wantMD:    "###### Глибокий\n\n# Нульовий\n\n# Без атрибута",
			wantPlain: "Глибокий\n\nНульовий\n\nБез атрибута",
		},
		{
			name:      "bullet list",
			doc:       docJSON(`{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("один")) + `]},{"type":"listItem","content":[` + para(text("два")) + `]}]}`),
			wantMD:    "- один\n- два",
			wantPlain: "один\nдва",
		},
		{
			name:      "ordered list honors start",
			doc:       docJSON(`{"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[` + para(text("а")) + `]},{"type":"listItem","content":[` + para(text("б")) + `]}]}`),
			wantMD:    "3. а\n4. б",
			wantPlain: "а\nб",
		},
		{
			name:      "nested list indents to marker width",
			doc:       docJSON(`{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("верх")) + `,{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("вкладений")) + `]}]}]}]}`),
			wantMD:    "- верх\n\n  - вкладений",
			wantPlain: "верх\nвкладений",
		},
		{
			name:      "multi-block list item keeps continuation indent",
			doc:       docJSON(`{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("перший")) + `,` + para(text("другий")) + `]}]}`),
			wantMD:    "- перший\n\n  другий",
			wantPlain: "перший\nдругий",
		},
		{
			name:      "code block with language",
			doc:       docJSON(`{"type":"codeBlock","attrs":{"language":"bash"},"content":[` + text("echo hi") + `]}`),
			wantMD:    "```bash\necho hi\n```",
			wantPlain: "echo hi",
		},
		{
			name:      "code block content is not escaped",
			doc:       docJSON(`{"type":"codeBlock","content":[` + text(`**bold** and _under_`) + `]}`),
			wantMD:    "```\n**bold** and _under_\n```",
			wantPlain: "**bold** and _under_",
		},
		{
			name:      "code block fence outgrows inner backticks",
			doc:       docJSON(`{"type":"codeBlock","content":[` + text("```\\ninner\\n```") + `]}`),
			wantMD:    "````\n```\ninner\n```\n````",
			wantPlain: "```\ninner\n```",
		},
		{
			name:      "blockquote prefixes every line",
			doc:       docJSON(`{"type":"blockquote","content":[` + para(text("цитата")) + `,` + para(text("далі")) + `]}`),
			wantMD:    "> цитата\n>\n> далі",
			wantPlain: "цитата\n\nдалі",
		},
		{
			name:      "horizontal rule renders only in markdown",
			doc:       docJSON(para(text("до")), `{"type":"horizontalRule"}`, para(text("після"))),
			wantMD:    "до\n\n---\n\nпісля",
			wantPlain: "до\n\nпісля",
		},
		{
			name:      "image with alt and title",
			doc:       docJSON(`{"type":"image","attrs":{"src":"https://x/img.png","alt":"схема","title":"огляд"}}`),
			wantMD:    `![схема](https://x/img.png "огляд")`,
			wantPlain: "схема",
		},
		{
			name:      "image without title",
			doc:       docJSON(`{"type":"image","attrs":{"src":"https://x/img.png","alt":"схема"}}`),
			wantMD:    "![схема](https://x/img.png)",
			wantPlain: "схема",
		},
		{
			name:      "empty paragraph and empty heading are dropped",
			doc:       docJSON(para(), `{"type":"heading","attrs":{"level":2}}`, para(text("x"))),
			wantMD:    "x",
			wantPlain: "x",
		},
		{
			name:      "list inside blockquote",
			doc:       docJSON(`{"type":"blockquote","content":[{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("пункт")) + `]},{"type":"listItem","content":[` + para(text("ще")) + `]}]}]}`),
			wantMD:    "> - пункт\n> - ще",
			wantPlain: "пункт\nще",
		},
		{
			name:      "code block inside list item",
			doc:       docJSON(`{"type":"bulletList","content":[{"type":"listItem","content":[` + para(text("код:")) + `,{"type":"codeBlock","content":[` + text("echo") + `]}]}]}`),
			wantMD:    "- код:\n\n  ```\n  echo\n  ```",
			wantPlain: "код:\necho",
		},
		{
			name:      "empty blockquote is dropped",
			doc:       docJSON(`{"type":"blockquote","content":[`+para()+`]}`, para(text("x"))),
			wantMD:    "x",
			wantPlain: "x",
		},
		{
			name:      "empty list item has no trailing space",
			doc:       docJSON(`{"type":"bulletList","content":[{"type":"listItem","content":[]},{"type":"listItem","content":[` + para(text("x")) + `]}]}`),
			wantMD:    "-\n- x",
			wantPlain: "x",
		},
		{
			name:      "inline image inside paragraph",
			doc:       docJSON(para(text("див "), `{"type":"image","attrs":{"src":"https://x/i.png","alt":"схема"}}`)),
			wantMD:    "див ![схема](https://x/i.png)",
			wantPlain: "див схема",
		},
		{
			name:      "bare text node at block level",
			doc:       docJSON(text("просто")),
			wantMD:    "просто",
			wantPlain: "просто",
		},
		{
			name:      "hard break inside code block",
			doc:       docJSON(`{"type":"codeBlock","content":[` + text("a") + `,{"type":"hardBreak"},` + text("b") + `]}`),
			wantMD:    "```\na\nb\n```",
			wantPlain: "a\nb",
		},
		{
			name:      "code text with trailing newline is not doubled",
			doc:       docJSON(`{"type":"codeBlock","content":[` + text(`echo\n`) + `]}`),
			wantMD:    "```\necho\n```",
			wantPlain: "echo",
		},
		{
			name:      "ordered start below one clamps",
			doc:       docJSON(`{"type":"orderedList","attrs":{"start":-5},"content":[{"type":"listItem","content":[` + para(text("а")) + `]}]}`),
			wantMD:    "1. а",
			wantPlain: "а",
		},
		{
			name:      "ordered start above the nine-digit cap clamps",
			doc:       docJSON(`{"type":"orderedList","attrs":{"start":1e19},"content":[{"type":"listItem","content":[` + para(text("а")) + `]}]}`),
			wantMD:    "999999999. а",
			wantPlain: "а",
		},
		{
			name:      "language with backtick is dropped from the fence",
			doc:       docJSON("{\"type\":\"codeBlock\",\"attrs\":{\"language\":\"a`b\"},\"content\":[" + text("x") + "]}"),
			wantMD:    "```\nx\n```",
			wantPlain: "x",
		},
		{
			name:      "image title with quote is escaped",
			doc:       docJSON(`{"type":"image","attrs":{"src":"https://x/i.png","alt":"а","title":"він \"так\""}}`),
			wantMD:    "![а](https://x/i.png \"він \\\"так\\\"\")",
			wantPlain: "а",
		},
		{
			name:      "hard break inside heading becomes a space",
			doc:       docJSON(`{"type":"heading","attrs":{"level":2},"content":[` + text("перший") + `,{"type":"hardBreak"},` + text("другий") + `]}`),
			wantMD:    "## перший другий",
			wantPlain: "перший\nдругий",
		},
		{
			name:      "paragraph trailing break next to a block collapses in plain",
			doc:       docJSON(para(text("a"), `{"type":"hardBreak"}`), para(text("b"))),
			wantMD:    "a\n\nb",
			wantPlain: "a\n\nb",
		},
	})
}

func TestConvertMarks(t *testing.T) {
	runConvertCases(t, []convertCase{
		{
			name:      "bold",
			doc:       docJSON(para(textMarked("жирний", `{"type":"bold"}`))),
			wantMD:    "**жирний**",
			wantPlain: "жирний",
		},
		{
			name:      "bold italic strike stack in fixed order",
			doc:       docJSON(para(textMarked("текст", `{"type":"strike"}`, `{"type":"bold"}`, `{"type":"italic"}`))),
			wantMD:    "~~***текст***~~",
			wantPlain: "текст",
		},
		{
			name:      "code span",
			doc:       docJSON(para(textMarked("код", `{"type":"code"}`))),
			wantMD:    "`код`",
			wantPlain: "код",
		},
		{
			name:      "code span with inner backtick extends delimiter",
			doc:       docJSON(para(textMarked("a`b", `{"type":"code"}`))),
			wantMD:    "``a`b``",
			wantPlain: "a`b",
		},
		{
			name:      "code span touching backtick gets padding",
			doc:       docJSON(para(textMarked("`edge", `{"type":"code"}`))),
			wantMD:    "`` `edge ``",
			wantPlain: "`edge",
		},
		{
			name:      "link",
			doc:       docJSON(para(textMarked("тут", `{"type":"link","attrs":{"href":"https://example.com"}}`))),
			wantMD:    "[тут](https://example.com)",
			wantPlain: "тут",
		},
		{
			name:      "link wraps other marks",
			doc:       docJSON(para(textMarked("тут", `{"type":"link","attrs":{"href":"https://example.com"}}`, `{"type":"bold"}`))),
			wantMD:    "[**тут**](https://example.com)",
			wantPlain: "тут",
		},
		{
			name:      "underline renders unstyled and is not unknown",
			doc:       docJSON(para(textMarked("підкреслений", `{"type":"underline"}`))),
			wantMD:    "підкреслений",
			wantPlain: "підкреслений",
		},
		{
			name:      "adjacent runs with same marks merge",
			doc:       docJSON(para(textMarked("аб", `{"type":"bold"}`), textMarked("вг", `{"type":"bold"}`))),
			wantMD:    "**абвг**",
			wantPlain: "абвг",
		},
		{
			name:      "adjacent runs with different marks stay apart",
			doc:       docJSON(para(textMarked("аб", `{"type":"bold"}`), text("вг"))),
			wantMD:    "**аб**вг",
			wantPlain: "абвг",
		},
		{
			name:      "hard break inside paragraph",
			doc:       docJSON(para(text("перший"), `{"type":"hardBreak"}`, text("другий"))),
			wantMD:    "перший\\\nдругий",
			wantPlain: "перший\nдругий",
		},
		{
			name:      "trailing hard break is dropped",
			doc:       docJSON(para(text("текст"), `{"type":"hardBreak"}`)),
			wantMD:    "текст",
			wantPlain: "текст",
		},
		{
			name:      "empty text with marks renders nothing",
			doc:       docJSON(para(textMarked("", `{"type":"bold"}`), text("x"))),
			wantMD:    "x",
			wantPlain: "x",
		},
		{
			name:      "emphasis expels enclosing whitespace",
			doc:       docJSON(para(text("до"), textMarked(" середина ", `{"type":"bold"}`), text("після"))),
			wantMD:    "до **середина** після",
			wantPlain: "до середина після",
		},
		{
			name:      "whitespace-only emphasized run keeps the space, drops delimiters",
			doc:       docJSON(para(text("до"), textMarked(" ", `{"type":"bold"}`), text("після"))),
			wantMD:    "до після",
			wantPlain: "до після",
		},
		{
			name:      "code span with trailing backtick gets padding",
			doc:       docJSON(para(textMarked("edge`", `{"type":"code"}`))),
			wantMD:    "`` edge` ``",
			wantPlain: "edge`",
		},
		{
			name:      "code span space-edged on both sides gets padding",
			doc:       docJSON(para(textMarked(" x ", `{"type":"code"}`))),
			wantMD:    "`  x  `",
			wantPlain: "x",
		},
		{
			name:      "href with space uses the angle-bracket form",
			doc:       docJSON(para(textMarked("тут", `{"type":"link","attrs":{"href":"https://x/a b"}}`))),
			wantMD:    "[тут](<https://x/a b>)",
			wantPlain: "тут",
		},
		{
			name:      "href with parenthesis uses the angle-bracket form",
			doc:       docJSON(para(textMarked("тут", `{"type":"link","attrs":{"href":"https://x/wiki/Foo_(bar"}}`))),
			wantMD:    "[тут](<https://x/wiki/Foo_(bar>)",
			wantPlain: "тут",
		},
	})
}

func TestConvertEscaping(t *testing.T) {
	runConvertCases(t, []convertCase{
		{
			name:      "inline specials are escaped in markdown only",
			doc:       docJSON(para(text(`ці *зірки* і _риски_ та [дужки]`))),
			wantMD:    `ці \*зірки\* і \_риски\_ та \[дужки\]`,
			wantPlain: "ці *зірки* і _риски_ та [дужки]",
		},
		{
			name:      "backslash is escaped",
			doc:       docJSON(para(text(`шлях C:\\dir`))),
			wantMD:    `шлях C:\\dir`,
			wantPlain: `шлях C:\dir`,
		},
		{
			name:      "line-start dash does not become a list",
			doc:       docJSON(para(text("- не список"))),
			wantMD:    `\- не список`,
			wantPlain: "- не список",
		},
		{
			name:      "line-start number does not become an ordered list",
			doc:       docJSON(para(text("1. не список"))),
			wantMD:    `1\. не список`,
			wantPlain: "1. не список",
		},
		{
			name:      "line-start hash does not become a heading",
			doc:       docJSON(para(text("# не заголовок"))),
			wantMD:    `\# не заголовок`,
			wantPlain: "# не заголовок",
		},
		{
			name:      "line start after hard break is escaped too",
			doc:       docJSON(para(text("текст"), `{"type":"hardBreak"}`, text("- другий рядок"))),
			wantMD:    "текст\\\n\\- другий рядок",
			wantPlain: "текст\n- другий рядок",
		},
		{
			name:      "dash inside a line stays intact",
			doc:       docJSON(para(text("тире - всередині"))),
			wantMD:    "тире - всередині",
			wantPlain: "тире - всередині",
		},
		{
			name:      "code span suppresses escaping",
			doc:       docJSON(para(textMarked("*не жирний*", `{"type":"code"}`))),
			wantMD:    "`*не жирний*`",
			wantPlain: "*не жирний*",
		},
		{
			name:      "line-start heading run is escaped",
			doc:       docJSON(para(text("## Розділ"))),
			wantMD:    `\## Розділ`,
			wantPlain: "## Розділ",
		},
		{
			name:      "seven hashes are not a heading and stay literal",
			doc:       docJSON(para(text("####### x"))),
			wantMD:    "####### x",
			wantPlain: "####### x",
		},
		{
			name:      "line-start quote marker without space is escaped",
			doc:       docJSON(para(text(">цитата"))),
			wantMD:    `\>цитата`,
			wantPlain: ">цитата",
		},
		{
			name:      "dash-only line does not become a thematic break",
			doc:       docJSON(para(text("---"))),
			wantMD:    `\---`,
			wantPlain: "---",
		},
		{
			name:      "dash line after hard break does not become a setext heading",
			doc:       docJSON(para(text("Ціни"), `{"type":"hardBreak"}`, text("---"))),
			wantMD:    "Ціни\\\n\\---",
			wantPlain: "Ціни\n---",
		},
		{
			name:      "equals-only line does not become a setext heading",
			doc:       docJSON(para(text("==="))),
			wantMD:    `\===`,
			wantPlain: "===",
		},
		{
			name:      "tildes are escaped so no fence or strike appears",
			doc:       docJSON(para(text("~~~ приклад і ~~не страйк~~"))),
			wantMD:    `\~\~\~ приклад і \~\~не страйк\~\~`,
			wantPlain: "~~~ приклад і ~~не страйк~~",
		},
		{
			name:      "line-start plus does not become a list",
			doc:       docJSON(para(text("+ не список"))),
			wantMD:    `\+ не список`,
			wantPlain: "+ не список",
		},
		{
			name:      "line-start number with parenthesis does not become a list",
			doc:       docJSON(para(text("1) не список"))),
			wantMD:    `1\) не список`,
			wantPlain: "1) не список",
		},
		{
			name:      "html tag and entity are escaped",
			doc:       docJSON(para(text("тег <b> і &amp;"))),
			wantMD:    `тег \<b> і \&amp;`,
			wantPlain: "тег <b> і &amp;",
		},
	})
}

func TestConvertUnknown(t *testing.T) {
	runConvertCases(t, []convertCase{
		{
			name:        "unknown block tree stays transparent",
			doc:         docJSON(`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[` + para(text("клітинка")) + `]}]}]}`),
			wantMD:      "клітинка",
			wantPlain:   "клітинка",
			wantUnknown: []string{"table", "tableCell", "tableRow"},
		},
		{
			name:        "unknown inline leaf keeps its text",
			doc:         docJSON(para(text("до "), `{"type":"variable","text":"X"}`, text(" після"))),
			wantMD:      "до X після",
			wantPlain:   "до X після",
			wantUnknown: []string{"variable"},
		},
		{
			name:        "unknown mark renders unstyled",
			doc:         docJSON(para(textMarked("виділене", `{"type":"highlight"}`))),
			wantMD:      "виділене",
			wantPlain:   "виділене",
			wantUnknown: []string{"highlight"},
		},
		{
			name:        "unknown types are deduplicated",
			doc:         docJSON(`{"type":"callout","content":[`+para(text("раз"))+`]}`, `{"type":"callout","content":[`+para(text("два"))+`]}`),
			wantMD:      "раз\n\nдва",
			wantPlain:   "раз\n\nдва",
			wantUnknown: []string{"callout"},
		},
		{
			name:      "prosemirror spellings are aliases, not unknown",
			doc:       docJSON(`{"type":"bullet_list","content":[{"type":"list_item","content":[` + para(textMarked("жирний", `{"type":"strong"}`), text(" "), textMarked("курсив", `{"type":"em"}`)) + `]}]}`),
			wantMD:    "- **жирний** *курсив*",
			wantPlain: "жирний курсив",
		},
		{
			name:      "prosemirror block spellings keep their structure",
			doc:       docJSON(`{"type":"code_block","content":[`+text("echo")+`]}`, `{"type":"horizontal_rule"}`, para(text("a"), `{"type":"hard_break"}`, text("b"))),
			wantMD:    "```\necho\n```\n\n---\n\na\\\nb",
			wantPlain: "echo\n\na\nb",
		},
		{
			name:        "unknown node with both text and content loses neither",
			doc:         docJSON(`{"type":"widget","text":"збережено","content":[` + para(text("діти")) + `]}`),
			wantMD:      "збережено\n\nдіти",
			wantPlain:   "збережено\n\nдіти",
			wantUnknown: []string{"widget"},
		},
		{
			name:      "node without type renders transparently and is not reported",
			doc:       docJSON(`{"content":[` + para(text("x")) + `]}`),
			wantMD:    "x",
			wantPlain: "x",
		},
	})
}

func TestConvertInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		notDocument bool
	}{
		{name: "empty input", doc: ""},
		{name: "malformed json", doc: `{"type":`},
		{name: "json scalar", doc: `42`},
		{name: "json null", doc: `null`, notDocument: true},
		{name: "root is not a document", doc: para(text("x")), notDocument: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.doc))
			if err == nil {
				t.Fatal("Convert succeeded, want error")
			}

			if got := errors.Is(err, ErrNotDocument); got != tt.notDocument {
				t.Fatalf("errors.Is(err, ErrNotDocument) = %v, want %v (err: %v)", got, tt.notDocument, err)
			}
		})
	}
}

func TestConvertMarkOrderStable(t *testing.T) {
	first, err := Convert([]byte(docJSON(para(textMarked("текст", `{"type":"bold"}`, `{"type":"italic"}`)))))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	second, err := Convert([]byte(docJSON(para(textMarked("текст", `{"type":"italic"}`, `{"type":"bold"}`)))))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if first.Markdown != second.Markdown {
		t.Fatalf("mark order changed rendering: %q vs %q", first.Markdown, second.Markdown)
	}
}

func TestConvertComposite(t *testing.T) {
	doc := docJSON(
		`{"type":"heading","attrs":{"level":1},"content":[`+text("Як скинути пароль")+`]}`,
		para(
			text("Натисніть "),
			textMarked("Забув пароль", `{"type":"bold"}`),
			text(" і введіть "),
			textMarked("email", `{"type":"code"}`),
			text("."),
		),
		`{"type":"bulletList","content":[`+
			`{"type":"listItem","content":[`+para(text("Крок один"))+`]},`+
			`{"type":"listItem","content":[`+para(text("Крок два"))+`,`+
			`{"type":"orderedList","content":[{"type":"listItem","content":[`+para(text("Підкрок"))+`]}]}]}]}`,
		`{"type":"codeBlock","attrs":{"language":"bash"},"content":[`+text("curl https://api.example.com/reset")+`]}`,
		`{"type":"blockquote","content":[`+para(text("Порада: перевірте спам."))+`]}`,
		`{"type":"horizontalRule"}`,
		para(textMarked("Докладніше", `{"type":"link","attrs":{"href":"https://kb.example.com"}}`)),
	)

	wantMD := "# Як скинути пароль\n\n" +
		"Натисніть **Забув пароль** і введіть `email`.\n\n" +
		"- Крок один\n" +
		"- Крок два\n\n" +
		"  1. Підкрок\n\n" +
		"```bash\ncurl https://api.example.com/reset\n```\n\n" +
		"> Порада: перевірте спам.\n\n" +
		"---\n\n" +
		"[Докладніше](https://kb.example.com)"

	wantPlain := "Як скинути пароль\n\n" +
		"Натисніть Забув пароль і введіть email.\n\n" +
		"Крок один\nКрок два\nПідкрок\n\n" +
		"curl https://api.example.com/reset\n\n" +
		"Порада: перевірте спам.\n\n" +
		"Докладніше"

	got, err := Convert([]byte(doc))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	if got.Markdown != wantMD {
		t.Fatalf("markdown:\n%s\n---want---\n%s", got.Markdown, wantMD)
	}

	if got.Plain != wantPlain {
		t.Fatalf("plain:\n%s\n---want---\n%s", got.Plain, wantPlain)
	}

	if got.Unknown != nil {
		t.Fatalf("unknown = %v, want none", got.Unknown)
	}

	again, err := Convert([]byte(doc))
	if err != nil {
		t.Fatalf("Convert (second run): %v", err)
	}

	if again.Markdown != got.Markdown || again.Plain != got.Plain {
		t.Fatal("conversion is not deterministic")
	}
}
