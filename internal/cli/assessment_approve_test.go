package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	reviewAnchorSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	reviewHeadSHA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reviewNow       = "2026-05-01T10:00:00Z"
	edgeApproval    = "assessments/approvals/ASM-EDGE-v1-1.yaml"
	evidenceDigest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// reviewWorkspaceYAML configures one protected-review authority and one required assessment.
const reviewWorkspaceYAML = assessmentWorkspaceYAML + `assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      reviewers: [alice@example.com]
      paths: [assessments/approvals/**, assessments/revocations/**]
    - id: signers
      role: reviewer
      mechanism: signed
      signers:
        - { identity: alice@example.com, issuer: "https://accounts.example.com" }
  required:
    - { id: edge, assessment: ASM-EDGE, type: security, environment: production, authority: security-review }
`

// reviewEdgeDoc is the edge assessment with evidence that records its digest.
const reviewEdgeDoc = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-EDGE
version: 1
type: security
scope:
  calmNodes: [api-gateway]
environment: production
subject:
  summary: Public entry points of the gateway
  dependencies:
    - kind: public-entry-point
      selector:
        calmNodes: [api-gateway]
assumptions: []
evidence:
  - id: EV-1
    ref: docs/security/edge-review.md
    digest: ` + evidenceDigest + `
