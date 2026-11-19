package canonjson_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
)

func marshal(t *testing.T, v any) string {
	t.Helper()

	out, err := canonjson.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	return string(out)
}

func TestSortsKeysRecursivelyAndEndsWithNewline(t *testing.T) {
	t.Parallel()

	got := marshal(t, map[string]any{
		"b": 1,
		"a": map[string]any{"d": []any{map[string]any{"z": 1, "y": 2}}, "c": nil},
	})
	want := "{\n  \"a\": {\n    \"c\": null,\n    \"d\": [\n      {\n        \"y\": 2,\n        \"z\": 1\n      }\n    ]\n  },\n  \"b\": 1\n}\n"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSameOutputForAnyKeyOrder(t *testing.T) {
	t.Parallel()

	left := marshal(t, map[string]any{"x": 1, "y": map[string]any{"q": 1, "p": 2}})

	right, err := canonjson.Canonicalize([]byte(`{"y":{"p":2,"q":1},"x":1}`))
	if err != nil {
		t.Fatal(err)
	}

	if left != string(right) {
		t.Fatalf("%q != %q", left, right)
	}
}

func TestRemovesOmittedFieldsAndKeepsArrayOrder(t *testing.T) {
	t.Parallel()

	type item struct {
		A *int  `json:"a,omitempty"`
		B []int `json:"b"`
	}

	out := marshal(t, item{B: []int{3, 1, 2}})

	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatal(err)
	}

	if _, has := back["a"]; has {
		t.Fatalf("a not removed: %s", out)
	}

	if got := marshal(t, item{B: []int{3, 1, 2}}); got != "{\n  \"b\": [\n    3,\n    1,\n    2\n  ]\n}\n" {
		t.Fatalf("got %q", got)
	}
}

func keysOf(t *testing.T, text string) []string {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(text))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}

	var keys []string

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}

		keys = append(keys, tok.(string)) //nolint:errcheck,forcetypeassert // the decoder gives a string for an object key.

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}

	return keys
}

func TestSortsInUTF16CodeUnitOrder(t *testing.T) {
	t.Parallel()

	got := keysOf(t, marshal(t, map[string]any{"b": 1, "B": 1, "\u00e9": 1, "a": 1}))
	want := []string{"B", "a", "b", "\u00e9"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUTF16OrderDiffersFromByteOrderAboveBMP(t *testing.T) {
	t.Parallel()

	// U+FFFF is one code unit 0xFFFF; U+1F600 starts with the surrogate 0xD83D.
	// In UTF-16 order U+1F600 comes first; in Go byte order U+FFFF comes first.
	got := keysOf(t, marshal(t, map[string]any{"\uFFFF": 1, "\U0001F600": 1}))
	want := []string{"\U0001F600", "\uFFFF"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCompareUTF16(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		sign int
	}{
		{"a", "a", 0},
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "ab", -1},
		{"ab", "a", 1},
		{"", "a", -1},
		{"\U0001F600", "\uFFFF", -1},
		{"\uFFFF", "\U0001F600", 1},
		{"\U0001F600", "\U0001F601", -1},
		{"\U0001F601", "\U0001F600", 1},
		{"\U00010000", "\U0001F600", -1},
	}

	for _, c := range cases {
		got := canonjson.CompareUTF16(c.a, c.b)
		if (got < 0 && c.sign >= 0) || (got > 0 && c.sign <= 0) || (got == 0 && c.sign != 0) {
			t.Errorf("CompareUTF16(%q, %q) = %d, want sign %d", c.a, c.b, got, c.sign)
		}
	}
}

func TestStringEscapesLikeJavaScript(t *testing.T) {
	t.Parallel()

	got := marshal(t, "<a&b>\u2028\n\t\x01\"\\\u007f")
	want := "\"<a&b>\u2028\\n\\t\\u0001\\\"\\\\\u007f\"\n"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestKeepsNumbersAndEmptyContainers(t *testing.T) {
	t.Parallel()

	got := marshal(t, map[string]any{"n": 0.95, "big": int64(9007199254740993), "e": []int{}, "o": map[string]int{}})
	want := "{\n  \"big\": 9007199254740993,\n  \"e\": [],\n  \"n\": 0.95,\n  \"o\": {}\n}\n"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMarshalError(t *testing.T) {
	t.Parallel()

	if _, err := canonjson.Marshal(make(chan int)); err == nil {
		t.Fatal("expected an error")
	}

	if _, err := canonjson.Canonicalize([]byte("{")); err == nil {
		t.Fatal("expected an error")
	}
}
