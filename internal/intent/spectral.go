package intent

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// This file holds the JavaScript and Spectral semantics that the ported CALM
// CLI rules need (calmrules.go, patternrules.go, timeline.go). The CALM CLI runs
// its rules with @stoplight/spectral-core; a port must select the same values
// and report the same paths.

// stripRefs ports stripRefs of the CALM CLI (validation-helpers.ts): the JSON
// text of the document with each "$ref" changed to "ref", parsed again. The
// CALM CLI lints patterns and timelines in this form, so that Spectral does not
// resolve their references.
func stripRefs(doc any) any {
	data, err := json.Marshal(doc)
	if err != nil {
		return doc
	}

	var out any
	if err := json.Unmarshal(bytes.ReplaceAll(data, []byte("$ref"), []byte("ref")), &out); err != nil {
		return doc
	}

	return out
}

// match is a value of a document and its path (unescaped segments).
type match struct {
	value any
	path  []string
}

func (m match) at(segment string, value any) match {
	return match{path: append(append([]string{}, m.path...), segment), value: value}
}

// isObject is lodash isObject for JSON values: an object or an array.
func isObject(v any) bool {
	switch v.(type) {
	case obj, []any:
		return true
	}

	return false
}

// children gives the members of an object (sorted keys) or the items of an
// array: the JSONPath `[*]` or `.*` selection. Other values have no children.
func children(parent match) []match {
	switch typed := parent.value.(type) {
	case []any:
		out := make([]match, len(typed))
		for i, item := range typed {
			out[i] = parent.at(strconv.Itoa(i), item)
		}

		return out
	case obj:
		keys := sortedKeys(typed)
		out := make([]match, len(keys))

		for i, key := range keys {
			out[i] = parent.at(key, typed[key])
		}

		return out
	}

	return nil
}

// prop gives the member of an object with a key, as the JSONPath `.key` selection.
// ok is false when the value is not an object or has no such key.
func prop(parent match, key string) (match, bool) {
	members, isObj := parent.value.(obj)
	if !isObj {
		return match{}, false
	}

	member, ok := members[key]
	if !ok {
		return match{}, false
	}

	return parent.at(key, member), true
}

// props gives prop for each match that has the key.
func props(list []match, key string) []match {
	out := []match{}

	for _, m := range list {
		if p, ok := prop(m, key); ok {
			out = append(out, p)
		}
	}

	return out
}

// childrenOf gives the children of each match.
func childrenOf(list []match) []match {
	out := make([]match, 0, len(list))
	for _, m := range list {
		out = append(out, children(m)...)
	}

	return out
}

// selectPath follows a JSONPath of `.key` and `[*]` steps ("*" is `[*]`) from m.
func selectPath(m match, steps ...string) []match {
	list := []match{m}

	for _, step := range steps {
		if step == "*" {
			list = childrenOf(list)
		} else {
			list = props(list, step)
		}
	}

	return list
}

// descendants gives every value below m (not m itself): the JSONPath `..*` selection.
func descendants(m match) []match {
	kids := children(m)
	out := make([]match, 0, len(kids))

	for _, c := range kids {
		out = append(out, c)
		out = append(out, descendants(c)...)
	}

	return out
}

// lintTarget is getLintTargets of spectral-core for a `field`: the member of an
// object or array (undefined when missing), or the value itself when it is not
// an object.
func lintTarget(m match, field string) match {
	if !isObject(m.value) {
		return m
	}

	if p, ok := prop(m, field); ok {
		return p
	}

	return m.at(field, nil)
}

// truthy is JavaScript truthiness of a JSON value.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}

	return true
}

// jsString is JavaScript String() of a JSON value.
func jsString(v any) string {
	switch typed := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return jsNumberText(typed)
	case string:
		return typed
	case []any:
		parts := make([]string, len(typed))

		for i, item := range typed {
			if item != nil {
				parts[i] = jsString(item)
			}
		}

		return strings.Join(parts, ",")
	}

	return "[object Object]"
}

// jsNumberText is JavaScript String() of a number.
func jsNumberText(number float64) string {
	switch {
	case math.IsNaN(number):
		return "NaN"
	case math.IsInf(number, 1):
		return "Infinity"
	case math.IsInf(number, -1):
		return "-Infinity"
	case number == 0:
		return "0"
	}

	if abs := math.Abs(number); abs >= 1e21 || abs < 1e-6 {
		return jsExponentText(number)
	}

	return strconv.FormatFloat(number, 'f', -1, 64)
}

