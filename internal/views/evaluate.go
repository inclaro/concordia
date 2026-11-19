package views

import (
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/workspace"
)

// outcome is the full result of the checks of one view, before it is turned into the plain result of
// `results.json` and into findings.
type outcome struct {
	View           *Definition
	Presence       PresenceResult
	Correspondence CorrespondenceResult
	Freshness      FreshnessResult
}

// Evaluate runs the presence, freshness and correspondence checks of every view that the workspace declares
// (design D1, D3). It is the implementation of conformance.Deps.EvaluateViews. The checks are independent: each
// has its own verdict, and a pass of one never changes another. It reads the tracked revision through the Git
// port, runs the pinned Structurizr CLI only for the exports of a generated view, and writes no file.
func Evaluate(ctx context.Context, input *conformance.ViewEvalInput) (*conformance.ViewEvaluation, error) {
	inputs := InputsOf(input.State)
	wsp := inputs.Workspace
	tools := &Tools{Getenv: input.Getenv, WorkspaceRoot: wsp.Root, AllowIncludes: wsp.Projection.AllowIncludes}
	indexed := DiagramPaths(input.State.Evidence.Nodes)
	evidence := DiagramEvidenceOf(input.State.Evidence)

	out := &conformance.ViewEvaluation{}
	outcomes := make([]outcome, 0, len(wsp.Views))

	for i := range wsp.Views {
		view := &wsp.Views[i]

		snap, err := SnapshotOf(inputs, view)
		if err != nil {
			return nil, err
		}

		res := outcome{View: view}
		res.Presence = CheckPresence(ctx, &PresenceInput{View: view, Git: input.Git, Root: wsp.Root, IndexedPaths: indexed})
		res.Freshness = CheckFreshness(ctx, &FreshnessInput{View: view, Inputs: inputs, Git: input.Git, Tools: tools, ToolVersion: input.ToolVersion})
		res.Correspondence = Correspond(&CorrespondenceInput{
			View: view, Declared: snap.Model, Observed: snap.Observed, Evidence: evidence, Model: inputs.Model,
			ImageDigest: committedImageDigest(ctx, input.Git, view), Freshness: res.Freshness.Status, Bindings: inputs.Bindings,
		})

		outcomes = append(outcomes, res)
		out.Views = append(out.Views, resultOf(ctx, input.Git, &res, snap, input.Currency))

		if out.StructurizrVersion == "" {
			out.StructurizrVersion = res.Freshness.StructurizrVersion
		}
	}

	out.Rules, out.Findings = rulesAndFindings(outcomes, input.OnUnknown)

	return out, nil
}

// committedImageDigest gives the digest of the committed raster image of an authored view, "" for another view or
// when the image is not in the tracked revision.
func committedImageDigest(ctx context.Context, git workspace.GitPort, view *Definition) string {
	if !view.IsRaster() {
		return ""
	}

	data, ok := committedFile(ctx, git, view.Source.Path)
	if !ok {
		return ""
	}

	return ArtifactDigest(data)
}

// resultOf makes the plain result of one view (conformance.ViewResult): the three checks, the assessment-currency
// entry (design D26, from the evaluation of the run), and the `GENERATED_FROM` link of a generated artifact.
func resultOf(ctx context.Context, git workspace.GitPort, res *outcome, snap *Snapshot, currency *assessment.Evaluation) conformance.ViewResult {
	view := res.View
	semantic := snap.Digests.Semantic

	result := conformance.ViewResult{
		ID: view.ID, Kind: string(view.Kind), Origin: string(view.Origin), Owner: view.Owner,
		Abstraction: string(view.Abstraction), Severity: view.EffectiveSeverity(), Scope: nonNil(slices.Clone(view.Scope)),
		SemanticDigest: &semantic, DigestContract: DigestContract, ProjectionContract: ProjectionContract,
		ContractChanged: res.Freshness.ContractChanged, Presence: presenceOf(&res.Presence), Freshness: freshnessOf(&res.Freshness),
		Correspondence: correspondenceOf(&res.Correspondence), Depicts: depictsOf(view, snap), NextAction: viewNextAction(res),
	}

	result.Assessment = conformance.ViewAssessmentCurrencyOf(currency, slices.Concat(result.Depicts.Elements, result.Depicts.Relationships))

	if view.Environment != "" {
		environment := view.Environment
		result.Environment = &environment
	}

	if view.Origin == OriginGenerated && view.Output != nil {
		result.GeneratedFrom = generatedFrom(ctx, git, res)
	}

	return result
}

// depictsOf gives the depicted subject: the claim of a supplemental mapping when the view has one, else the
// subject of the model projection (the elements and relationships that the view must show).
func depictsOf(view *Definition, snap *Snapshot) conformance.ViewDepicts {
	if view.IsRaster() && view.Mapping != nil && view.Mapping.Depicts != nil {
		depicts := view.Mapping.Depicts

		return conformance.ViewDepicts{Elements: sortedCopy(depicts.Elements), Relationships: sortedCopy(depicts.Relationships)}
	}

	return conformance.ViewDepicts{Elements: nonNil(slices.Clone(snap.Model.Depicts.Elements)), Relationships: nonNil(slices.Clone(snap.Model.Depicts.Relationships))}
}

