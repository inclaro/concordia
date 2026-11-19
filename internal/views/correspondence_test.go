package views_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
)

const (
	imageDigest = "sha256:985ad62a56bea546be523ba205cc4786b3b4c6dfc16c928e0e4a5fadab13e79e"
	otherDigest = "sha256:d3748985cf800f6a065299816d8da8ee2e45923500c49a5d6bae74541842bece"
)

// The mapping of the standard diagram: diagram identifier to CALM ID.
var standardMapping = map[string]string{
	"customer": "customer", "web": "web-ui", "orders": "orders-service", "db": "payments-db",
	"ledger": "ledger", "audit": "audit-db",
}

// standardDiagram shows every element of the container view of the projection model and its four declared arrows.
func standardDiagram() block {
	return block{
		section: "Payments-containers", ordinal: 1, kind: "flowchart", format: "mermaid",
		elements: []string{"customer", "web=Web UI", "orders=Orders", "db=Payments DB", "ledger", "audit"},
		arrows:   []string{"customer>web", "web>orders:HTTPS", "orders>db:SQL", "orders>ledger:gRPC"},
	}
}

// authoredView is the authored container view of the diagram at diagramFile.
func diagramView(mapping map[string]string, against ...string) views.Definition {
	v := containerView("payments-diagram")
	v.Origin, v.Output = views.OriginAuthored, nil
	v.Source = &views.Source{Path: diagramFile, Format: "mermaid", Selector: &views.Selector{Title: "Payments containers"}}
	v.Mapping = &views.Mapping{Elements: mapping}

	if len(against) > 0 {
		v.Comparison = &views.Comparison{Against: against}
	}

	return v
}

// quietCode is the code of the model: calls from the web UI to the orders service, from the handler component
// to the database, and calls inside the orders service and the audit database that are not architecture.
func quietCode() ([]fn, []call) {
	fns, _ := codeGraph()
	fns = append(fns, fn{"aud2", "services/audit/b.go"})

	return fns, []call{
		calls("web1", "ord1"), calls("hdl1", "db1"), calls("ord1", "ord2"), calls("hdl1", "ord2"), calls("ord2", "hdl1"),
		calls("aud1", "aud2"),
	}
}

func projectionsOf(t *testing.T, v *views.Definition, extra ...call) (declared, observedProj *views.Projection) {
	t.Helper()

	fns, edges := quietCode()

	return views.BuildModelProjection(v, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()}),
		observed(t, v, codeEvidence(t, fns, append(edges, extra...), projectionBindings()))
}

func correspond(t *testing.T, v *views.Definition, evidence *views.DiagramEvidence) views.CorrespondenceResult {
	t.Helper()

	declared, obs := projectionsOf(t, v)

	return views.Correspond(&views.CorrespondenceInput{View: v, Declared: declared, Observed: obs, Evidence: evidence})
}

func kinds(r *views.CorrespondenceResult) []string {
	out := make([]string, 0, len(r.Differences))
	for i := range r.Differences {
		d := &r.Differences[i]
		out = append(out, strings.Join([]string{d.Kind, d.Subject, d.Basis, d.Key(), d.Detail}, "|"))
	}

	return out
}

func reasonsOf(r *views.CorrespondenceResult) []string {
	out := make([]string, 0, len(r.Unknown))
	for i := range r.Unknown {
		out = append(out, r.Unknown[i].Reason+"|"+r.Unknown[i].Subject)
	}

	return out
}

// correspondBlock compares the diagram of a block, mapped with the mapping, against the projections of its view.
func correspondBlock(t *testing.T, b *block, mapping map[string]string, against ...string) views.CorrespondenceResult {
	t.Helper()

	v := diagramView(mapping, against...)

	return correspond(t, &v, evidenceOf(t, []block{*b}))
}

