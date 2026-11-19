package assessment_test

import (
	"regexp"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/policy"
	ws "github.com/inclaro/concordia/internal/workspace"
)

var digestShape = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func TestAuthorityDigestIsStableAndSensitive(t *testing.T) {
	t.Parallel()

	a := ws.Authority{ID: "a", Role: "reviewer", Mechanism: "protected-review", Reviewers: []string{"alice"}}
	b := ws.Authority{ID: "b", Role: "reviewer", Mechanism: "signed"}

	first := assessment.AuthorityDigest([]ws.Authority{a, b})
	if !digestShape.MatchString(first) || first != assessment.AuthorityDigest([]ws.Authority{b, a}) {
		t.Errorf("digest = %q, want a digest that does not depend on the order", first)
	}

	changed := a
	changed.Reviewers = []string{"alice", "mallory"}

	if first == assessment.AuthorityDigest([]ws.Authority{changed, b}) {
		t.Error("adding a reviewer did not change the digest")
	}

	if empty := assessment.AuthorityDigest(nil); !digestShape.MatchString(empty) {
		t.Errorf("empty digest = %q", empty)
	}
}

func TestPolicyDigestCoversOnlyTheReviewRules(t *testing.T) {
	t.Parallel()

	review := policy.PolicyFile{Path: "builtin:review-currency", Text: "rules:\n  - id: REV-APR-001\n    assessmentCurrency: approval\n"}
	other := policy.PolicyFile{Path: "policies/a.policy.yaml", Text: "rules:\n  - id: X\n"}

	base := assessment.PolicyDigest([]policy.PolicyFile{review, other})
	if !digestShape.MatchString(base) {
		t.Fatalf("digest = %q", base)
	}

	other.Text += "# edited\n"

	if got := assessment.PolicyDigest([]policy.PolicyFile{review, other}); got != base {
		t.Error("a policy file without review rules changed the digest")
	}

	review.Text += "# edited\n"

	if got := assessment.PolicyDigest([]policy.PolicyFile{review, other}); got == base {
		t.Error("a change of the review rules did not change the digest")
	}

	if none := assessment.PolicyDigest(nil); !digestShape.MatchString(none) {
		t.Errorf("digest of no file = %q", none)
	}
}
