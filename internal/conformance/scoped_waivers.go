package conformance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	waiverStateActive  = "active"
	waiverStateExpired = "expired"
)

// ScopedWaiver is the record of one scoped waiver in `exceptions.scopedWaivers` of results.json and in
// `exceptions/scoped.json` of the bundle. It holds no free text of the waiver (reason, issue) and no evidence
// content: only the scope, the approver, the verification result, the expiry, the state of each compensating
// evidence reference, and the fingerprints of the findings that the waiver covered.
type ScopedWaiver struct {
	ID    string           `json:"id"`
	Rule  string           `json:"rule"`
	Scope exceptions.Scope `json:"scope"`
	Owner string           `json:"owner"`
	// Expires is YYYY-MM-DD.
	Expires string `json:"expires"`
	// State is `active` or `expired`.
	State string `json:"state"`
	// Digest is the digest of the canonical entry: what approver verification compared.
	Digest   string              `json:"digest"`
	Approver exceptions.Approver `json:"approver"`
	// Verification is the result of approver verification. A waiver that is not verified is not applied.
	Verification exceptions.Verification `json:"verification"`
	// CompensatingEvidence has one value for each reference of the waiver, in file order. Never nil.
	CompensatingEvidence []exceptions.EvidenceResult `json:"compensatingEvidence"`
	// CoveredFindings are the fingerprints of the findings that the waiver is the disposition of, sorted. Never nil.
	CoveredFindings []string `json:"coveredFindings"`
}

// WaiverVerifyOptions says how scoped waivers are verified outside a run (`waiver validate`) and in a run.
type WaiverVerifyOptions struct {
	Workspace *workspace.Workspace
	// Git reads the trust anchor and the register at it.
	Git workspace.GitPort
	// Anchor is the trust anchor, when the caller has read it already. nil reads it from Git.
	Anchor *assessment.Anchor
	// Getenv reads the environment, for $COSIGN. nil reads nothing.
	Getenv func(string) string
	// Offline refuses network access: a keyless signature cannot be verified.
	Offline bool
}

// VerifyScopedWaivers verifies the approver of each scoped waiver of the register (an absolute OS path) against the
// trust anchor of the workspace (`assessments.trustAnchor.ref`), with the authorities that the anchor lists. It gives one
// value for each scoped waiver in the order of the register, and none when the register has no scoped waiver. The
// error is a condition that stops the run, such as a missing cosign (CO-WRK-014).
func VerifyScopedWaivers(ctx context.Context, opts *WaiverVerifyOptions, register string, waivers []exceptions.Waiver) ([]exceptions.Verification, error) {
	if !slices.ContainsFunc(waivers, func(w exceptions.Waiver) bool { return w.IsScoped() }) {
		return nil, nil
	}

	wsp := opts.Workspace

	rel, err := fsutil.ToPosixRelative(wsp.Root, register)
	if err != nil || strings.HasPrefix(rel, "../") {
		return exceptions.VerifyApprovers(ctx, outsideWorkspace{}, register, waivers) //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	anchor := opts.Anchor
	if anchor == nil {
		ref := ""
		if wsp.Assessments != nil && wsp.Assessments.TrustAnchor != nil {
			ref = wsp.Assessments.TrustAnchor.Ref
		}

		anchor = assessment.ReadAnchor(ctx, &assessment.AnchorSource{Git: opts.Git, Ref: ref, WorkspacePath: wsp.Path, Profile: wsp.Profile})
	}

	verifier := assessment.NewAuthorities(&assessment.AuthorityOptions{
		Git: opts.Git, Anchor: anchor, Root: wsp.Root, Getenv: opts.Getenv, Offline: opts.Offline,
	})

	return exceptions.VerifyApprovers(ctx, verifier, rel, waivers) //nolint:wrapcheck // a *diag.Error that the caller reports.
}

// outsideWorkspace is the verifier of a register that lies outside the workspace: it cannot be on the trust
// anchor, so no waiver of it is verified.
type outsideWorkspace struct{}

func (outsideWorkspace) Verify(_ context.Context, rec assessment.Record) (assessment.VerifyResult, error) { //nolint:gocritic // the Verifier interface passes the record by value.
	return assessment.VerifyResult{
		Authority: rec.Reviewer.Authority, Reason: assessment.NotOnAnchor,
		Detail: "the waiver register is outside the workspace, so it cannot be on the trust anchor",
	}, nil
}

// scopedState is what a run knows about the scoped waivers of its register.
type scopedState struct {
	verifications map[string]exceptions.Verification
	evidence      map[string][]exceptions.EvidenceResult
}

// verifyScopedWaivers verifies the approver of each scoped waiver and resolves its compensating evidence before the
// dispositions are applied. A failed verification is the warning CO-EXC-009, and an evidence problem is the warning
// CO-EXC-011. It does nothing when the register has no scoped waiver.
func (c *checker) verifyScopedWaivers(ctx context.Context, wspace *workspace.Workspace, inp *inputs) error {
	opts := &WaiverVerifyOptions{
		Workspace: wspace, Git: c.newGit(wspace.Root), Getenv: c.getenv(), Offline: c.opts.Env.Offline,
	}

	if slices.ContainsFunc(inp.waivers, func(w exceptions.Waiver) bool { return w.IsScoped() }) {
		opts.Anchor = c.reviewAnchor(ctx, wspace)
	}

	results, err := VerifyScopedWaivers(ctx, opts, inp.waiversPath, inp.waivers)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		return nil
	}

	state := &scopedState{verifications: make(map[string]exceptions.Verification, len(results)), evidence: map[string][]exceptions.EvidenceResult{}}

	for i := range results {
		state.verifications[results[i].WaiverID] = results[i]

		if d := results[i].Diagnostic; d != nil {
			c.diags = append(c.diags, *d)
		}
	}

	for i := range inp.waivers {
		entry := &inp.waivers[i]
		if !entry.IsScoped() {
			continue
		}

		found, warnings := exceptions.ResolveEvidence(wspace.Root, entry)
		state.evidence[entry.ID] = found

		c.diags = append(c.diags, warnings...)
	}

	inp.scoped = state

	return nil
}