// jsExponentText writes a number in the exponent form of JavaScript: "1e+21", "1.5e-7".
func jsExponentText(number float64) string {
	text := strconv.FormatFloat(number, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(text, "e")
	exp = strings.TrimLeft(exp, "+")

	if strings.HasPrefix(exp, "-") {
		exp = "-" + strings.TrimLeft(exp[1:], "0")
	} else {
		exp = "+" + strings.TrimLeft(exp, "0")
	}

	return mantissa + "e" + exp
}

const jsSpaces = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

var jsDecimal = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// jsNumber is JavaScript Number() of a string for decimal text; other text is NaN
// (a hex, octal, or binary literal is never negative, so it never equals -1).
func jsNumber(text string) float64 {
	text = strings.Trim(text, jsSpaces)
	if text == "" {
		return 0
	}

	if !jsDecimal.MatchString(text) {
		return math.NaN()
	}

	// The text is a decimal literal; a value out of range is ±Inf, as in JavaScript.
	parsed, _ := strconv.ParseFloat(text, 64)

	return parsed
}

// looseEqualsMinusOne is the JavaScript `value == -1`.
func looseEqualsMinusOne(v any) bool {
	switch t := v.(type) {
	case float64:
		return t == -1
	case string:
		return jsNumber(t) == -1
	case []any:
		return jsNumber(jsString(t)) == -1
	}

	return false
}

// looseEqualsString is the JavaScript `value == text`.
func looseEqualsString(value any, text string) bool {
	switch typed := value.(type) {
	case string:
		return typed == text
	case float64:
		return typed == jsNumber(text)
	case bool:
		n := 0.0
		if typed {
			n = 1
		}

		return n == jsNumber(text)
	case nil:
		return false
	}

	return jsString(value) == text
}

// valueKey is the SameValueZero identity of a JSON value (Set, includes,
// lodash difference). ok is false for an object or array: such a value is
// equal to no other value.
func valueKey(v any) (string, bool) {
	switch typed := v.(type) {
	case nil:
		return "null", true
	case bool:
		return "b:" + strconv.FormatBool(typed), true
	case float64:
		if typed == 0 {
			typed = 0 // -0 and +0 are the same value
		}

		return "n:" + strconv.FormatFloat(typed, 'g', -1, 64), true
	case string:
		return "s:" + typed, true
	}

	return "", false
}

// valueSet is a JavaScript Set of JSON values.
type valueSet map[string]struct{}

func newValueSet(values []match) valueSet {
	s := valueSet{}
	for _, m := range values {
		s.add(m.value)
	}

	return s
}

func (s valueSet) has(v any) bool {
	k, ok := valueKey(v)
	if !ok {
		return false
	}

	_, found := s[k]

	return found
}

func (s valueSet) add(v any) {
	if k, ok := valueKey(v); ok {
		s[k] = struct{}{}
	}
}

// finding is one result of a ported CALM CLI rule. Path is the Spectral path
// (unescaped segments) relative to the linted document.
type finding struct {
	rule    string
	code    string
	message string
	params  diag.Params
	path    []string
}

// findings collects rule results like spectral-core: the path is cut to the
// closest path that exists in the document, and a result with the same rule,
// path, and message as an earlier one is dropped.
type findings struct {
	doc  any
	seen map[string]bool
	list []finding
}

func newFindings(doc any) *findings {
	return &findings{doc: doc, seen: map[string]bool{}}
}

// add records a result. code is diag.INT014 for an error rule and diag.INT015
// for a warning rule, unless a rule keeps an older Concordia code.
func (f *findings) add(rule, code string, path []string, message string) {
	f.record(&finding{rule: rule, code: code, message: message, path: path})
}

// record records a result with its own diagnostic parameters. Without them, the
// parameters are the message.
func (f *findings) record(result *finding) {
	result.path = closestPath(f.doc, result.path)

	key := result.rule + "\x00" + strings.Join(result.path, "\x00") + "\x00" + result.message
	if f.seen[key] {
		return
	}

	f.seen[key] = true

	if result.params == nil {
		result.params = diag.Detail(result.message)
	}

	f.list = append(f.list, *result)
}

// closestPath is getClosestJsonPath of spectral-runtime.
func closestPath(doc any, path []string) []string {
	out := []string{}
	piece := doc

	for _, segment := range path {
		next, found := stepInto(piece, segment)
		if !found {
			return out
		}

		piece = next

		out = append(out, segment)
	}

	return out
}

// stepInto gives the member of an object, or the item of an array, that the
// path segment names. found is false when the value has no such member or item.
func stepInto(piece any, segment string) (next any, found bool) {
	switch typed := piece.(type) {
	case obj:
		next, found = typed[segment]

		return next, found
	case []any:
		index, valid := arrayIndex(segment, len(typed))
		if !valid {
			return nil, false
		}

		return typed[index], true
	}

	return nil, false
}

// arrayIndex reads a canonical array index (no sign, no leading zero) below size.
func arrayIndex(segment string, size int) (index int, valid bool) {
	index, err := strconv.Atoi(segment)
	if err != nil || strconv.Itoa(index) != segment {
		return 0, false
	}

	return index, index >= 0 && index < size
}

// pointerOf gives the escaped JSON Pointer of a path.
func pointerOf(path []string) string {
	var b strings.Builder
	for _, s := range path {
		b.WriteString("/" + esc(s))
	}

	return b.String()
}

// diagnostics gives the findings as diagnostics with the source URI and a pointer prefix.
func (f *findings) diagnostics(uri, prefix string) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(f.list))

	for _, x := range f.list {
		out = append(out, diag.New(x.code, x.params,
			diag.WithSource(diag.Source{URI: uri, Pointer: prefix + pointerOf(x.path)}), diag.WithRule(x.rule)))
	}

	return out
}
