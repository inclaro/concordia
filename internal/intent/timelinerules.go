package intent

import (
	"fmt"
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
)

// Rule IDs of the CALM CLI timeline ruleset
// (shared/src/spectral/rules-timeline.ts).
const (
	ruleTimelineNoEmpty        = "timeline-has-no-empty-properties"
	ruleCurrentMomentExists    = "current-moment-must-reference-moment-in-timeline"
	ruleTimelineUniqueIDs      = "unique-ids-must-be-unique-in-timeline"
	ruleValidFromAfterCurrent  = "moments-after-current-moment-may-not-have-valid-from"
	ruleCurrentMomentRequired  = "current-moment-required-when-moments-non-empty"
	ruleTimelineMomentsNonZero = "timeline-moments-must-be-non-empty"
	ruleMomentsSorted          = "moments-must-be-sorted-by-valid-from"
)

// timelineRules runs the ported CALM CLI timeline ruleset on a timeline. Like
// the CALM CLI, it lints the document in the form of stripRefs. The function
// current-moment-must-be-last-when-no-valid-from exists upstream but is not in
// the ruleset, so it is not ported.
func timelineRules(timeline any) *findings {
	doc := stripRefs(timeline)
	results := newFindings(doc)
	root := match{path: []string{}, value: doc}

	timelineNoEmptyProperties(results, root)
	timelineCurrentMomentExists(results, root)
	timelineUniqueIDs(results, root)
	timelineValidFromNotAfterCurrent(results, root)
	timelineCurrentMomentRequired(results, root)
	timelineMomentsNonEmpty(results, root)
	timelineMomentsSorted(results, root)

	return results
}

// timelineNoEmptyProperties ports timeline-has-no-empty-properties: `$..*` with
// spectral `truthy`.
func timelineNoEmptyProperties(results *findings, root match) {
	for _, m := range descendants(root) {
		if !truthy(m.value) {
			results.add(ruleTimelineNoEmpty, diag.INT014, m.path, "All properties must be set to a nonempty, nonzero value.")
		}
	}
}

// timelineCurrent gives the `$.current-moment` selection, when the input is truthy.
func timelineCurrent(root match) (match, bool) {
	current, ok := prop(root, "current-moment")
	if !ok || !truthy(current.value) {
		return match{}, false
	}

	return current, true
}

// timelineMoments is `$.moments`: the value when it is an array, else nil.
func timelineMoments(root match) []any {
	t, ok := prop(root, "moments")
	if !ok {
		return nil
	}

	list, _ := t.value.([]any)

	return list
}

// timelineCurrentMomentExists ports moment-id-exists.
func timelineCurrentMomentExists(f *findings, root match) {
	current, ok := timelineCurrent(root)
	if !ok {
		return
	}

	names := newValueSet(timelineMomentIDs(root))
	if !names.has(current.value) {
		f.add(ruleCurrentMomentExists, diag.INT014, current.path, fmt.Sprintf("'%s' does not refer to the unique-id of an existing moment.", jsString(current.value)))
	}
}

// timelineMomentIDs is `$.moments[*].unique-id`.
func timelineMomentIDs(root match) []match {
	return selectPath(root, "moments", "*", "unique-id")
}

// timelineUniqueIDs ports ids-are-unique.
func timelineUniqueIDs(f *findings, root match) {
	if !truthy(root.value) {
		return
	}

	seen := valueSet{}

	for _, m := range timelineMomentIDs(root) {
		if seen.has(m.value) {
			f.add(ruleTimelineUniqueIDs, diag.INT014, m.path, fmt.Sprintf("Duplicate unique-id detected. ID: %s, path: %s", jsString(m.value), pointerOf(m.path)))

			continue
		}

		seen.add(m.value)
	}
}

// timelineMoment is the unique-id and valid-from members of a moment.
type timelineMoment struct {
	id        any
	validFrom any
	hasID     bool
}

func timelineMomentOf(value any) timelineMoment {
	fields, isObj := value.(obj)
	if !isObj {
		return timelineMoment{}
	}

	id, hasID := fields["unique-id"]

	return timelineMoment{id: id, validFrom: fields["valid-from"], hasID: hasID}
}

