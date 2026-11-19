package redact

import (
	"regexp"
	"strings"
)

var (
	dockerWord = regexp.MustCompile(`(?i)^(?:ENV|ARG)[ \t]+`)
	dockerPair = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*)=`)
)

// dockerfilePass redacts the values of ENV and ARG instructions with a sensitive name, and
// sensitive assignments in the other (shell) instructions.
func dockerfilePass(buf *buffer) {
	edits := make([]edit, 0, len(buf.lines))

	inEnv := false

	for i := range buf.lines {
		var found []edit

		found, inEnv = dockerLine(buf, i, inEnv)
		edits = append(edits, found...)
	}

	buf.apply(withoutOverlaps(edits))
}

// dockerLine finds the sensitive values of one line. inEnv says that the line continues an ENV or
// ARG instruction. It also tells whether the next line continues the instruction.
func dockerLine(buf *buffer, line int, inEnv bool) (edits []edit, continues bool) {
	text := buf.lines[line].text
	isEnv, from := inEnv, 0

	if !inEnv {
		isEnv, from = dockerInstruction(text)
	}

	if isEnv {
		edits = dockerPairs(text, from, line)
	} else {
		edits = shellLine(buf, line)
	}

	return edits, isEnv && strings.HasSuffix(strings.TrimRight(text, " \t"), `\`)
}

// dockerInstruction tells whether the text starts an ENV or ARG instruction, and where its
// arguments start.
func dockerInstruction(text string) (isEnv bool, from int) {
	trimmed := strings.TrimLeft(text, " \t")

	word := dockerWord.FindStringIndex(trimmed)
	if word == nil {
		return false, 0
	}

	return true, len(text) - len(trimmed) + word[1]
}

// skipBlanks gives the index of the first character from pos on that is not white space.
func skipBlanks(text string, pos int) int {
	for pos < len(text) && isBlank(text[pos]) {
		pos++
	}

	return pos
}

// dockerPairs finds the sensitive values of `KEY=value ...` and `KEY value` text from the offset.
func dockerPairs(text string, from, line int) []edit {
	edits := make([]edit, 0, 2)

	pos := from

	for {
		pos = skipBlanks(text, pos)
		if pos >= len(text) || text[pos] == '\\' {
			return edits
		}

		pair := dockerPair.FindStringSubmatchIndex(text[pos:])
		if pair == nil {
			found, ok := dockerLegacyEdit(text, pos, line)

			return appendEdit(edits, found, ok)
		}

		found, ok, end := dockerPairEdit(text, pos, line, pair)
		edits = appendEdit(edits, found, ok)
		pos = end
	}
}

// appendEdit adds the edit to the edits when there is one.
func appendEdit(edits []edit, e edit, ok bool) []edit {
	if ok {
		return append(edits, e)
	}

	return edits
}

// dockerPairEdit reads the `KEY=value` pair at pos, whose match is pair. It gives the edit of a
// sensitive value and the end of the pair.
func dockerPairEdit(text string, pos, line int, pair []int) (found edit, ok bool, end int) {
	name := text[pos+pair[2] : pos+pair[3]]
	start := pos + pair[1]
	end = tokenEnd(text, start, mode{})

	if !sensitiveName(name) || !isRedactable(unquote(text[start:end])) {
		return edit{}, false, end
	}

	return edit{line: line, start: start, end: end, class: ClassEnvValue, property: name}, true, end
}

// dockerLegacyEdit reads the legacy form `ENV KEY value with spaces` at pos: the first word is the
// name, the rest is the value.
func dockerLegacyEdit(text string, pos, line int) (found edit, ok bool) {
	nameEnd := pos
	for nameEnd < len(text) && !isBlank(text[nameEnd]) {
		nameEnd++
	}

	name := text[pos:nameEnd]
	start := skipBlanks(text, nameEnd)
	value := strings.TrimRight(text[start:], " \t\\")

	if !sensitiveName(name) || !isRedactable(value) {
		return edit{}, false
	}

	return edit{line: line, start: start, end: start + len(value), class: ClassEnvValue, property: name}, true
}