// correspondStandardDiagram compares the standard diagram of a view with the two projections that the test built.
func correspondStandardDiagram(t *testing.T, v *views.Definition, declared, obs *views.Projection) views.CorrespondenceResult {
	t.Helper()

	return views.Correspond(&views.CorrespondenceInput{View: v, Declared: declared, Observed: obs, Evidence: evidenceOf(t, []block{standardDiagram()})})
}

// assertViolation checks that a result is a violation with the differences that it lists.
func assertViolation(t *testing.T, got *views.CorrespondenceResult, want ...string) {
	t.Helper()

	assertEq(t, "status", got.Status, views.StatusViolation)
	assertSliceEq(t, "differences", kinds(got), want)
}

// assertUnknown checks that a result is unknown with the reasons that it could not compare.
func assertUnknown(t *testing.T, got *views.CorrespondenceResult, want ...string) {
	t.Helper()

	assertEq(t, "status", got.Status, views.StatusUnknown)
	assertSliceEq(t, "reasons", reasonsOf(got), want)
}

// assertPasses checks that a result passes, with no difference and no reason that it could not compare.
func assertPasses(t *testing.T, got *views.CorrespondenceResult) {
	t.Helper()

	assertEq(t, "status", got.Status, views.StatusPass)
	assertSliceEq(t, "differences", kinds(got), []string{})
	assertSliceEq(t, "reasons", reasonsOf(got), []string{})
}

// assertNotPassing checks that a result does not pass and that it has a reason that it could not compare.
func assertNotPassing(t *testing.T, got *views.CorrespondenceResult, reason string) {
	t.Helper()

	assertHas(t, "reasons", reasonsOf(got), reason)

	if got.Status == views.StatusPass {
		t.Errorf("status = %s, want it not to pass", got.Status)
	}
}

func TestCorrespondenceMatchingDiagramPasses(t *testing.T) {
	v := diagramView(standardMapping)

	got := correspond(t, &v, evidenceOf(t, []block{standardDiagram()}))
	requireEq(t, "status", got.Status, views.StatusPass)
	requireEq(t, "method", got.Method, views.MethodStructuredSource)
	requireEq(t, "differences", len(got.Differences), 0)
	requireEq(t, "unknown", len(got.Unknown), 0)

	assertSliceEq(t, "compared", got.Compared, []string{"declared", "observed"})
	assertEq(t, "next action", got.NextAction, "")
	assertEq(t, "section", got.Section, "Payments-containers")
}

func TestCorrespondenceMissingRelationship(t *testing.T) {
	b := standardDiagram()
	b.arrows = []string{"customer>web", "orders>db:SQL", "orders>ledger:gRPC"}

	got := correspondBlock(t, &b, standardMapping, "declared")
	requireEq(t, "status", got.Status, views.StatusViolation)
	requireEq(t, "next action", got.NextAction, views.NextActionCorrectIntent)
	assertSliceEq(t, "differences", kinds(&got), []string{"missing|relationship|declared|web-ui>orders-service|"})
}

func TestCorrespondenceExtraRelationship(t *testing.T) {
	b := standardDiagram()
	b.arrows = append(b.arrows, "web>ledger")

	got := correspondBlock(t, &b, standardMapping)
	assertViolation(t, &got, "extra|relationship||web-ui>ledger|")

	loc := got.Differences[0].Locations
	requireEq(t, "locations", len(loc), 1)
	assertEq(t, "location path", loc[0].Path, diagramFile)

	if loc[0].Line == 0 {
		t.Errorf("locations = %+v, want a line", loc)
	}
}

func TestCorrespondenceIncorrectDirection(t *testing.T) {
	b := standardDiagram()
	b.arrows = []string{"customer>web", "orders>web:HTTPS", "orders>db:SQL", "orders>ledger:gRPC"}

	got := correspondBlock(t, &b, standardMapping, "declared")
	assertViolation(t, &got, "incorrect|relationship|declared|web-ui>orders-service|direction")
}

