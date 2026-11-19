// Package canonjson writes stable JSON text: object keys sorted in UTF-16 code
// unit order, array order kept, 2-space indent, final newline.
package canonjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

const (
	indentUnit = "  "
	hexDigits  = "0123456789abcdef"
	// surrogateBase is the first UTF-16 high surrogate; a code point above the
	// BMP starts with a code unit in 0xD800..0xDBFF.
	surrogateBase = 0xD800
	// bmpLimit is the first code point that needs a surrogate pair.
	bmpLimit = 0x10000
	// surrogateShift is the shift that gives the high surrogate offset.
	surrogateShift = 10
	// lowControl is the first printable ASCII character.
	lowControl = 0x20
)

// Marshal gives the canonical JSON text of v. The value is first encoded with
// encoding/json, so struct tags and json.Marshaler apply. A field that
// encoding/json leaves out (omitempty) is not in the result.
func Marshal(v any) ([]byte, error) {
	var raw bytes.Buffer

	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("canonjson: encode: %w", err)
	}

	return Canonicalize(raw.Bytes())
}

// Canonicalize gives the canonical form of JSON text.
func Canonicalize(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("canonjson: decode: %w", err)
	}

	var out bytes.Buffer

	write(&out, tree, "")
	out.WriteByte('\n')

	return out.Bytes(), nil
}

// MustString gives the canonical JSON text of v as a string. It panics when v
// cannot be encoded.
func MustString(v any) string {
	b, err := Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(b)
}

func write(out *bytes.Buffer, v any, indent string) {
	switch val := v.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(val))
	case json.Number:
		out.WriteString(val.String())
	case string:
		writeString(out, val)
	case []any:
		writeArray(out, val, indent)
	case map[string]any:
		writeObject(out, val, indent)
	}
}

func writeArray(out *bytes.Buffer, list []any, indent string) {
	if len(list) == 0 {
		out.WriteString("[]")

		return
	}

	inner := indent + indentUnit

	out.WriteString("[\n")

	for i, item := range list {
		if i > 0 {
			out.WriteString(",\n")
		}

		out.WriteString(inner)
		write(out, item, inner)
	}

	out.WriteString("\n" + indent + "]")
}

func writeObject(out *bytes.Buffer, obj map[string]any, indent string) {
	if len(obj) == 0 {
		out.WriteString("{}")

		return
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, CompareUTF16)

	inner := indent + indentUnit

	out.WriteString("{\n")

	for i, k := range keys {
		if i > 0 {
			out.WriteString(",\n")
		}

		out.WriteString(inner)
		writeString(out, k)
		out.WriteString(": ")
		write(out, obj[k], inner)
	}

	out.WriteString("\n" + indent + "}")
}

// writeString writes a JSON string as JavaScript JSON.stringify does: only the
// quote, the backslash and the control characters are escaped.
func writeString(out *bytes.Buffer, s string) {
	out.WriteByte('"')

	for _, char := range s {
		switch {
		case char == '"':
			out.WriteString(`\"`)
		case char == '\\':
			out.WriteString(`\\`)
		case char == '\b':
			out.WriteString(`\b`)
		case char == '\f':
			out.WriteString(`\f`)
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case char < lowControl:
			out.WriteString(`\u00`)
			out.WriteByte(hexDigits[char>>4])
			out.WriteByte(hexDigits[char&0xF])
		default:
			out.WriteRune(char)
		}
	}

	out.WriteByte('"')
}

// CompareUTF16 compares two strings in UTF-16 code unit order, as the
// JavaScript operator "<" does. The result is negative, zero or positive.
// Go compares bytes, which is the code point order: it differs from the UTF-16
// order for characters above U+FFFF against U+E000..U+FFFF.
func CompareUTF16(left, right string) int {
	for left != "" && right != "" {
		leftRune, leftSize := utf8.DecodeRuneInString(left)
		rightRune, rightSize := utf8.DecodeRuneInString(right)

		if leftRune != rightRune {
			if diff := firstUnit(leftRune) - firstUnit(rightRune); diff != 0 {
				return diff
			}

			return int(leftRune - rightRune)
		}

		left, right = left[leftSize:], right[rightSize:]
	}

	return len(left) - len(right)
}

// firstUnit gives the first UTF-16 code unit of a code point. When both code
// points are above the BMP and their first units are equal, the order of the
// code points still gives the order of the second units.
func firstUnit(r rune) int {
	if r >= bmpLimit {
		return surrogateBase + int((r-bmpLimit)>>surrogateShift)
	}

	return int(r)
}