// generatedFrom is the `GENERATED_FROM` link: the artifact path and digest, the semantic digest that the committed
// manifest names, and the manifest path.
func generatedFrom(ctx context.Context, git workspace.GitPort, res *outcome) *conformance.ViewGeneratedFrom {
	link := &conformance.ViewGeneratedFrom{ArtifactPath: res.View.Output.Path, ManifestPath: res.View.ManifestPath()}

	for i := range res.Freshness.Artifacts {
		if res.Freshness.Artifacts[i].Path == link.ArtifactPath {
			link.ArtifactDigest = res.Freshness.Artifacts[i].Digest
		}
	}

	if data, ok := committedFile(ctx, git, link.ManifestPath); ok {
		if manifest, err := ParseManifest(data); err == nil {
			link.SemanticDigest = manifest.SemanticDigest
		}
	}

	return link
}

// viewNextAction is the next-action category of the first check of the view that is not a pass (presence,
// freshness, correspondence). A view whose checks all pass or do not apply has none.
func viewNextAction(res *outcome) string {
	if action := presenceAction(res.View, &res.Presence); action != "" {
		return action
	}

	if action := freshnessAction(&res.Freshness); action != "" {
		return action
	}

	return res.Correspondence.NextAction
}

func presenceOf(p *PresenceResult) conformance.ViewPresence {
	files := make([]conformance.ViewPresenceFile, len(p.Files))
	for i, f := range p.Files {
		files[i] = conformance.ViewPresenceFile{Path: f.Path, Role: f.Role, Committed: f.Committed, Reason: f.Reason, Detail: f.Detail}
	}

	return conformance.ViewPresence{
		Status: verdictOf(p.Status), Revision: p.Revision, Reasons: nonNil(slices.Clone(p.Reasons)), Files: files,
	}
}

func freshnessOf(fresh *FreshnessResult) conformance.ViewFreshness {
	artifacts := make([]conformance.ViewArtifact, len(fresh.Artifacts))
	for i, a := range fresh.Artifacts {
		artifacts[i] = conformance.ViewArtifact{
			Path: a.Path, Format: a.Format, Digest: a.Digest, RegeneratedDigest: a.RegeneratedDigest, Status: verdictOf(a.Status),
			Detail: a.Detail, Reasons: nonNil(slices.Clone(a.Reasons)),
		}
	}

	return conformance.ViewFreshness{
		Status: verdictOf(fresh.Status), Verification: fresh.Verification, Reasons: nonNil(slices.Clone(fresh.Reasons)),
		DifferingDigests: nonNil(slices.Clone(fresh.DifferingDigests)), ManifestDifferences: nonNil(slices.Clone(fresh.ManifestDifferences)), Artifacts: artifacts,
	}
}

func correspondenceOf(corr *CorrespondenceResult) conformance.ViewCorrespondence {
	differences := make([]conformance.ViewDifference, len(corr.Differences))
	for i := range corr.Differences {
		d := &corr.Differences[i]
		differences[i] = conformance.ViewDifference{
			Kind: d.Kind, Subject: d.Subject, Basis: d.Basis, From: d.From, To: d.To, Detail: d.Detail, Locations: viewLocations(d.Locations),
		}
	}

	unknown := make([]conformance.ViewUnknownReason, len(corr.Unknown))
	for i := range corr.Unknown {
		u := &corr.Unknown[i]
		unknown[i] = conformance.ViewUnknownReason{Reason: u.Reason, Subject: u.Subject, Candidates: u.Candidates, Locations: viewLocations(u.Locations)}
	}

	lost := make([]conformance.ViewLostCoverage, len(corr.Lost))
	for i, l := range corr.Lost {
		lost[i] = conformance.ViewLostCoverage{Kind: l.Kind, Subject: l.Subject}
	}

	return conformance.ViewCorrespondence{
		Status: verdictOf(corr.Status), Method: corr.Method, Basis: corr.Basis, NextAction: corr.NextAction,
		Source: conformance.ViewLocation{Path: corr.Source.Path, Line: corr.Source.Line}, Section: corr.Section, QualifiedName: corr.QualifiedName,
		Against: nonNil(slices.Clone(corr.Against)), Compared: nonNil(slices.Clone(corr.Compared)), ByConstruction: nonNil(slices.Clone(corr.ByConstruction)),
		Differences: differences, Unknown: unknown, Lost: lost, Reasons: nonNil(slices.Clone(corr.Reasons)),
	}
}

func viewLocations(in []Location) []conformance.ViewLocation {
	out := make([]conformance.ViewLocation, len(in))
	for i, l := range in {
		out[i] = conformance.ViewLocation{Path: l.Path, Line: l.Line}
	}

	return out
}

// verdictOf maps the status of a view check to the verdict of a rule: the sets are the same words.
func verdictOf(s Status) conformance.RuleVerdict {
	switch s {
	case StatusPass:
		return conformance.VerdictPass
	case StatusViolation:
		return conformance.VerdictViolation
	case StatusUnknown:
		return conformance.VerdictUnknown
	case StatusNotApplicable:
		return conformance.VerdictNotApplicable
	case StatusError:
		return conformance.VerdictError
	}

	return conformance.VerdictError
}