func TestCorrespondenceIncorrectProtocol(t *testing.T) {
	b := standardDiagram()
	b.arrows = []string{"customer>web", "web>orders:gRPC", "orders>db:SQL", "orders>ledger:gRPC"}

	got := correspondBlock(t, &b, standardMapping, "declared")
	assertSliceEq(t, "differences", kinds(&got), []string{"incorrect|relationship|declared|web-ui>orders-service|protocol gRPC HTTPS"})
}

func TestCorrespondenceDescriptionLabelIsNotAProtocol(t *testing.T) {
	b := standardDiagram()
	b.arrows = []string{"customer>web:browses", "web>orders:calls [HTTPS]", "orders>db:reads and writes", "orders>ledger:gRPC"}

	got := correspondBlock(t, &b, standardMapping, "declared")
	assertEq(t, "status", got.Status, views.StatusPass)
}

func TestCorrespondenceUnmappedElement(t *testing.T) {
	b := standardDiagram()
	b.elements = append(b.elements, "cache")
	b.arrows = append(b.arrows, "orders>cache")

	got := correspondBlock(t, &b, standardMapping)
	assertViolation(t, &got, "unmapped|element||cache|")
}

func TestCorrespondenceMissingAndExtraElement(t *testing.T) {
	b := standardDiagram()
	b.elements = []string{"customer", "web", "orders", "db", "ledger", "cache"}
	b.arrows = []string{"customer>web", "web>orders:HTTPS", "orders>db:SQL", "orders>ledger:gRPC"}

	mapping := cloneMapping(standardMapping)
	mapping["cache"] = "reporting-service"

	got := correspondBlock(t, &b, mapping, "declared")
	assertViolation(t, &got, "missing|element|declared|audit-db|", "extra|element||reporting-service|")
}

func cloneMapping(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}

	return out
}

// A16: the generated view was regenerated from the current model, so freshness holds; the model lacks a
// relationship that the observed graph has, so correspondence is a violation.
func TestCorrespondenceA16FreshRenderOfStaleIntent(t *testing.T) {
	v := containerView("payments-container")

	declared, obs := projectionsOf(t, &v, calls("web2", "db1"))
	in := &views.CorrespondenceInput{View: &v, Declared: declared, Observed: obs, Freshness: views.StatusPass}

	got := views.Correspond(in)
	requireEq(t, "status", got.Status, views.StatusViolation)
	requireSliceEq(t, "differences", kinds(&got), []string{"missing|relationship|observed|web-ui>payments-db|"})

	assertEq(t, "method", got.Method, views.MethodGeneratedSource)
	assertEq(t, "basis", got.Basis, views.CorrespondenceBasisCommitted)
	assertSliceEq(t, "by construction", got.ByConstruction, []string{"declared"})

	in.Freshness = views.StatusViolation
	got = views.Correspond(in)
	assertEq(t, "basis", got.Basis, views.CorrespondenceBasisRegenerated)

	declaredOnly := containerView("payments-container")
	declaredOnly.Comparison = &views.Comparison{Against: []string{"declared"}}

	in.View = &declaredOnly
	got = views.Correspond(in)
	assertEq(t, "against declared: status", got.Status, views.StatusPass)
}

func TestCorrespondenceGeneratedViewWithoutDrift(t *testing.T) {
	v := containerView("payments-container")

	declared, obs := projectionsOf(t, &v)

	got := views.Correspond(&views.CorrespondenceInput{View: &v, Declared: declared, Observed: obs, Freshness: views.StatusPass})
	assertEq(t, "status", got.Status, views.StatusPass)
}

// belowContainerLevel says whether a row of the observed projection is a call inside one container, or one that
// involves a component: no row of a projection at the container level is.
func belowContainerLevel(row *views.ProjectionRelationship) bool {
	if row.From == row.To {
		return true
	}

	return slices.Contains([]string{row.From, row.To}, "orders-handler")
}

