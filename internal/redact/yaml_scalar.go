package redact

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// redaction says how a scalar is replaced: the name and class that the record carries, and
// whether the scalar is in flow style.
type redaction struct {
	property string
	class    string
	flow     bool
}

// isBlockStyle says whether a scalar is a literal or folded block.
func isBlockStyle(style yaml.Style) bool { return style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 }

// isQuotedStyle says whether a scalar is in single or double quotes.
func isQuotedStyle(style yaml.Style) bool {
	return style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
}

// isRedactableNode says whether val is a scalar that holds a value to redact: not null, not
// a boolean, and not a value that is already a marker.
func isRedactableNode(val *yaml.Node) bool {
	if !isScalar(val) || val.Tag == nullTag || val.Tag == boolTag {
		return false
	}

	return isRedactable(val.Value)
}

func (p *yamlWalk) redactScalar(val, key *yaml.Node, class string, flow bool) {
	p.redactScalarAs(val, key, redaction{property: key.Value, class: class, flow: flow})
}

// redactScalarAs replaces the text of a scalar value with a marker. key is the node of the key (or
// the dash of the item) that decides the indentation of a value that spans lines.
func (p *yamlWalk) redactScalarAs(val, key *yaml.Node, repl redaction) {
	if !isRedactableNode(val) {
		return
	}

	if _, seen := p.done[val]; seen {
		return
	}

	p.done[val] = struct{}{}

	p.replaceSpan(val, key.Column-1, repl)
}

// redactAfterEquals redacts the value of `KEY=value` when KEY is sensitive.
func (p *yamlWalk) redactAfterEquals(item *yaml.Node, flow bool) {
	name, value, found := strings.Cut(item.Value, "=")
	if !found || isBlockStyle(item.Style) {
		return
	}

	property := strings.TrimLeft(name, "-")
	if !sensitiveName(property) || !isRedactable(value) {
		return
	}

	if e, ok := p.assignmentEdit(item, property, flow); ok {
		p.edits = append(p.edits, e)
	}
}

// assignmentEdit gives the edit that replaces the value of the `KEY=value` item in its line. ok is
// false when the item does not sit in a single line.
func (p *yamlWalk) assignmentEdit(item *yaml.Node, property string, flow bool) (edit, bool) {
	line := p.offset + item.Line - 1
	text := p.buf.lines[line].text
	startByte := byteOffset(text, item.Column-1)

	if startByte >= len(text) {
		return edit{}, false
	}

	end, ok := scalarEnd(p.buf.lines, position{line: line, byte: startByte}, item.Style, flow)
	if !ok || end.line != line {
		return edit{}, false
	}

	equals := strings.Index(text[startByte:end.byte], "=")
	if equals < 0 {
		return edit{}, false
	}

	valueEnd := end.byte

	if isQuotedStyle(item.Style) {
		valueEnd--
	}

	return edit{line: line, start: startByte + equals + 1, end: valueEnd, class: ClassEnvValue, property: property}, true
}

// replaceSpan queues the replacement of the scalar val by a marker, and the drop of the lines
// that the scalar spans after its first line. base is the indentation of the key that holds it.
func (p *yamlWalk) replaceSpan(val *yaml.Node, base int, repl redaction) {
	line := p.offset + val.Line - 1
	if line < 0 || line >= len(p.buf.lines) {
		return
	}

	start := byteOffset(p.buf.lines[line].text, val.Column-1)
	if start >= len(p.buf.lines[line].text) {
		return
	}

	from := position{line: line, byte: start}
	end, last := p.extent(val, from, base, repl.flow)

	p.edits = append(p.edits, edit{line: line, start: start, end: end, class: repl.class, property: repl.property})

	if last > line {
		p.drops = append(p.drops, [2]int{line + 1, last + 1})
	}
}

// extent gives the end offset in the first line and the last line of the scalar that starts at
// the position from. The marker replaces the scalar up to the end offset, and the lines
// after the first one up to the last line go.
func (p *yamlWalk) extent(val *yaml.Node, from position, base int, flow bool) (end, last int) {
	text := p.buf.lines[from.line].text

	if isBlockStyle(val.Style) {
		return len(text), p.continuation(from.line, base, true)
	}

	pos, ok := scalarEnd(p.buf.lines, from, val.Style, flow)

	switch {
	case !ok:
		return len(text), from.line
	case pos.line > from.line:
		return len(text), pos.line
	case flow || isQuotedStyle(val.Style):
		return pos.byte, from.line
	}

	return pos.byte, p.continuation(from.line, base, false)
}

// continuation gives the last line of the lines below line that belong to the scalar: they are
// indented deeper than base. A block scalar also takes the blank lines inside, a plain scalar
// stops at a blank or comment line.
func (p *yamlWalk) continuation(line, base int, block bool) int {
	last := line

	for i := line + 1; i < len(p.buf.lines); i++ {
		next := p.buf.lines[i].text

		if block && strings.TrimSpace(next) == "" {
			continue
		}

		if endsContinuation(next, base, block) {
			return last
		}

		last = i
	}

	return last
}

// endsContinuation says whether the line is not part of the scalar above it: it is blank, a comment
// of a plain scalar, or not indented deeper than base.
func endsContinuation(text string, base int, block bool) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || indentOf(text) <= base {
		return true
	}

	return !block && strings.HasPrefix(trimmed, "#")
}
