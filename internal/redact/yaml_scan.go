package redact

import (
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// position is a place in the text: a line and a byte offset in it.
type position struct {
	line int
	byte int
}

func indentOf(text string) int {
	return len(text) - len(strings.TrimLeft(text, " \t"))
}

// byteOffset converts a zero-based column in characters to a byte offset in the line.
func byteOffset(text string, column int) int {
	offset := 0

	for i := 0; i < column && offset < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}

	return offset
}

// scalarEnd finds the end of the single-line or quoted scalar that starts at the position from.
// A quoted scalar can span lines. ok is false when no end is found.
func scalarEnd(lines []bufLine, from position, style yaml.Style, flow bool) (position, bool) {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return quotedEnd(lines, from, '"')
	case style&yaml.SingleQuotedStyle != 0:
		return quotedEnd(lines, from, '\'')
	}

	text := lines[from.line].text
	end := plainEnd(text, from.byte, flow)

	return position{line: from.line, byte: len(strings.TrimRight(text[:end], " \t"))}, true
}

// plainEnd gives the offset where the plain scalar that starts at start ends: at a comment, or
// in a flow collection at the next separator or closing bracket.
func plainEnd(text string, start int, flow bool) int {
	for i := start; i < len(text); i++ {
		if endsPlain(text, i, start, flow) {
			return i
		}
	}

	return len(text)
}

// endsPlain says whether the plain scalar that starts at start ends at offset i.
func endsPlain(text string, i, start int, flow bool) bool {
	if isCommentStart(text, i, start) {
		return true
	}

	return flow && isFlowEnd(text[i])
}

// isCommentStart says whether a comment begins at offset i: a hash after a space or tab.
func isCommentStart(text string, i, start int) bool {
	return text[i] == '#' && i > start && isBlank(text[i-1])
}

// isFlowEnd says whether the character ends a plain scalar in a flow collection.
func isFlowEnd(c byte) bool { return c == ',' || c == '}' || c == ']' }

// quotedEnd gives the position after the closing quote of the quoted scalar that opens at from.
func quotedEnd(lines []bufLine, from position, quote byte) (position, bool) {
	col := from.byte + 1

	for cur := from.line; cur < len(lines); cur++ {
		if end, ok := closingQuote(lines[cur].text, col, quote); ok {
			return position{line: cur, byte: end}, true
		}

		col = 0
	}

	return position{}, false
}

// closingQuote gives the offset after the closing quote in the line, from offset i on. A double-quoted
// scalar escapes with a backslash, a single-quoted one doubles the quote.
func closingQuote(text string, i int, quote byte) (int, bool) {
	for i < len(text) {
		switch {
		case quote == '"' && text[i] == '\\':
			i += 2
		case isDoubledQuote(text, i, quote):
			i += 2
		case text[i] == quote:
			return i + 1, true
		default:
			i++
		}
	}

	return i, false
}

// isDoubledQuote says whether offset i holds two single quotes: an escaped quote.
func isDoubledQuote(text string, i int, quote byte) bool {
	return quote == '\'' && text[i] == quote && i+1 < len(text) && text[i+1] == quote
}
