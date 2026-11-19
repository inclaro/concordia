package integration_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
	"github.com/inclaro/concordia/internal/views"
)

// These tests run the view checks on the view-currency fixture (test/fixtures/viewcurrency) against the CBM graph
// that `mise run record-cbm` recorded (cmd/record-cbm/views.go). They replay the recordings and need no
// codebase-memory-mcp: the pipeline of `check` gives the run, the provider adapter gives the diagram evidence,
// and the checks of internal/views decide. Each test is one scenario of the view-currency specs: A14 to A19.

const (
	vcFixtureDir  = "../fixtures/viewcurrency"
	vcBase        = "../fixtures/cbm-recordings/viewcurrency.json"
	vcReordered   = "../fixtures/cbm-recordings/viewcurrency-reordered.json"
	vcDrift       = "../fixtures/cbm-recordings/viewcurrency-drift.json"
	vcRefactored  = "../fixtures/cbm-recordings/viewcurrency-refactored.json"
	vcProject     = "concordia-fixture-viewcurrency"
	vcDiagramDoc  = "docs/architecture.md"
	vcDraftDoc    = "docs/drafts/context.md"
	vcImage       = "docs/context.png"
	vcDepicted    = "docs/context-depicted.png"
	vcContainers  = "shop-containers"
	vcContext     = "shop-context"
	vcDiagramView = "shop-containers-diagram"
	vcDraftView   = "shop-draft"
	vcImageView   = "shop-context-image"
	vcDepictView  = "shop-context-depicted"

	// vcImageDigest and vcDepictedDigest are the sha256 of the two images of the fixture.
	vcImageDigest    = "sha256:985ad62a56bea546be523ba205cc4786b3b4c6dfc16c928e0e4a5fadab13e79e"
	vcDepictedDigest = "sha256:d3748985cf800f6a065299816d8da8ee2e45923500c49a5d6bae74541842bece"
)

// vcDiagramKeysAndLabels lists the element IDs and the labels of a diagram read.
func vcDiagramKeysAndLabels(read *views.DiagramRead) (keys, labels []string) {
	for _, e := range read.Structure.Elements {
		keys, labels = append(keys, e.ID.ElementID), append(labels, e.Label)
	}

	return keys, labels
}

// vcSameDiagramKeys is true when both reads have the author's node IDs and the same labels.
func vcSameDiagramKeys(keys, otherKeys, labels, otherLabels []string) bool {
	wantKeys := []string{"billing", "customer", "db", "orders", "web"}

	return slices.Equal(keys, wantKeys) && slices.Equal(keys, otherKeys) && slices.Equal(labels, otherLabels)
}

func requireDiagramElementsOfBothRecordings(t *testing.T, first, second *views.DiagramRead) {
	t.Helper()

	keys, labels := vcDiagramKeysAndLabels(first)
	otherKeys, otherLabels := vcDiagramKeysAndLabels(second)

	if !vcSameDiagramKeys(keys, otherKeys, labels, otherLabels) {
		t.Errorf("keys = %v / %v, labels = %v / %v", keys, otherKeys, labels, otherLabels)
	}

	// The label text is the provider name; a node without one has no label.
	if want := []string{"Billing", "Customer", "Orders DB", "Orders API", "Web App"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %q, want %q", labels, want)
	}
}

func requireDiagramArrows(t *testing.T, read *views.DiagramRead) {
	t.Helper()

	arrows := map[string]string{}

	for i := range read.Structure.Arrows {
		a := &read.Structure.Arrows[i]
		arrows[a.From.ElementID+">"+a.To.ElementID] = a.Label
	}

	if want := map[string]string{"customer>web": "", "web>orders": "HTTPS", "orders>billing": "HTTPS", "orders>db": "TCP"}; !maps.Equal(arrows, want) {
		t.Errorf("arrows = %v, want %v", arrows, want)
	}
}

