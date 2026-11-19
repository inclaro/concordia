package policy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

// docsWithPolicyExamples are the documents whose YAML examples are policy packs or rules.
// `concordia policy verify` checks each pack with policy.DescribePack; the test runs the same function on
// every example, so a policy example that the documentation shows cannot drift from the schema and the compiler.
var docsWithPolicyExamples = []string{
	"docs/reference/policies.md",
	"docs/reference/infrastructure-rules.md",
	"docs/how-to/write-policies.md",
	"docs/explanation/verdicts-and-coverage.md",
}

var yamlFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

const docsPackHeader = "apiVersion: concordia.io/v1alpha1\nkind: PolicyPack\nmetadata: {id: docs-example, version: 1.0.0}\n"

// policyExample gives the pack text that verifies an example, or "" for an example that is not a policy
// (a workspace snippet or a fragment of a rule block). Any other shape is an error: a new kind of example must be classified here.
func policyExample(t *testing.T, text string) string {
	t.Helper()

	switch trimmed := strings.TrimSpace(text); {
	case strings.Contains(text, "kind: PolicyPack"):
		return text
	case strings.HasPrefix(trimmed, "selectors:"):
		return docsPackHeader + text
	case strings.HasPrefix(trimmed, "- id:"):
		return docsPackHeader + "rules:\n" + text
	case strings.HasPrefix(trimmed, "policies:"), strings.HasPrefix(trimmed, "evidence:"), strings.HasPrefix(trimmed, "check:"):
		return ""
	default:
		t.Fatalf("unclassified YAML example:\n%s", text)

		return ""
	}
}

func TestEveryPolicyExampleInTheDocsVerifies(t *testing.T) {
	t.Parallel()

	verified, typed := 0, 0

	for _, doc := range docsWithPolicyExamples {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(doc)))
		if err != nil {
			t.Fatal(err)
		}

		for i, match := range yamlFence.FindAllStringSubmatch(string(data), -1) {
			text := policyExample(t, match[1])
			if text == "" {
				continue
			}

			info, problems := policy.DescribePack(policy.PolicyFile{Path: doc, Text: text})
			if len(problems) > 0 {
				t.Errorf("%s example %d does not verify: %+v\n%s", doc, i+1, problems, match[1])

				continue
			}

			verified++

			for j := range info.Rules {
				if info.Rules[j].Obligation != nil {
					typed++
				}
			}
		}
	}

	// The documentation shows typed rules, and each of them verified.
	if verified < 8 || typed < 5 {
		t.Fatalf("verified %d examples with %d typed rules: the docs lost their policy examples", verified, typed)
	}
}
