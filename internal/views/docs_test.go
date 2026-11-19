package views_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	headingLine  = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
	slugDrop     = regexp.MustCompile("[^a-z0-9 _-]")
)

// slug is the anchor that GitHub makes for a heading.
func slug(heading string) string {
	return strings.ReplaceAll(slugDrop.ReplaceAllString(strings.ToLower(strings.ReplaceAll(heading, "`", "")), ""), " ", "-")
}

// linksOf gives the relative link targets of a markdown file.
func linksOf(t *testing.T, file string) []string {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, m := range markdownLink.FindAllStringSubmatch(string(data), -1) {
		if !strings.Contains(m[1], "://") {
			out = append(out, m[1])
		}
	}

	return out
}

func anchors(t *testing.T, file string) map[string]bool {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]bool{}
	for _, m := range headingLine.FindAllStringSubmatch(string(data), -1) {
		out[slug(m[1])] = true
	}

	return out
}

// The page "keep diagrams current" is linked from "render diagrams", and every link of the page resolves to a file
// and, when it has one, to a heading of that file.
func TestKeepDiagramsCurrentIsLinkedAndItsLinksResolve(t *testing.T) {
	t.Parallel()

	const page = "../../docs/how-to/keep-diagrams-current.md"

	linked := false

	for _, target := range linksOf(t, "../../docs/how-to/render-diagrams.md") {
		if target == "keep-diagrams-current.md" {
			linked = true
		}
	}

	if !linked {
		t.Error("docs/how-to/render-diagrams.md does not link to keep-diagrams-current.md")
	}

	for _, target := range linksOf(t, page) {
		file, anchor, _ := strings.Cut(target, "#")
		resolved := filepath.Join(filepath.Dir(page), file)

		if file == "" {
			resolved = page
		}

		if _, err := os.Stat(resolved); err != nil {
			t.Errorf("link %s: %v", target, err)

			continue
		}

		if anchor != "" && !anchors(t, resolved)[anchor] {
			t.Errorf("link %s: %s has no heading with the anchor #%s", target, resolved, anchor)
		}
	}
}