// requirePNGNotIndexed checks O1: CBM does not index the PNG, so no node holds its path and the index result names it.
func requirePNGNotIndexed(t *testing.T, base *vcRepo) {
	t.Helper()

	if paths := views.DiagramPaths(base.diagram.Nodes); slices.Contains(paths, vcImage) || !slices.Contains(paths, vcDiagramDoc) {
		t.Errorf("diagram paths = %v", paths)
	}

	index, err := cbmprobe.Index(t.Context(), observation.NewReplayClient(base.rec), "/anywhere/else", vcProject)
	if err != nil {
		t.Fatal(err)
	}

	for _, image := range []string{vcImage, vcDepicted} {
		if !slices.Contains(index.NotIndexedFiles.Files, cbmprobe.PathReason{Path: image, Reason: "ignored-suffix"}) {
			t.Errorf("not_indexed_files = %v, want %s with ignored-suffix", index.NotIndexedFiles.Files, image)
		}
	}
}

// O1 and O12 of the CBM 0.12.1 observations, as the recording holds them: the elements of a Mermaid diagram have the
// author's node IDs and, in the provider `name`, the label text; the ordinal of the diagram is in its qualified name;
// a PNG is not indexed.
func TestViewCurrencyRecordingReproducesO1AndO12(t *testing.T) {
	base, reordered := newVCRepo(t, vcBase), newVCRepo(t, vcReordered)
	selector := views.DiagramSelector{Title: "Shop containers"}

	first := views.ReadDiagram(vcDiagramDoc, selector, base.diagram)
	second := views.ReadDiagram(vcDiagramDoc, selector, reordered.diagram)

	if !first.Available() || !second.Available() {
		t.Fatalf("reasons = %q, %q", first.Reason, second.Reason)
	}

	// The flowchart follows a sequence diagram in the section; swapping them changes the ordinal.
	if !strings.HasSuffix(first.Structure.QualifiedName, "#diagram-2") || !strings.HasSuffix(second.Structure.QualifiedName, "#diagram-1") {
		t.Errorf("qualified names = %s, %s", first.Structure.QualifiedName, second.Structure.QualifiedName)
	}

	requireDiagramElementsOfBothRecordings(t, &first, &second)
	requireDiagramArrows(t, &first)
	requirePNGNotIndexed(t, base)
}

// A14: CBM reports a diagram for the draft, and the tracked revision does not list its path.
func TestViewCurrencyA14DiagramOnlyInTheIndexWorkspace(t *testing.T) {
	repo := newVCRepo(t, vcBase)
	view := repo.view(t, vcDraftView)

	indexed := views.DiagramPaths(repo.diagram.Nodes)
	if !slices.Contains(indexed, vcDraftDoc) {
		t.Fatalf("CBM reports no diagram for %s: %v", vcDraftDoc, indexed)
	}

	// The draft is in the index workspace only: the working tree of the check has no such file either.
	got := views.CheckPresence(t.Context(), &views.PresenceInput{View: view, Git: repo.git(vcDraftDoc), Root: t.TempDir(), IndexedPaths: indexed})
	if got.Status != views.StatusViolation || !slices.Equal(got.Reasons, []string{views.ReasonIndexOnly}) {
		t.Fatalf("presence = %+v", got)
	}

	if len(got.Files) != 1 || !strings.Contains(got.Files[0].Detail, "repository listing of revision "+repo.rec.HeadSHA+" is complete and does not contain "+vcDraftDoc) {
		t.Errorf("files = %+v", got.Files)
	}

	// A commit of the draft makes presence pass; the index never did.
	if pass := views.CheckPresence(t.Context(), &views.PresenceInput{View: view, Git: repo.git(), Root: t.TempDir(), IndexedPaths: nil}); pass.Status != views.StatusPass {
		t.Errorf("committed: presence = %+v", pass)
	}
}

// vcSameDigests is true when the semantic, observed and model digests of two views agree.
func vcSameDigests(a, b *views.Digests) bool {
	return a.Semantic == b.Semantic && a.Observed == b.Observed && a.Model == b.Model
}