// isCurrent tells whether the moment has the unique-id of the current moment.
func (m timelineMoment) isCurrent(current any) bool {
	return truthy(m.id) && timelineStrictEquals(m.id, current)
}

// idText is the unique-id of the moment as the CALM CLI message shows it.
func (m timelineMoment) idText() string {
	if !m.hasID {
		return "undefined"
	}

	return jsString(m.id)
}

// timelineValidFromNotAfterCurrent ports valid-from-not-after-current-moment.
func timelineValidFromNotAfterCurrent(results *findings, root match) {
	current, ok := timelineCurrent(root)
	if !ok {
		return
	}

	checking := false

	for _, item := range selectPath(root, "moments", "*") {
		moment := timelineMomentOf(item.value)

		if moment.isCurrent(current.value) {
			checking = true

			continue
		}

		if checking && truthy(moment.validFrom) {
			results.add(ruleValidFromAfterCurrent, diag.INT015, current.path, fmt.Sprintf("Moment with unique-id %s is after current-moment %s but has a valid-from.",
				doubleQuoted(moment.idText()), doubleQuoted(jsString(current.value))))
		}
	}
}

// timelineStrictEquals is JavaScript `===` for JSON values.
func timelineStrictEquals(a, b any) bool {
	ka, okA := valueKey(a)
	kb, okB := valueKey(b)

	return okA && okB && ka == kb
}

// timelineCurrentMomentRequired ports current-moment-required-when-moments-non-empty.
func timelineCurrentMomentRequired(f *findings, root match) {
	if !truthy(root.value) || len(timelineMoments(root)) == 0 {
		return
	}

	if current, _ := prop(root, "current-moment"); !truthy(current.value) {
		f.add(ruleCurrentMomentRequired, diag.INT015, []string{"current-moment"}, "Timeline has moments but no current-moment is set.")
	}
}

// timelineMomentsNonEmpty ports moments-must-be-non-empty.
func timelineMomentsNonEmpty(f *findings, root match) {
	if !truthy(root.value) {
		return
	}

	if len(timelineMoments(root)) == 0 {
		f.add(ruleTimelineMomentsNonZero, diag.INT015, []string{"moments"}, "Timeline must define at least one moment.")
	}
}

// timelineStamp is a valid-from of a moment: its text and its time in milliseconds.
type timelineStamp struct {
	text   string
	millis float64
}

// timelineStampOf parses the valid-from of a moment. ok is false when the
// valid-from is falsy or is not a date.
func timelineStampOf(validFrom any) (timelineStamp, bool) {
	if !truthy(validFrom) {
		return timelineStamp{}, false
	}

	text := jsString(validFrom)

	millis, ok := timelineParseDate(text)
	if !ok {
		return timelineStamp{}, false
	}

	return timelineStamp{text: text, millis: millis}, true
}

// timelineUnsortedMessage is the message of a moment whose valid-from is before
// the previous valid-from.
func timelineUnsortedMessage(index int, fields obj, stamp, previous timelineStamp) string {
	label := fmt.Sprintf("index %d", index)
	if momentID := fields["unique-id"]; truthy(momentID) {
		label = "unique-id " + doubleQuoted(jsString(momentID))
	}

	return fmt.Sprintf("Moment with %s has valid-from %s which is before the previous valid-from %s.",
		label, doubleQuoted(stamp.text), doubleQuoted(previous.text))
}

// timelineMomentsSorted ports moments-sorted-by-valid-from.
func timelineMomentsSorted(results *findings, root match) {
	if !truthy(root.value) {
		return
	}

	moments := timelineMoments(root)
	if len(moments) < 2 {
		return
	}

	var previous *timelineStamp

	for i, item := range moments {
		fields, _ := item.(obj)

		stamp, ok := timelineStampOf(fields["valid-from"])
		if !ok {
			continue
		}

		if previous != nil && stamp.millis < previous.millis {
			results.add(ruleMomentsSorted, diag.INT014, []string{"moments", strconv.Itoa(i), "valid-from"}, timelineUnsortedMessage(i, fields, stamp, *previous))
		}

		previous = &stamp
	}
}

// doubleQuoted wraps text in double quotes without escaping, as the CALM CLI
// messages do with "${value}".
func doubleQuoted(text string) string {
	return "\"" + text + "\""
}
