package redact

import (
	"regexp"
	"strings"
)

var heredoc = regexp.MustCompile(`^<<-?[ \t]*["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

const bracketKind = "bracket"

// lineScope is a line of the buffer together with the end (exclusive) of the lines that a value
// which starts on it may span.
type lineScope struct {
	buf   *buffer
	line  int
	limit int
}

// text gives the text of the line.
func (s lineScope) text() string {
	return s.buf.lines[s.line].text
}

// isBlank tells whether the character is a space or a tab.
func isBlank(c byte) bool {
	return c == ' ' || c == '\t'
}

// isQuoteChar tells whether the character is a single or double quote.
func isQuoteChar(c byte) bool {
	return c == '"' || c == '\''
}

// valueAt reads the value that starts at the byte offset of the line. It gives the value without
// quotes, the end offset in the line, and how many following lines belong to it.
func valueAt(scope lineScope, start int, how mode) (value string, end, extra int) {
	text := scope.text()
	rest := text[start:]

	if h := heredoc.FindStringSubmatch(rest); h != nil {
		end, extra = heredocValue(scope, h[1])

		return "heredoc", end, extra
	}

	if rest != "" && strings.IndexByte("[{", rest[0]) >= 0 {
		return bracketValue(scope, start)
	}

	end = tokenEnd(text, start, how)

	return unquote(text[start:end]), end, 0
}

// heredocValue finds the line of the delimiter that closes the heredoc. It gives the end offset in
// the line of the heredoc start, and how many following lines belong to the heredoc.
func heredocValue(scope lineScope, delimiter string) (end, extra int) {
	end = len(scope.text())

	for i := scope.line + 1; i < scope.limit; i++ {
		if strings.TrimSpace(scope.buf.lines[i].text) == delimiter {
			return end, i - scope.line
		}
	}

	return end, scope.limit - scope.line - 1
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}

	quote := value[0]
	if isQuoteChar(quote) && value[len(value)-1] == quote {
		return value[1 : len(value)-1]
	}

	return value
}

// bracketScan is the state of the scan of a bracketed value: the nesting depth and the open quote
// (0 for none).
type bracketScan struct {
	depth int
	quote byte
}

// bracketValue consumes a balanced [..] or {..} value across lines. Quoted strings may hold brackets.
func bracketValue(scope lineScope, start int) (value string, end, extra int) {
	var scan bracketScan

	for i := scope.line; i < scope.limit; i++ {
		first := 0
		if i == scope.line {
			first = start
		}

		closeAt := scan.closeIndex(scope.buf.lines[i].text, first)
		if closeAt < 0 {
			continue
		}

		if i == scope.line {
			return bracketKind, closeAt + 1, 0
		}

		return bracketKind, len(scope.text()), i - scope.line
	}

	return bracketKind, len(scope.text()), scope.limit - scope.line - 1
}

// closeIndex gives the index of the character in the text from first on that closes the outermost
// bracket, or -1.
func (s *bracketScan) closeIndex(text string, first int) int {
	for j := first; j < len(text); j++ {
		var closed bool

		j, closed = s.step(text, j)
		if closed {
			return j
		}
	}

	return -1
}

// step reads the character at j. It gives the index of the last character that it used, and
// whether that character closed the outermost bracket.
func (s *bracketScan) step(text string, j int) (last int, closed bool) {
	char := text[j]

	if s.quote != 0 {
		return s.stepQuoted(char, j), false
	}

	switch char {
	case '"', '\'':
		s.quote = char
	case '[', '{':
		s.depth++
	case ']', '}':
		s.depth--

		return j, s.depth == 0
	}

	return j, false
}

// stepQuoted reads the character at j inside a quoted string. A backslash takes the next character.
func (s *bracketScan) stepQuoted(char byte, j int) int {
	switch char {
	case '\\':
		return j + 1
	case s.quote:
		s.quote = 0
	}

	return j
}

// tokenEnd gives the end of the value token that starts at start: a quoted string, or a bare word.
func tokenEnd(text string, start int, how mode) int {
	if start >= len(text) {
		return len(text)
	}

	switch text[start] {
	case '"':
		return doubleQuotedEnd(text, start)
	case '\'':
		return singleQuotedEnd(text, start)
	}

	if how.toEOL {
		return commentStart(text, start)
	}

	return bareEnd(text, start, how.stops)
}

// bareEnd gives the end of the bare word that starts at start: the first white space or stop character.
func bareEnd(text string, start int, stops string) int {
	for i := start; i < len(text); i++ {
		if isBlank(text[i]) || strings.IndexByte(stops, text[i]) >= 0 {
			return i
		}
	}

	return len(text)
}

// singleQuotedEnd gives the index after the closing quote of the string that opens at start, or the end of the text.
func singleQuotedEnd(text string, start int) int {
	i := strings.IndexByte(text[start+1:], '\'')
	if i < 0 {
		return len(text)
	}

	return start + 1 + i + 1
}

// doubleQuotedEnd gives the index after the closing quote of the string that opens at start, or the end of the text.
func doubleQuotedEnd(text string, start int) int {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}

	return len(text)
}

// commentStart gives the end of a bare YAML value: the start of a ` #` comment, or the end of the line, without trailing white space.
func commentStart(text string, start int) int {
	for i := start + 1; i < len(text); i++ {
		if text[i] == '#' && isBlank(text[i-1]) {
			return len(strings.TrimRight(text[:i], " \t"))
		}
	}

	return len(strings.TrimRight(text, " \t"))
}