// A17: the diagram shows containers, the observed graph has calls between the functions of one container, and
// the mapped container arrows match.
func TestCorrespondenceA17MethodsAreNotDrift(t *testing.T) {
	v := diagramView(standardMapping)

	_, obs := projectionsOf(t, &v)
	for i := range obs.Relationships {
		if belowContainerLevel(&obs.Relationships[i]) {
			t.Fatalf("the observed projection is not at the container level: %+v", obs.Relationships[i])
		}
	}

	got := correspond(t, &v, evidenceOf(t, []block{standardDiagram()}))
	assertEq(t, "status", got.Status, views.StatusPass)
	assertEq(t, "differences", len(got.Differences), 0)
}

func TestCorrespondenceDuplicateSourceIdentifier(t *testing.T) {
	b := standardDiagram()
	b.elements = append(b.elements, "orders")

	got := correspondBlock(t, &b, standardMapping, "declared")
	requireEq(t, "status", got.Status, views.StatusUnknown)
	requireEq(t, "next action", got.NextAction, views.NextActionSupplyEvidence)

	reason := got.Unknown[0]
	requireEq(t, "reason", reason.Reason, "ambiguous mapping")
	requireEq(t, "reason subject", reason.Subject, "orders")
	requireEq(t, "reason locations", len(reason.Locations), 2)

	assertSliceEq(t, "an ambiguous element has no difference", kinds(&got), []string{})
}

func TestCorrespondenceLabelFallback(t *testing.T) {
	labels := []string{"n1=Customer", "n2=Web UI", "n3=Orders", "n4=Payments DB", "n5=Ledger", "n6=Audit DB"}
	arrows := []string{"n1>n2", "n2>n3:HTTPS", "n3>n4:SQL", "n3>n5:gRPC"}
	mapping := map[string]string{
		"Customer": "customer", "Web UI": "web-ui", "Orders": "orders-service", "Payments DB": "payments-db",
		"Ledger": "ledger", "Audit DB": "audit-db",
	}

	b := standardDiagram()
	b.elements, b.arrows = labels, arrows

	got := correspondBlock(t, &b, mapping)
	assertPasses(t, &got)

	// A label that two elements share is never a key.
	b.elements = []string{"n1=Customer", "n2=Web UI", "n3=Orders", "n4=Payments DB", "n5=Ledger", "n6=Orders"}

	got = correspondBlock(t, &b, mapping)
	assertNotPassing(t, &got, "ambiguous mapping key|Orders")
}

func TestCorrespondenceElementIdentifierWinsOverLabel(t *testing.T) {
	b := standardDiagram()
	// `web` has the label `orders`, which is also the identifier of the orders node: the key `orders` is the node.
	b.elements = []string{"customer", "web=orders", "orders", "db", "ledger", "audit"}

	got := correspondBlock(t, &b, standardMapping)
	assertPasses(t, &got)
}

func TestCorrespondenceMappingGap(t *testing.T) {
	b := standardDiagram()
	b.elements = []string{"customer", "web", "orders", "db", "ledger"}

	mapping := cloneMapping(standardMapping)
	delete(mapping, "audit")

	got := correspondBlock(t, &b, mapping)
	assertUnknown(t, &got, "mapping gap|audit-db")
}

func TestCorrespondenceBindingRemoved(t *testing.T) {
	v := diagramView(standardMapping)

	fns, edges := quietCode()
	without := slices.DeleteFunc(projectionBindings(), func(b binding.Binding) bool {
		return b.ArchitectureRef == "orders-service" || b.ArchitectureRef == "orders-handler"
	})
	declared := views.BuildModelProjection(&v, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()})
	obs := views.ObserveProjection(&v, &views.ObserveInput{Reconcile: codeEvidence(t, fns, edges, without), Bindings: without})

	got := correspondStandardDiagram(t, &v, declared, obs)
	requireEq(t, "status", got.Status, views.StatusUnknown)
	requireHas(t, "reasons", reasonsOf(&got), "no binding for necessary element|orders-service")

	assertEq(t, "next action", got.NextAction, conformance.NextActionResolveBinding)
	assertHas(t, "lost", got.Lost, views.LostCoverage{Kind: views.LostBinding, Subject: "orders-service"})
}