outcome: satisfactory
state: ready-for-review
`

// reviewFacts is a fact source with one public entry point and the evidence file.
type reviewFacts struct{}

func (reviewFacts) Items(_ context.Context, sel impact.Selector) (impact.Match, error) { //nolint:gocritic // hugeParam: the FactSource contract takes the selector by value.
	if sel.Kind != impact.KindPublicEntryPoint {
		return impact.Match{State: impact.MatchUnsupported, Reason: impact.ReasonUnsupportedFamily, Profile: "cbm-0.12.1", Items: []impact.Item{}}, nil
	}

	facets := impact.Facets{}
	for _, f := range impact.V1().FieldsOf(sel.Kind) {
		facets[f] = []string{"api-" + f}
	}

	return impact.Match{
		State: impact.MatchFound,
		Items: []impact.Item{impact.V1().NewItem(sel.Kind, "k8s:ingress/api", facets, nil, nil)},
	}, nil
}

func (reviewFacts) FileDigest(_ context.Context, _, path string) (digest string, found bool, err error) {
	return evidenceDigest, path == "docs/security/edge-review.md", nil
}

func (reviewFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{
		ID: "cbm-0.12.1", Kinds: []impact.Kind{impact.KindPublicEntryPoint},
		Selectors: map[impact.Kind][]string{impact.KindPublicEntryPoint: {"host", "path"}},
	}
}

func (reviewFacts) EvaluatorVersions() map[string]string {
	return map[string]string{impact.EvaluatorNormalization: "1"}
}

// reviewDeps gives the dependencies of a review run: the facts, and a git whose anchor holds the workspace file.
func reviewDeps(anchorFiles map[string]string) cli.Deps {
	git := &workspace.FakeGit{
		Rev: reviewHeadSHA, Refs: map[string]string{"origin/main": reviewAnchorSHA},
		Ancestors: map[[2]string]bool{{reviewAnchorSHA, reviewHeadSHA}: true},
		FilesAt:   map[string]map[string]string{reviewAnchorSHA: anchorFiles},
	}

	return cli.Deps{
		NewGit: func(string) workspace.GitPort { return git },
		ReviewFacts: func(context.Context, *cli.Context, *workspace.Workspace) (impact.FactSource, error) {
			return reviewFacts{}, nil
		},
	}
}

func reviewTree() map[string]string {
	return map[string]string{
		"concordia.yaml":               reviewWorkspaceYAML,
		"assessments/ASM-EDGE/v1.yaml": reviewEdgeDoc,
	}
}

// reviewRun writes the tree and runs the CLI in it.
func reviewRun(t *testing.T, dir string, deps cli.Deps, env map[string]string, args ...string) result {
	t.Helper()

	if env == nil {
		env = map[string]string{}
	}

	env["CONCORDIA_NOW"] = reviewNow

	return runWith(t, &runConfig{Dir: dir, Deps: deps, Env: env}, args...)
}

var subjectDigestRe = regexp.MustCompile(`Subject digest: (sha256:[0-9a-f]{64})`)

// subjectDigestOf reads the subject digest that `assessment approve` prints.
func subjectDigestOf(t *testing.T, stdout string) string {
	t.Helper()

	m := subjectDigestRe.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("stdout has no subject digest:\n%s", stdout)
	}

	return m[1]
}

func noFile(t *testing.T, dir, rel string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
		t.Errorf("%s was written", rel)
	}
}

// stderrNames reports whether stderr holds every one of the parts.
func stderrNames(stderr string, parts ...string) bool {
	return !slices.ContainsFunc(parts, func(part string) bool { return !strings.Contains(stderr, part) })
}

func wantRefusal(t *testing.T, res result, part string) {
	t.Helper()

	if res.Code != 2 || !stderrNames(res.Stderr, "CO-ASM-005", part) {
		t.Errorf("exit code = %d, stderr = %q; want exit 2 with CO-ASM-005 and %q", res.Code, res.Stderr, part)
	}
}

// Scenario: Approve needs the examined digest.
func TestAssessmentApproveNeedsTheExaminedDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	res := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com")
	wantRefusal(t, res, "--confirm-subject")

	digest := subjectDigestOf(t, res.Stdout)
	for _, want := range []string{"ASM-EDGE@v1", "public-entry-point k8s:ingress/api", "Digest contract: review-digest/1"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("subject summary lacks %q:\n%s", want, res.Stdout)
		}
	}

	if !strings.Contains(res.Stderr, digest) {
		t.Errorf("the refusal does not give the digest to confirm: %q", res.Stderr)
	}

	noFile(t, dir, edgeApproval)

	// A digest that is not the current subject writes nothing either.
	res = reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--confirm-subject", "sha256:"+strings.Repeat("0", 64))
	wantRefusal(t, res, "differs")
	noFile(t, dir, edgeApproval)
}

// Scenario: Approve refused in CI.
func TestAssessmentApproveAndRevokeAreRefusedInCI(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := reviewTree()
	files["assessments/approvals/ASM-EDGE-v1-1.yaml"] = approvalOf(t, files)
	testutil.WriteTree(t, dir, files)
	before := dirFiles(t, dir)

	env := map[string]string{"CI": "true"}

	res := reviewRun(t, dir, reviewDeps(nil), env, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--confirm-subject", "sha256:"+strings.Repeat("0", 64))
	wantRefusal(t, res, "CI")

	res = reviewRun(t, dir, reviewDeps(nil), env, "assessment", "revoke", "ASM-EDGE-v1-1", "--reviewer", "alice@example.com",
		"--reason", "x", "--confirm-subject", "sha256:"+strings.Repeat("0", 64))
	wantRefusal(t, res, "CI")

	if after := dirFiles(t, dir); strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("files changed: %v -> %v", before, after)
	}
}

func TestAssessmentApproveNeedsAReviewer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	wantRefusal(t, reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-EDGE"), "--reviewer")
	noFile(t, dir, edgeApproval)
}

func TestAssessmentApproveRefusesWithoutFacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	deps := reviewDeps(nil)
	deps.ReviewFacts = nil

	res := reviewRun(t, dir, deps, nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--confirm-subject", "sha256:"+strings.Repeat("0", 64))
	wantRefusal(t, res, "cannot be computed")
	noFile(t, dir, edgeApproval)
}

func TestAssessmentApproveOfAnUnknownAssessmentFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	res := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-NONE", "--reviewer", "alice@example.com")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-ASM-001") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// approve runs the two documented steps and gives the text of the record.
func approve(t *testing.T, dir string, deps cli.Deps, extra ...string) (string, result) {
	t.Helper()

	args := []string{"assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com"}

	first := reviewRun(t, dir, deps, nil, args...)
	digest := subjectDigestOf(t, first.Stdout)

	args = append(append(args, "--confirm-subject", digest), extra...)
	res := reviewRun(t, dir, deps, nil, args...)

	text, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(edgeApproval)))

	return string(text), res
}

// approvalOf writes an approval of the edge assessment in a scratch directory and gives its text.
func approvalOf(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, files)

	text, res := approve(t, dir, reviewDeps(nil))
	if res.Code != 0 {
		t.Fatalf("approve: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return text
}

func dirFiles(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// approvalFacts are the fields of an approval that the review run decides.
type approvalFacts struct {
	id, decision, identity, authority, environment, contract, profile, normalization, authorityDigest string
	decidedAt                                                                                         int64
	dependencies                                                                                      int
	policyDigestSet                                                                                   bool
}

func factsOfApproval(approval *assessment.Approval) approvalFacts {
	return approvalFacts{
		id: approval.ID, decision: string(approval.Decision), identity: approval.Reviewer.Identity,
		authority: approval.Reviewer.Authority, environment: approval.Environment,
		contract: approval.ContractVersion, profile: approval.CapabilityProfile,
		normalization: approval.ProducedUnder.NormalizationVersion, authorityDigest: approval.ProducedUnder.AuthorityDigest,
		decidedAt: approval.DecidedAt.Unix(), dependencies: len(approval.Subject.Dependencies),
		policyDigestSet: approval.ProducedUnder.PolicyDigest != "",
	}
}

func assertApprovalStdout(t *testing.T, stdout string) {
	t.Helper()

	for _, want := range []string{"approval written: " + edgeApproval, "Record digest: sha256:", "trust anchor"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func assertApprovalFacts(t *testing.T, approval *assessment.Approval, dir string) {
	t.Helper()

	want := approvalFacts{
		id: "ASM-EDGE-v1-1", decision: string(assessment.DecisionApproved), identity: "alice@example.com",
		authority: "security-review", environment: "production", contract: "review-digest/1", profile: "cbm-0.12.1",
		normalization: "1", authorityDigest: authorityDigestOf(t, dir),
		decidedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Unix(), dependencies: 1, policyDigestSet: true,
	}

	if got := factsOfApproval(approval); got != want {
		t.Errorf("approval = %+v, facts %+v, want %+v", approval, got, want)
	}
}

// verifyApproval verifies the approval against an anchor that holds the given files.
func verifyApproval(t *testing.T, dir string, anchorFiles map[string]string, approval *assessment.Approval) (assessment.VerifyResult, error) {
	t.Helper()

	git := reviewDeps(anchorFiles).NewGit("")
	anchor := assessment.ReadAnchor(t.Context(), &assessment.AnchorSource{
		Git: git, Ref: "origin/main", WorkspacePath: filepath.Join(dir, "concordia.yaml"),
	})

	return (&assessment.ProtectedReview{Git: git, Anchor: anchor}).Verify(t.Context(), assessment.ApprovalRecord(approval))
}

// A successful approve writes one record that ProtectedReview accepts once it is on the anchor.
func TestAssessmentApproveWritesARecordThatProtectedReviewAccepts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())
	before := dirFiles(t, dir)

	text, res := approve(t, dir, reviewDeps(nil))
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	assertApprovalStdout(t, res.Stdout)

	if after := dirFiles(t, dir); len(after) != len(before)+1 {
		t.Errorf("files: %v -> %v, want exactly one new file", before, after)
	}

	approval, err := assessment.ParseApproval(edgeApproval, []byte(text))
	if err != nil {
		t.Fatalf("the record does not validate: %v", err)
	}

	assertApprovalFacts(t, approval, dir)

	// The record is on the anchor: the reviewer is listed, the digest is the same.
	verified, err := verifyApproval(t, dir, map[string]string{"concordia.yaml": reviewWorkspaceYAML, edgeApproval: text}, approval)
	if err != nil || !verified.Applied {
		t.Fatalf("on the anchor: %+v, %v", verified, err)
	}

	// Before it is placed on the anchor, it is not applied.
	verified, err = verifyApproval(t, dir, map[string]string{"concordia.yaml": reviewWorkspaceYAML}, approval)
	if err != nil {
		t.Errorf("before the anchor: %+v, %v", verified, err)
	}

	if verified.Applied || verified.Reason != assessment.NotOnAnchor {
		t.Errorf("before the anchor: %+v, %v", verified, err)
	}

	// A second approval is a new record and never overwrites the first.
	res = reviewRun(t, dir, reviewDeps(map[string]string{"concordia.yaml": reviewWorkspaceYAML}), nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--confirm-subject", approval.Subject.Digest)
	if res.Code != 0 || !strings.Contains(res.Stdout, "ASM-EDGE-v1-2.yaml") {
		t.Errorf("second approve: exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

// fakeCosignScript writes a cosign that writes the bundle on sign-blob and records its arguments.
func fakeCosignScript(t *testing.T) (script, argsFile string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	dir := t.TempDir()
	script = filepath.Join(dir, "cosign")
	argsFile = filepath.Join(dir, "args.txt")
	text := "#!/bin/sh\necho \"$@\" >> \"" + argsFile + "\"\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = \"--bundle\" ]; then echo '{}' > \"$2\"; fi; shift; done\n"

	if err := os.WriteFile(script, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}

	return script, argsFile
}

func TestAssessmentApproveSignsTheCanonicalRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	script, argsFile := fakeCosignScript(t)
	env := map[string]string{"COSIGN": script}

	first := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--authority", "signers")
	digest := subjectDigestOf(t, first.Stdout)

	res := reviewRun(t, dir, reviewDeps(nil), env, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--authority", "signers", "--confirm-subject", digest, "--sign", "--sign-key", "k")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	bundle := edgeApproval + ".sigstore.json"
	for _, rel := range []string{edgeApproval, bundle} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}

	if !strings.Contains(res.Stdout, "signature written: "+bundle) || !strings.Contains(res.Stdout, "signature, not the location") {
		t.Errorf("stdout = %q", res.Stdout)
	}

	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "sign-blob --yes --bundle "+filepath.Join(dir, filepath.FromSlash(bundle))+" --key k ") {
		t.Errorf("cosign arguments = %q", args)
	}

	if !strings.Contains(string(args), "concordia-record-") {
		t.Errorf("cosign signed the file, not the canonical JSON: %q", args)
	}
}

// No cosign is installed: --offline is decided first, so signing offline is CO-WRK-016 (exit 6), not CO-WRK-014.
func TestAssessmentApproveSigningOfflineWithoutCosignIsExit6(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	res := reviewRun(t, dir, reviewDeps(nil), nil, "--offline", "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--sign", "--sign-key", "k")
	if res.Code != 6 || !strings.Contains(res.Stderr, "CO-WRK-016") {
		t.Errorf("exit code = %d, stderr = %q, want exit 6 with CO-WRK-016", res.Code, res.Stderr)
	}

	noFile(t, dir, edgeApproval)
}

func TestAssessmentApproveSignNeedsCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	res := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com",
		"--sign", "--sign-key", "k")
	if res.Code != 5 || !strings.Contains(res.Stderr, "CO-WRK-014") {
		t.Errorf("exit code = %d, stderr = %q, want exit 5 with CO-WRK-014", res.Code, res.Stderr)
	}

	noFile(t, dir, edgeApproval)
}

// cosign 3 writes every signature, also one made with a key, to the public transparency log, so signing never runs
// with --offline: keyless and keyed signing both stop with CO-WRK-016 (exit 6) and write nothing.
func TestAssessmentApproveSigningOfflineIsExit6(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string][]string{"keyless": nil, "key": {"--sign-key", "k"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			testutil.WriteTree(t, dir, reviewTree())

			script, argsFile := fakeCosignScript(t)

			args := append([]string{"--offline", "assessment", "approve", "ASM-EDGE", "--reviewer", "alice@example.com", "--sign"}, extra...)

			res := runWith(t, &runConfig{Dir: dir, Deps: reviewDeps(nil), Env: map[string]string{"COSIGN": script, "CONCORDIA_NOW": reviewNow}}, args...)
			if res.Code != 6 || !stderrNames(res.Stderr, "CO-WRK-016", "transparency log") {
				t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
			}

			noFile(t, dir, edgeApproval)

			if _, err := os.Stat(argsFile); err == nil {
				t.Error("cosign was started with --offline")
			}
		})
	}
}

const edgeRevocationFile = "assessments/revocations/ASM-EDGE-v1-1.yaml"

// revokeFixture is a workspace that holds one approval of the edge assessment.
type revokeFixture struct {
	approval *assessment.Approval
	dir      string
	text     string
	args     []string
}

func newRevokeFixture(t *testing.T) *revokeFixture {
	t.Helper()

	dir := t.TempDir()
	files := reviewTree()
	text := approvalOf(t, files)
	files[edgeApproval] = text
	testutil.WriteTree(t, dir, files)

	approval, err := assessment.ParseApproval(edgeApproval, []byte(text))
	if err != nil {
		t.Fatal(err)
	}

	return &revokeFixture{
		approval: approval, dir: dir, text: text,
		args: []string{"assessment", "revoke", "ASM-EDGE-v1-1", "--reviewer", "alice@example.com", "--reason", "gateway replaced"},
	}
}

// revoke runs `assessment revoke` with the standard arguments and the extra ones.
func (f *revokeFixture) revoke(t *testing.T, extra ...string) result {
	t.Helper()

	return reviewRun(t, f.dir, reviewDeps(nil), nil, append(slices.Clone(f.args), extra...)...)
}

// confirmedRevoke revokes with the digest of the approval and gives the record that it writes.
func (f *revokeFixture) confirmedRevoke(t *testing.T) (written string, rev *assessment.Revocation) {
	t.Helper()

	res := f.revoke(t, "--confirm-subject", f.approval.Digest)
	if res.Code != 0 || !strings.Contains(res.Stdout, "revocation written: "+edgeRevocationFile) {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	raw, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(edgeRevocationFile)))
	if err != nil {
		t.Fatal(err)
	}

	rev, err = assessment.ParseRevocation(edgeRevocationFile, raw)
	if err != nil {
		t.Fatalf("revocation = %+v, %v", rev, err)
	}

	return string(raw), rev
}

// verifyOnAnchor wants the revocation to count once it is on the anchor.
func (f *revokeFixture) verifyOnAnchor(t *testing.T, written string, rev *assessment.Revocation) {
	t.Helper()

	git := reviewDeps(map[string]string{"concordia.yaml": reviewWorkspaceYAML, edgeApproval: f.text, edgeRevocationFile: written}).NewGit("")
	anchor := assessment.ReadAnchor(t.Context(), &assessment.AnchorSource{
		Git: git, Ref: "origin/main", WorkspacePath: filepath.Join(f.dir, "concordia.yaml"),
	})

	verified, err := (&assessment.ProtectedReview{Git: git, Anchor: anchor}).Verify(t.Context(), assessment.RevocationRecord(rev))
	if err != nil || !verified.Applied {
		t.Errorf("revocation on the anchor: %+v, %v", verified, err)
	}
}

func TestAssessmentRevoke(t *testing.T) {
	t.Parallel()

	fx := newRevokeFixture(t)

	// Without the digest: the approval is printed and nothing is written.
	res := fx.revoke(t)
	wantRefusal(t, res, "--confirm-subject")
	noFile(t, fx.dir, edgeRevocationFile)

	if !strings.Contains(res.Stdout, "Approval digest: "+fx.approval.Digest) {
		t.Errorf("stdout = %q", res.Stdout)
	}

	// A reason is required.
	res = reviewRun(t, fx.dir, reviewDeps(nil), nil, "assessment", "revoke", "ASM-EDGE-v1-1", "--reviewer", "alice@example.com")
	wantRefusal(t, res, "--reason")

	written, rev := fx.confirmedRevoke(t)

	gotFields := [4]string{rev.Approval.ID, rev.Approval.Digest, rev.Reviewer.Authority, rev.Reason}
	wantFields := [4]string{"ASM-EDGE-v1-1", fx.approval.Digest, "security-review", "gateway replaced"}

	if gotFields != wantFields {
		t.Fatalf("revocation = %+v", rev)
	}

	// The revocation counts once it is on the anchor.
	fx.verifyOnAnchor(t, written, rev)

	// A record is never overwritten.
	wantRefusal(t, fx.revoke(t, "--confirm-subject", fx.approval.Digest), "never overwritten")
}

// authorityDigestOf is the authority digest of the workspace file in dir.
func authorityDigestOf(t *testing.T, dir string) string {
	t.Helper()

	w, err := workspace.Load(filepath.Join(dir, "concordia.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}

	return assessment.AuthorityDigest(w.Assessments.Authorities)
}