// applicable gives the waivers that may be applied: every finding waiver, and the scoped waivers whose approver
// is verified. A scoped waiver that is not verified changes no disposition.
func (s *scopedState) applicable(waivers []exceptions.Waiver) []exceptions.Waiver {
	if s == nil {
		return waivers
	}

	out := make([]exceptions.Waiver, 0, len(waivers))

	for i := range waivers {
		if waivers[i].IsScoped() && !s.verifications[waivers[i].ID].Verified {
			continue
		}

		out = append(out, waivers[i])
	}

	return out
}

// records gives the ScopedWaiver record of each scoped waiver, sorted by id, with the findings that it covered.
// nil when the register has no scoped waiver.
func (s *scopedState) records(waivers []exceptions.Waiver, findings []Finding, when time.Time) []ScopedWaiver {
	if s == nil {
		return nil
	}

	covered := map[string][]string{}

	for i := range findings {
		if ref := findings[i].DispositionRef; ref != nil {
			covered[*ref] = append(covered[*ref], findings[i].Fingerprint)
		}
	}

	out := []ScopedWaiver{}

	for i := range waivers {
		entry := &waivers[i]
		if !entry.IsScoped() {
			continue
		}

		sum, err := entry.Digest()
		if err != nil {
			sum = ""
		}

		state := waiverStateExpired
		if exceptions.IsWaiverActive(entry, when) {
			state = waiverStateActive
		}

		fingerprints := slices.Clone(covered[entry.ID])
		slices.SortFunc(fingerprints, canonjson.CompareUTF16)

		verification := s.verifications[entry.ID]
		verification.Detail, _ = redact.Default().RedactValue("detail", verification.Detail)

		owner, _ := redact.Default().RedactValue("owner", entry.Owner)

		out = append(out, ScopedWaiver{
			ID: entry.ID, Rule: entry.Rule, Scope: *entry.Scope, Owner: owner, Expires: entry.Expires, State: state, Digest: sum,
			Approver: *entry.Approver, Verification: verification,
			CompensatingEvidence: nonNilEvidence(s.evidence[entry.ID]), CoveredFindings: nonNilStrings(fingerprints),
		})
	}

	slices.SortFunc(out, func(a, b ScopedWaiver) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return out
}

func nonNilEvidence(list []exceptions.EvidenceResult) []exceptions.EvidenceResult {
	if list == nil {
		return []exceptions.EvidenceResult{}
	}

	return list
}

// runExceptions gives the `exceptions` section of the run.
func (d *disposed) runExceptions() RunExceptions {
	return RunExceptions{
		ResolvedBaselineEntries: d.resolved, OrphanedWaivers: d.orphaned, Waivers: d.applied, ScopedWaivers: d.scoped,
	}
}

// scopedWaiversSchemaVersion is the `schemaVersion` of the bundle file `exceptions/scoped.json`.
const scopedWaiversSchemaVersion = "1.0"

// ScopedWaiversJSON gives the content of the bundle file `exceptions/scoped.json`: the canonical JSON of
// the scoped waivers of the run with their approver, verification result, expiry, evidence state, and the findings
// that they covered. It holds the same records as `exceptions.scopedWaivers` of results.json, so no waiver reason,
// issue, or evidence content, and no secret value.
func ScopedWaiversJSON(run *Run) ([]byte, error) {
	list := run.Exceptions.ScopedWaivers
	if list == nil {
		list = []ScopedWaiver{}
	}

	out, err := canonjson.Marshal(struct {
		SchemaVersion string         `json:"schemaVersion"`
		ScopedWaivers []ScopedWaiver `json:"scopedWaivers"`
	}{scopedWaiversSchemaVersion, list})
	if err != nil {
		return nil, fmt.Errorf("encode scoped waivers: %w", err)
	}

	return out, nil
}