func TestCorrespondenceEdgeFamilyLosesSupport(t *testing.T) {
	v := diagramView(standardMapping)
	v.Projection = &views.ProjectionRules{EdgeKinds: []string{"imports", "calls"}}

	fns, edges := quietCode()
	in := codeEvidence(t, fns, edges, projectionBindings())
	in.SupportedCanonicalKinds = []observation.CanonicalEdgeKind{observation.KindImports}

	declared := views.BuildModelProjection(&v, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()})
	obs := views.ObserveProjection(&v, &views.ObserveInput{Reconcile: in, Bindings: projectionBindings()})

	got := correspondStandardDiagram(t, &v, declared, obs)
	assertNotPassing(t, &got, "coverage lost|calls")
	assertHas(t, "lost", got.Lost, views.LostCoverage{Kind: views.LostEdgeKind, Subject: "calls"})

	if got.NextAction == "" {
		t.Errorf("next action = %q, want one", got.NextAction)
	}
}

func TestCorrespondenceAmbiguousEntityAndUnboundEvidence(t *testing.T) {
	v := diagramView(standardMapping)
	declared, obs := projectionsOf(t, &v)

	obs.Ambiguous = []views.AmbiguousEntity{{Entity: "pkg.shared", Candidates: []string{"orders-service", "web-ui"}}}
	obs.Coverage.Unbound = &views.UnboundCoverage{Class: views.UnboundSome, Elements: []string{"payments-db", "reporting-service"}}

	got := correspondStandardDiagram(t, &v, declared, obs)
	assertUnknown(t, &got, "ambiguous binding|pkg.shared", "unbound evidence for necessary element|payments-db")
	assertEq(t, "next action", got.NextAction, conformance.NextActionResolveBinding)
	assertSliceEq(t, "candidates", got.Unknown[0].Candidates, []string{"orders-service", "web-ui"})
}

func TestCorrespondenceObservedProjectionUnavailable(t *testing.T) {
	v := diagramView(standardMapping)
	declared, _ := projectionsOf(t, &v)

	got := views.Correspond(&views.CorrespondenceInput{View: &v, Declared: declared, Evidence: evidenceOf(t, []block{standardDiagram()})})
	assertUnknown(t, &got, "observed projection not available|payments-diagram")
	assertEq(t, "next action", got.NextAction, views.NextActionFixAnalysis)
	assertSliceEq(t, "compared", got.Compared, []string{"declared"})
}

func TestCorrespondenceStructureNotAvailable(t *testing.T) {
	b := standardDiagram()
	b.arrows = nil

	v := diagramView(standardMapping)

	got := correspond(t, &v, evidenceOf(t, []block{b}))
	assertUnknown(t, &got, "structure not available|"+diagramFile)
	assertEq(t, "next action", got.NextAction, views.NextActionSupplyEvidence)

	none := views.Correspond(&views.CorrespondenceInput{View: &v})
	assertEq(t, "no evidence: status", none.Status, views.StatusUnknown)
}

// rasterView is an authored container view whose source is an image.
func imageView(depicts *views.Depicts) views.Definition {
	v := containerView("payments-container-image")
	v.Origin, v.Output = views.OriginAuthored, nil
	v.Source = &views.Source{Path: "docs/context.png", Format: "png"}

	if depicts != nil {
		v.Mapping = &views.Mapping{Depicts: depicts}
	}

	return v
}

// A19: an image with no structured source and no supplemental mapping has no automated pass.
func TestCorrespondenceA19ImageOnlyDiagram(t *testing.T) {
	v := imageView(nil)

	declared, obs := projectionsOf(t, &v)

	got := views.Correspond(&views.CorrespondenceInput{View: &v, Declared: declared, Observed: obs, ImageDigest: imageDigest})
	requireEq(t, "status", got.Status, views.StatusUnknown)
	requireEq(t, "method", got.Method, views.MethodPresenceOnly)
	requireEq(t, "next action", got.NextAction, views.NextActionSupplyEvidence)

	assertEq(t, "source path", got.Source.Path, "docs/context.png")
	assertEq(t, "compared", len(got.Compared), 0)
	assertEq(t, "differences", len(got.Differences), 0)
}

