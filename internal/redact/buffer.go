package redact

import (
	"slices"
	"strings"
)

// bufLine is one line of the text. A dropped line is part of a value that a marker on an
// earlier line replaced.
type bufLine struct {
	text    string
	dropped bool
}

// edit is a byte range of one line that a marker replaces.
type edit struct {
	property string
	class    string
	line     int
	start    int
	end      int
}

// buffer is the text as numbered lines. Passes replace byte ranges inside a line and drop whole
// lines. Line numbers are the numbers of the original text.
type buffer struct {
	file    string
	lines   []bufLine
	records []Record
	crlf    bool
}

func newBuffer(file, content string) *buffer {
	b := &buffer{file: file, crlf: strings.Contains(content, "\r\n")}
	if b.crlf {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}

	for line := range strings.SplitSeq(content, "\n") {
		b.lines = append(b.lines, bufLine{text: line})
	}

	return b
}

// text gives the text without the dropped lines.
func (b *buffer) text() string {
	kept := make([]string, 0, len(b.lines))

	for _, l := range b.lines {
		if !l.dropped {
			kept = append(kept, l.text)
		}
	}

	sep := "\n"
	if b.crlf {
		sep = "\r\n"
	}

	return strings.Join(kept, sep)
}

// numbered gives the lines that are not dropped, with their original numbers.
func (b *buffer) numbered() []Line {
	out := make([]Line, 0, len(b.lines))

	for i, l := range b.lines {
		if !l.dropped {
			out = append(out, Line{Number: i + 1, Text: l.text})
		}
	}

	return out
}

// apply replaces the ranges (several per line are fine, none may overlap) and records each
// replacement. Records of one batch are in line and column order.
func (b *buffer) apply(edits []edit) {
	slices.SortStableFunc(edits, func(x, y edit) int {
		if x.line != y.line {
			return x.line - y.line
		}

		return x.start - y.start
	})

	for _, e := range edits {
		b.records = append(b.records, Record{File: b.file, Line: e.line + 1, Property: e.property, Class: e.class})
	}

	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		text := b.lines[e.line].text
		b.lines[e.line].text = text[:e.start] + Marker(e.class) + text[e.end:]
	}
}

// drop removes the lines from..to (zero-based, to exclusive) from the output.
func (b *buffer) drop(from, to int) {
	for i := from; i < to && i < len(b.lines); i++ {
		b.lines[i].dropped = true
	}
}