func requireRefactoringKeepsGeneratedViews(t *testing.T, base, refactored *vcRepo) {
	t.Helper()

	for _, id := range []string{vcContainers, vcContext} {
		a, b := base.semantic(t, id), refactored.semantic(t, id)
		if !vcSameDigests(&a, &b) {
			t.Errorf("%s: the semantic digest changed with the refactoring: %+v -> %+v", id, a, b)
		}

		first, _ := base.generated(t, id)
		second, _ := refactored.generated(t, id)

		if !bytes.Equal(first.DSL, second.DSL) {
			t.Errorf("%s: the DSL changed with the refactoring", id)
		}
	}
}

func requireNewContainerCallChangesContainerViewOnly(t *testing.T, base, drift *vcRepo) {
	t.Helper()

	if a, b := base.semantic(t, vcContainers), drift.semantic(t, vcContainers); a.Semantic == b.Semantic || a.Observed == b.Observed {
		t.Errorf("%s: a new call between two containers left the semantic digest unchanged", vcContainers)
	}

	// At the system level that call is inside the shop.
	if a, b := base.semantic(t, vcContext), drift.semantic(t, vcContext); a.Semantic != b.Semantic {
		t.Errorf("%s: the semantic digest changed", vcContext)
	}
}

// A15: an internal refactoring changes no digest of the generated container view and context view, and the check of a
// diagram that has no methods stays `pass`. An undeclared call between two containers changes the container view only.
func TestViewCurrencyA15InternalRefactoring(t *testing.T) {
	base, refactored, drift := newVCRepo(t, vcBase), newVCRepo(t, vcRefactored), newVCRepo(t, vcDrift)

	// The refactoring adds calls between the functions of the orders service.
	if callsInOrders(base) >= callsInOrders(refactored) {
		t.Fatalf("calls: base %d, refactored %d", callsInOrders(base), callsInOrders(refactored))
	}

	requireRefactoringKeepsGeneratedViews(t, base, refactored)
	requireNewContainerCallChangesContainerViewOnly(t, base, drift)

	if got := refactored.correspondence(t, vcDiagramView); got.Status != views.StatusPass {
		t.Errorf("refactored: correspondence = %s, differences %v, reasons %v", got.Status, vcDifferences(&got), got.Reasons)
	}
}

// vcWebBillingDrift is the difference of the observed call from the web app to the billing service.
var vcWebBillingDrift = []string{"missing|relationship|observed|web-app>billing-service"}

// requireFreshRenderPasses checks the freshness of the generated view of repo, regenerated from the current CALM
// file, and gives the generated view.
func requireFreshRenderPasses(t *testing.T, repo *vcRepo, view *views.Definition) (*views.Generated, views.Status) {
	t.Helper()

	g, files := repo.generated(t, vcContainers)

	fresh := views.CheckFreshness(t.Context(), &views.FreshnessInput{
		View: view, Inputs: repo.inputs, Git: repo.gitWith(files), ToolVersion: "test",
		Tools: &views.Tools{Getenv: func(string) string { return "" }, WorkspaceRoot: repo.dir},
	})
	if fresh.Status != views.StatusPass || fresh.Verification != views.VerificationRegenerated {
		t.Fatalf("freshness = %+v", fresh)
	}

	return g, fresh.Status
}

func isCommittedGeneratedSourceCorrection(got *views.CorrespondenceResult) bool {
	return got.Method == views.MethodGeneratedSource && got.Basis == views.CorrespondenceBasisCommitted && got.NextAction == views.NextActionCorrectIntent
}

