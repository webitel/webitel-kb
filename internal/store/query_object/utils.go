package queryobject

import (
	"io"
	"strings"
)

// CompactSQL collapses whitespace runs in an SQL text to single spaces and
// strips -- and /* */ comments, preserving quoted literals. Used so rendered
// queries and test expectations compare stably regardless of formatting.
func CompactSQL(s string) string {
	var (
		r = strings.NewReader(s)
		w strings.Builder
	)

	w.Grow(int(r.Size()))

	var (
		err  error
		char rune
		last rune
		hold rune

		isSpace = func() (is bool) {
			switch char {
			case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0:
				is = true
			}

			return is
		}
		isPunct = func(char rune) (is bool) {
			switch char {
			// none; start of text
			case 0:
				is = true
			// special
			// ':' USES [squirrel] for :named parameters,
			//     so we need to keep SPACE if there were any
			case ',', '(', ')', '[', ']', ';', '\'', '"':
				is = true
			// operators
			case '+', '-', '*', '/', '<', '>', '=', '~', '!', '@', '#', '%', '^', '&', '|':
				is = true
			}

			return is
		}
		isQuote = func() (is bool) {
			switch char {
			case '\'', '"':
				is = true
			}

			return is
		}
		// context
		space   bool // [IN] whitespace(s)
		quote   rune // [IN] literal(s); *QUOTE(s)
		comment rune // [IN] comment; [-|*]
		// helpers
		isComment = func() bool {
			switch comment {
			case '-':
				{
					// comment: close(\n)
					if char == '\n' { // EOL
						space = true // inject
						comment = 0  // close
						hold = 0     // clear
					}

					return true // still IN ...
				}
			case '*':
				{
					// comment: close(*/)
					if hold == 0 && char == '*' {
						// MAY: close(*/)
						hold = char
						// need more data ...
					} else if hold == '*' && char == '/' {
						space = true // inject
						comment = 0  // close
						hold = 0     // clear
					}

					return true // still IN ...
				}
				// default: 0
			}
			// NOTE: (comment == 0)
			switch hold {
			// comment: start(--)
			case '-': // single-line
				{
					if char == hold {
						hold = 0       // clear
						comment = char // start

						return true
					}

					return false
				}
			// comment: start(/*)
			case '/': // multi-line
				{
					if char == '*' {
						hold = 0       // clear
						comment = char // start

						return true
					}

					return false
				}
			case 0:
				{
					// NOTE: (hold == 0)
					switch char {
					case '-':
					case '/':
					default:
						// NOT alike ...
						return false
					}
					// need more data ...
					hold = char
					// DO NOT write(!)
					return true
				}
			default:
				{
					// NO match
					// need to write hold[ed] char
					return false
				}
			}
		}
		isLiteral = func() bool {
			if !isQuote() || last == '\\' { // ESC(\')
				return quote > 0 // We are IN ?
			}
			// close(?)
			if quote == char { // inLiteral(?)
				quote = 0

				return true // as last
			}
			// start(!)
			quote = char

			return true
		}
		// [re]write
		output = func() {
			if hold > 0 {
				if _, err := w.WriteRune(hold); err != nil {
					return
				}

				last = hold
				hold = 0
			}

			if space {
				space = false

				if !isPunct(last) && !isPunct(char) {
					if _, err := w.WriteRune(' '); err != nil { // INJECT SPACE(' ')
						return
					}
				}
			}

			if _, err := w.WriteRune(char); err != nil {
				return
			}

			last = char
		}
	)

	for {
		char, _, err = r.ReadRune()
		if err != nil {
			break
		}

		// Comment markers inside a quoted literal are literal text: '--x' is a
		// value, not a comment. (The upstream implementation checked comments
		// first and truncated such literals.)
		if quote == 0 && isComment() {
			// suppress; DO NOT write(!)
			continue
		}

		if isLiteral() {
			// [re]write: as is (!)
			output()

			continue
		}

		if isSpace() {
			// fold sequence ...
			space = true

			continue
		}
		// [re]write: [hold]char
		output()
	}

	// A trailing '-' or '/' was held as a possible comment start; the comment
	// never materialized, so the rune belongs to the output.
	if comment == 0 && hold > 0 {
		_, _ = w.WriteRune(hold)
	}

	if err != io.EOF {
		panic(err)
	}

	return w.String()
}
