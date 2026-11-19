package redact

import (
	"regexp"
	"slices"
	"strings"
)

// shape is a known secret shape. A match of the group (0 is the whole match) is redacted.
type shape struct {
	re    *regexp.Regexp
	class string
	group int
}

var shapes = []shape{
	{re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`), class: ClassToken},
	{re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), class: ClassToken},
	{re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), class: ClassToken},
	{re: regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`), class: ClassToken},
	{re: regexp.MustCompile(`\bxox[abeprs]-[A-Za-z0-9-]{10,}`), class: ClassToken},
	{re: regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`), class: ClassToken},
	{re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), class: ClassToken},
	{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), class: ClassToken},
	{re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), class: ClassToken},
	{re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`), class: ClassToken},
	{re: regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}`), class: ClassToken},
	{re: regexp.MustCompile(`(?i)\bauthorization["']?\s*[:=]\s*["']?(?:bearer|basic|token)\s+([A-Za-z0-9._~+/=-]{8,})`), class: ClassToken, group: 1},
	{re: regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@"'<>]+:([^\s/@"'<>]+)@`), class: ClassCredential, group: 1},
}

var (
	pemBegin = regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-{5}`)
	pemEnd   = regexp.MustCompile(`-{5}END [A-Z0-9 ]*PRIVATE KEY[A-Z0-9 ]*-{5}`)
)

// patternPass redacts private key blocks and the known token shapes in every line.
func patternPass(buf *buffer) {
	pemPass(buf)

	var edits []edit

	for i := range buf.lines {
		if buf.lines[i].dropped {
			continue
		}

		text := buf.lines[i].text

		for _, s := range shapes {
			for _, m := range s.re.FindAllStringSubmatchIndex(text, -1) {
				start, end := m[2*s.group], m[2*s.group+1]
				if start < 0 || !isRedactable(text[start:end]) {
					continue
				}

				edits = append(edits, edit{line: i, start: start, end: end, class: s.class})
			}
		}
	}

	buf.apply(withoutOverlaps(edits))
}

// pemLineEdits gives an edit for each PEM private key block that begins and ends on line i, and the
// offset of a BEGIN marker whose block runs on past the line (-1 when there is none).
func pemLineEdits(i int, text string) (edits []edit, open int) {
	pos := 0

	for pos < len(text) {
		begin := pemBegin.FindStringIndex(text[pos:])
		if begin == nil {
			break
		}

		start, bodyStart := pos+begin[0], pos+begin[1]

		end := pemEnd.FindStringIndex(text[bodyStart:])
		if end == nil {
			return edits, start
		}

		pos = bodyStart + end[1]
		edits = append(edits, edit{line: i, start: start, end: pos, class: ClassPrivateKey})
	}

	return edits, -1
}

// pemPass redacts each PEM private key block, from its BEGIN line to its END line. A block that
// has no END line runs to the end of the text. The block becomes one marker on the BEGIN line.
func pemPass(buf *buffer) {
	var edits []edit

	for i := 0; i < len(buf.lines); i++ {
		if buf.lines[i].dropped {
			continue
		}

		text := buf.lines[i].text

		lineEdits, open := pemLineEdits(i, text)
		edits = append(edits, lineEdits...)

		if open < 0 {
			continue
		}

		last := len(buf.lines) - 1
		for last > i && strings.TrimSpace(buf.lines[last].text) == "" {
			last--
		}

		for j := i + 1; j < len(buf.lines); j++ {
			if pemEnd.MatchString(buf.lines[j].text) {
				last = j

				break
			}
		}

		edits = append(edits, edit{line: i, start: open, end: len(text), class: ClassPrivateKey})

		buf.apply(edits)
		buf.drop(i+1, last+1)

		edits = nil
		i = last
	}

	buf.apply(edits)
}

// stableSortRecords orders the records by line. Records of one line keep the order of their passes.
func stableSortRecords(records []Record) {
	slices.SortStableFunc(records, func(x, y Record) int { return x.Line - y.Line })
}
