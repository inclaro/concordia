package redact

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitize makes text from repository content, provider output, or node names safe to print to a
// terminal, or to write to SARIF or JUnit. It replaces each ANSI escape character and other
// control character, DEL, C1 control, invalid UTF-8 byte, and invisible or bidirectional
// formatting character with a visible escape such as `\x1b` or `\u202e`. Newline and tab stay.
// Text without such characters is returned unchanged.
func Sanitize(text string) string { return sanitize(text, false) }

// SanitizeLine is Sanitize for text that must stay on one line (a node name, a message in a
// list): a newline is escaped too, as `\n`. Tab stays.
func SanitizeLine(text string) string { return sanitize(text, true) }

func sanitize(text string, oneLine bool) string {
	if !needsEscape(text, oneLine) {
		return text
	}

	var out strings.Builder

	for i := 0; i < len(text); {
		char, size := utf8.DecodeRuneInString(text[i:])

		switch {
		case char == utf8.RuneError && size <= 1:
			fmt.Fprintf(&out, `\x%02x`, text[i])
		case char == '\n' && oneLine:
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case isUnsafe(char):
			out.WriteString(escapeRune(char))
		default:
			out.WriteRune(char)
		}

		i += size
	}

	return out.String()
}

func needsEscape(text string, oneLine bool) bool {
	if !utf8.ValidString(text) {
		return true
	}

	for _, r := range text {
		if isUnsafe(r) || oneLine && r == '\n' {
			return true
		}
	}

	return false
}

// isUnsafe says whether the rune is a control or invisible formatting character. Newline and
// tab are not decided here.
func isUnsafe(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < ' ' || r == 0x7f || (r >= 0x80 && r <= 0x9f):
		return true
	case r == 0x2028 || r == 0x2029 || r == 0xfeff, r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069:
		return true
	}

	return unicode.Is(unicode.Cc, r)
}

// latin1Max is the first rune that needs four hex digits.
const latin1Max = 0x100

func escapeRune(r rune) string {
	if r < latin1Max {
		return fmt.Sprintf(`\x%02x`, r)
	}

	return fmt.Sprintf(`\u%04x`, r)
}