func depictsOf(digest string, elements, relationships []string) *views.Depicts {
	return &views.Depicts{Digest: digest, Elements: elements, Relationships: relationships}
}

func supplemental(t *testing.T, depicts *views.Depicts, digest string) views.CorrespondenceResult {
	t.Helper()

	v := imageView(depicts)
	v.Comparison = &views.Comparison{Against: []string{"declared"}}

	declared, _ := projectionsOf(t, &v)

	return views.Correspond(&views.CorrespondenceInput{
		View: &v, Declared: declared, Model: projectionModel("HTTPS"), Bindings: projectionBindings(), ImageDigest: digest,
	})
}

var (
	contextElements = []string{"customer", "web-ui", "orders-service", "payments-db", "audit-db", "ledger"}
	allRelationship = []string{"customer-uses-web", "web-to-orders", "orders-to-db", "orders-to-ledger"}
)

func TestCorrespondenceSupplementalMapping(t *testing.T) {
	full := supplemental(t, depictsOf(imageDigest, contextElements, allRelationship), imageDigest)
	requireEq(t, "full claim: status", full.Status, views.StatusPass)
	requireEq(t, "full claim: method", full.Method, views.MethodSupplementalMapping)
	requireSliceEq(t, "full claim: compared", full.Compared, []string{"declared"})

	// A claim that leaves out a declared relationship, or a necessary element, misses it.
	partial := supplemental(t, depictsOf(imageDigest, []string{"customer", "web-ui", "orders-service", "payments-db", "ledger"}, allRelationship[:3]), imageDigest)

	assertViolation(t, &partial, "missing|element|declared|audit-db|", "missing|relationship|declared|orders-service>ledger|")
	assertEq(t, "partial claim: method", partial.Method, views.MethodSupplementalMapping)
}

func TestCorrespondenceImageReplaced(t *testing.T) {
	got := supplemental(t, depictsOf(otherDigest, contextElements, nil), imageDigest)
	assertUnknown(t, &got, "supplemental mapping stale|docs/context.png")
	assertEq(t, "method", got.Method, views.MethodSupplementalMapping)
	assertEq(t, "compared", len(got.Compared), 0)

	got = supplemental(t, depictsOf(otherDigest, contextElements, nil), "")
	assertSliceEq(t, "no digest: reasons", reasonsOf(&got), []string{"image digest not available|docs/context.png"})
}

func TestCorrespondenceDepictedRelationshipNotInModel(t *testing.T) {
	got := supplemental(t, depictsOf(imageDigest, contextElements, []string{"customer-uses-web", "nope"}), imageDigest)
	assertNotPassing(t, &got, "depicted relationship not in model|nope")
}

func TestCorrespondenceNeverEmitsReviewed(t *testing.T) {
	for _, method := range []string{views.MethodGeneratedSource, views.MethodStructuredSource, views.MethodSupplementalMapping, views.MethodPresenceOnly} {
		if method == views.MethodReviewed {
			t.Errorf("method %q is reserved", method)
		}
	}
}

func TestCorrespondenceResultIsSorted(t *testing.T) {
	b := standardDiagram()
	b.arrows = append(b.arrows, "web>ledger", "orders>customer")

	got := correspondBlock(t, &b, standardMapping, "declared")

	order := kinds(&got)
	if !slices.IsSorted(order) {
		t.Errorf("differences = %v, want them sorted", order)
	}

	requireEq(t, "differences", len(order), 2)

	if !strings.HasPrefix(order[0], "extra|relationship||orders-service>customer") {
		t.Errorf("differences = %v", order)
	}

	if !slices.IsSorted(got.Reasons) {
		t.Errorf("reasons = %v, want them sorted", got.Reasons)
	}

	assertHas(t, "reasons", got.Reasons, "extra relationship")
}