// A16: the generated view was regenerated from the current CALM file, which lacks a call that the observed graph
// holds. Freshness passes, and correspondence is a violation.
func TestViewCurrencyA16FreshRenderOfStaleIntent(t *testing.T) {
	repo := newVCRepo(t, vcDrift)
	view := repo.view(t, vcContainers)

	g, freshness := requireFreshRenderPasses(t, repo, view)

	got := views.Correspond(&views.CorrespondenceInput{View: view, Declared: g.Model, Observed: g.Observed, Freshness: freshness})
	if got.Status != views.StatusViolation || !slices.Equal(vcDifferences(&got), vcWebBillingDrift) {
		t.Fatalf("correspondence = %s, differences %v, reasons %v", got.Status, vcDifferences(&got), got.Reasons)
	}

	if !isCommittedGeneratedSourceCorrection(&got) {
		t.Errorf("method %q, basis %q, next action %q", got.Method, got.Basis, got.NextAction)
	}

	// The same code without the call has no drift.
	base := newVCRepo(t, vcBase)

	baseG, _ := base.generated(t, vcContainers)
	if ok := views.Correspond(&views.CorrespondenceInput{View: view, Declared: baseG.Model, Observed: baseG.Observed, Freshness: views.StatusPass}); ok.Status != views.StatusPass {
		t.Errorf("base: correspondence = %s, differences %v", ok.Status, vcDifferences(&ok))
	}
}

// isPassingStructuredCorrespondence is true for a pass from a structured source with no difference and nothing unknown.
func isPassingStructuredCorrespondence(got *views.CorrespondenceResult) bool {
	if got.Status != views.StatusPass || got.Method != views.MethodStructuredSource {
		return false
	}

	return len(got.Differences) == 0 && len(got.Unknown) == 0
}

// A17: the container diagram has no methods, the observed graph has calls between the functions of the orders
// service, and the mapped container arrows match.
func TestViewCurrencyA17MethodsAreNotDrift(t *testing.T) {
	repo := newVCRepo(t, vcBase)

	if callsInOrders(repo) == 0 {
		t.Fatal("the recording has no call between two functions of the orders service")
	}

	got := repo.correspondence(t, vcDiagramView)
	if !isPassingStructuredCorrespondence(&got) {
		t.Fatalf("correspondence = %+v", got)
	}

	if !slices.Equal(got.Compared, []string{"declared", "observed"}) {
		t.Errorf("compared = %v", got.Compared)
	}

	// An observed call that the diagram lacks is drift.
	drift := newVCRepo(t, vcDrift).correspondence(t, vcDiagramView)
	if drift.Status != views.StatusViolation || !slices.Equal(vcDifferences(&drift), vcWebBillingDrift) {
		t.Errorf("drift: correspondence = %s, differences %v", drift.Status, vcDifferences(&drift))
	}
}

