package redact

import (
	"regexp"
	"slices"
	"strings"
)

// mode says how a line pass finds assignments and where a bare value ends.
type mode struct {
	// sep matches the key and the separator up to the value. Group 1 is the key.
	sep *regexp.Regexp
	// class is the class of the redacted values.
	class string
	// stops end a bare value, with white space.
	stops string
	// toEOL makes a bare value run up to the end of the line or a comment (YAML).
	toEOL bool
	// skipDash ignores a value that starts with "-": it is the next flag.
	skipDash bool
}

var (
	// Terraform variables: `db_password = "x"`, also inline in `{ password = "x" }`.
	variableMode = mode{sep: regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?[ \t]*=[ \t]*`), class: ClassSecretData, stops: ",}]"}
	// YAML or JSON text that does not parse as a document.
	yamlLineMode = mode{sep: regexp.MustCompile(`(?:^|[\s{,\-])["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?[ \t]*:[ \t]*`), class: ClassSecretData, toEOL: true}
	// Shell: `KEY=value`, `export KEY=value`, `docker run -e KEY=value`, `--password=value`.
	shellAssignMode = mode{sep: regexp.MustCompile(`(?:^|[^A-Za-z0-9_])([A-Za-z_][A-Za-z0-9_]*)=`), class: ClassEnvValue, stops: ";&|)`"}
	// Shell flags with a separate value: `--password value`.
	shellFlagMode = mode{sep: regexp.MustCompile(`(?:^|[ \t])--?([A-Za-z][A-Za-z0-9_-]*)[ \t]+`), class: ClassSecretData, stops: ";&|)`", skipDash: true}
)

// keyValuePass redacts the values of sensitive keys in the lines from..to (to exclusive).
// equalsOnly is for Terraform variable files (separator `=`), else the separator is `:`.
func keyValuePass(buf *buffer, from, stop int, equalsOnly bool) {
	how := yamlLineMode
	if equalsOnly {
		how = variableMode
	}

	var (
		edits []edit
		drops [][2]int
	)

	for i := from; i < stop; i++ {
		if buf.lines[i].dropped {
			continue
		}

		found, extra := scanLine(lineScope{buf: buf, line: i, limit: stop}, how)
		edits = append(edits, found...)

		if extra > 0 {
			drops = append(drops, [2]int{i + 1, i + 1 + extra})
			i += extra
		}
	}

	buf.apply(edits)

	for _, d := range drops {
		buf.drop(d[0], d[1])
	}
}

// skips tells whether the assignment of the key, whose value starts with rest, is not redacted.
func (m mode) skips(key, rest string) bool {
	if !sensitiveName(key) {
		return true
	}

	return m.skipDash && strings.HasPrefix(rest, "-")
}

// scanLine finds the sensitive assignments of one line. extra is the number of following lines
// that a value which spans lines (heredoc, list, object) takes; the scan of the line stops there.
func scanLine(scope lineScope, how mode) (edits []edit, extra int) {
	text := scope.text()
	pos := 0

	for pos < len(text) {
		loc := how.sep.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}

		key := text[pos+loc[2] : pos+loc[3]]
		start := pos + loc[1]
		pos = start

		if how.skips(key, text[start:]) {
			continue
		}

		value, end, more := valueAt(scope, start, how)
		if !isRedactable(value) {
			pos = max(end, start)

			continue
		}

		edits = append(edits, edit{line: scope.line, start: start, end: end, class: how.class, property: key})

		if more > 0 {
			return edits, more
		}

		pos = end
	}

	return edits, 0
}

// shellPass redacts sensitive assignments and flags in shell text.
func shellPass(buf *buffer) {
	edits := make([]edit, 0, len(buf.lines))

	for i := range buf.lines {
		edits = append(edits, shellLine(buf, i)...)
	}

	buf.apply(edits)
}

func shellLine(buf *buffer, line int) []edit {
	var edits []edit

	for _, how := range []mode{shellAssignMode, shellFlagMode} {
		found, _ := scanLine(lineScope{buf: buf, line: line, limit: line + 1}, how)
		edits = append(edits, found...)
	}

	return withoutOverlaps(edits)
}

// withoutOverlaps sorts the edits by position and keeps the first of those that overlap.
func withoutOverlaps(edits []edit) []edit {
	slices.SortStableFunc(edits, func(x, y edit) int {
		if x.line != y.line {
			return x.line - y.line
		}

		return x.start - y.start
	})

	out := edits[:0]

	for _, e := range edits {
		if overlapsLast(out, e) {
			continue
		}

		out = append(out, e)
	}

	return out
}

// overlapsLast tells whether the edit starts inside the last of the edits on the same line.
func overlapsLast(edits []edit, e edit) bool {
	if len(edits) == 0 {
		return false
	}

	last := edits[len(edits)-1]

	return last.line == e.line && e.start < last.end
}