// A18: the diagram blocks change order, so the provider ordinal of the diagram changes. The view ID, the mapping
// keys, the result and the semantic digest do not.
func TestViewCurrencyA18DiagramBlocksGetANewOrder(t *testing.T) {
	base, reordered := newVCRepo(t, vcBase), newVCRepo(t, vcReordered)

	first, second := base.correspondence(t, vcDiagramView), reordered.correspondence(t, vcDiagramView)
	if first.Status != views.StatusPass || second.Status != views.StatusPass {
		t.Fatalf("correspondence = %s, %s", first.Status, second.Status)
	}

	if first.QualifiedName == second.QualifiedName || first.Section != second.Section {
		t.Errorf("qualified names = %s, %s; sections = %s, %s", first.QualifiedName, second.QualifiedName, first.Section, second.Section)
	}

	first.QualifiedName, second.QualifiedName = "", ""

	if a, b := mustJSON(t, first), mustJSON(t, second); !bytes.Equal(a, b) {
		t.Errorf("the result changed with the order of the blocks:\n%s\n%s", a, b)
	}

	if a, b := base.semantic(t, vcDiagramView), reordered.semantic(t, vcDiagramView); a != b {
		t.Errorf("the digests changed with the order of the blocks: %+v -> %+v", a, b)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// requireImageViewHasPresenceAndNoFreshness checks that presence of an image view comes from Git alone and that
// its freshness is not applicable but names the digest of the image.
func requireImageViewHasPresenceAndNoFreshness(t *testing.T, repo *vcRepo, id string) {
	t.Helper()

	view := repo.view(t, id)

	// Presence comes from Git alone: CBM has no node for the image.
	got := views.CheckPresence(t.Context(), &views.PresenceInput{View: view, Git: repo.git(), Root: repo.dir, IndexedPaths: views.DiagramPaths(repo.diagram.Nodes)})
	if got.Status != views.StatusPass || len(got.Reasons) != 0 {
		t.Errorf("%s: presence = %+v", id, got)
	}

	fresh := views.CheckFreshness(t.Context(), &views.FreshnessInput{View: view, Inputs: repo.inputs, Git: repo.git(), ToolVersion: "test"})
	if fresh.Status != views.StatusNotApplicable || len(fresh.Artifacts) != 1 {
		t.Errorf("%s: freshness = %+v", id, fresh)

		return
	}

	if fresh.Artifacts[0].Digest != vcImageDigestOf(id) {
		t.Errorf("%s: freshness = %+v", id, fresh)
	}
}

func isPresenceOnlyCorrespondence(got *views.CorrespondenceResult) bool {
	if got.Status != views.StatusUnknown || got.Method != views.MethodPresenceOnly {
		return false
	}

	return got.NextAction == views.NextActionSupplyEvidence && len(got.Compared) == 0
}

func isComparedSupplementalMapping(got *views.CorrespondenceResult) bool {
	if got.Status != views.StatusPass || got.Method != views.MethodSupplementalMapping {
		return false
	}

	return slices.Equal(got.Compared, []string{"declared", "observed"})
}

func isStaleSupplementalMapping(got *views.CorrespondenceResult) bool {
	if got.Status != views.StatusUnknown || got.Method != views.MethodSupplementalMapping {
		return false
	}

	return len(got.Unknown) == 1 && got.Unknown[0].Reason == views.ReasonSupplementalStale
}

// A19: an image with no structured source has presence and no automated pass; with a depiction that is bound to its
// digest it is compared, and another image makes the depiction stale.
func TestViewCurrencyA19ImageOnlyDiagram(t *testing.T) {
	repo := newVCRepo(t, vcBase)

	for _, id := range []string{vcImageView, vcDepictView} {
		requireImageViewHasPresenceAndNoFreshness(t, repo, id)
	}

	none := repo.correspondence(t, vcImageView)
	if !isPresenceOnlyCorrespondence(&none) {
		t.Errorf("image only: correspondence = %+v", none)
	}

	depicted := repo.correspondence(t, vcDepictView)
	if !isComparedSupplementalMapping(&depicted) {
		t.Errorf("supplemental mapping: correspondence = %+v", depicted)
	}

	// The committed image is replaced; mapping.depicts keeps the old digest.
	view := repo.view(t, vcDepictView)
	declared, observed := repo.projections(view)
	replaced := views.Correspond(&views.CorrespondenceInput{
		View: view, Declared: declared, Observed: observed, Model: repo.inputs.Model, Bindings: repo.inputs.Bindings, ImageDigest: vcImageDigest,
	})

	if !isStaleSupplementalMapping(&replaced) {
		t.Errorf("image replaced: correspondence = %+v", replaced)
	}
}

// The check pipeline itself keeps the fixture valid: it loads the workspace with its six views and gives the state
// that the view checks read.
func TestViewCurrencyFixtureRunsThroughTheCheckPipeline(t *testing.T) {
	repo := newVCRepo(t, vcBase)

	ids := make([]string, 0, len(repo.inputs.Workspace.Views))
	for i := range repo.inputs.Workspace.Views {
		ids = append(ids, repo.inputs.Workspace.Views[i].ID)
	}

	want := []string{vcContainers, vcContext, vcDiagramView, vcDraftView, vcImageView, vcDepictView}
	if !slices.Equal(ids, want) {
		t.Errorf("views = %v, want %v", ids, want)
	}

	if repo.run.ViewInput == nil || repo.reconcile == nil {
		t.Fatal("the run kept no view input")
	}
}
