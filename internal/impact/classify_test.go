package impact_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

func change(op impact.ChangeKind, kind impact.Kind, identity string) impact.Change {
	return impact.Change{Op: op, Kind: kind, Identity: identity}
}

// assertClass checks the class of an addition and of a modification; removals are the removal class.
func assertClass(t *testing.T, want impact.Class, kind impact.Kind) {
	t.Helper()

	for _, op := range []impact.ChangeKind{impact.ChangeAdded, impact.ChangeModified} {
		if got := impact.ClassifyChange(change(op, kind, "x")); got != want {
			t.Errorf("%s of %s: class = %s, want %s", op, kind, got, want)
		}
	}

	if got := impact.ClassifyChange(change(impact.ChangeRemoved, kind, "x")); got != impact.ClassRemoval {
		t.Errorf("removal of %s: class = %s, want %s", kind, got, impact.ClassRemoval)
	}
}

// Scenario: A21 new public route.
func TestClassExposure(t *testing.T) {
	assertClass(t, impact.ClassExposure, impact.KindPublicEntryPoint)
}

// Scenario: A21 new public route (a new workload identity touches the dependencies).
// Scenario: Identity and role change.
func TestClassIdentity(t *testing.T) {
	assertClass(t, impact.ClassIdentity, impact.KindRuntimeIdentity)
	assertClass(t, impact.ClassIdentity, impact.KindRoleBinding)
}

// Scenario: New boundary-crossing flow.
func TestClassBoundaryCrossingFlow(t *testing.T) {
	for _, kind := range []impact.Kind{
		impact.KindExternalDestination, impact.KindTrustContext, impact.KindDataClassification, impact.KindBinding,
	} {
		assertClass(t, impact.ClassBoundary, kind)
	}
}

// Scenario: Secret reference changes.
func TestClassSensitiveReference(t *testing.T) {
	for _, kind := range []impact.Kind{
		impact.KindSecretReference, impact.KindConfigurationReference, impact.KindStorageReference,
	} {
		assertClass(t, impact.ClassSensitiveReference, kind)
	}
}

// Scenario: Protocol or certificate change.
func TestClassProtection(t *testing.T) {
	for _, kind := range []impact.Kind{impact.KindProtocol, impact.KindCertificateReference, impact.KindControl} {
		assertClass(t, impact.ClassProtection, kind)
	}
}

// Scenario: Environment or hosting changes.
func TestClassEnvironmentOrHosting(t *testing.T) {
	assertClass(t, impact.ClassEnvironment, impact.KindPlacement)
}

// Scenario: Implementation or verification change.
func TestClassImplementationOrVerification(t *testing.T) {
	for _, kind := range []impact.Kind{impact.KindImplementation, impact.KindVerification, impact.KindQualityScenario} {
		assertClass(t, impact.ClassImplementation, kind)
	}
}

// Scenario: Removal is classified. A removed mandatory control is a removal, not a protection change.
func TestClassRemovalAndCapabilityLoss(t *testing.T) {
	got := impact.ClassifyChange(change(impact.ChangeRemoved, impact.KindControl, "control:mtls"))
	checkEq(t, "class", got, impact.ClassRemoval)

	regression := impact.CapabilityRegressions(
		impact.CapabilityProfile{ID: "p2", Kinds: []impact.Kind{impact.KindPublicEntryPoint}},
		[]impact.Use{{Assessment: "ASM-1@1", Kind: impact.KindCertificateReference, Identity: "tls"}},
	)
	if len(regression) != 1 {
		t.Fatalf("capability loss regressions = %+v, want 1", regression)
	}

	trigger := regression[0].Trigger()
	checkEq(t, "capability loss trigger class", trigger.Class, impact.ClassRemoval)
	checkEq(t, "capability loss trigger change", trigger.Change, impact.ChangeRemoved)
}

// classesProduced gives the classes that some kind maps to, plus the removal class. It reports each kind
// that has no class.
func classesProduced(t *testing.T) map[impact.Class]bool {
	t.Helper()

	used := map[impact.Class]bool{impact.ClassRemoval: true}

	for _, kind := range impact.Kinds() {
		class, ok := impact.ClassOfKind(kind)
		checkTrue(t, ok, "kind "+string(kind)+" has no class")

		used[class] = true
	}

	return used
}

func TestEveryKindHasAClassAndEveryClassIsUsed(t *testing.T) {
	used := classesProduced(t)

	for _, class := range impact.Classes() {
		checkTrue(t, used[class], "class "+string(class)+" is never produced")
		checkTrue(t, class.Label() != string(class), "class "+string(class)+" has no label")
	}

	checkEq(t, "classes", len(impact.Classes()), 8)
}

// assertTriggerTextWording checks, for every class and change, that the trigger text asks for review
// and never claims a verdict.
func assertTriggerTextWording(t *testing.T) {
	t.Helper()

	for _, class := range impact.Classes() {
		for _, op := range []impact.ChangeKind{impact.ChangeAdded, impact.ChangeRemoved, impact.ChangeModified} {
			text := strings.ToLower(impact.ReviewTrigger{
				Class: class, Change: op, Dependency: "x:y", Assessment: "A@1", Action: impact.ActionGetReview,
			}.Text())

			for _, banned := range []string{"vulnerable", "compliant"} {
				checkTrue(t, !strings.Contains(text, banned), "trigger text "+text+" contains "+banned)
			}

			for _, wanted := range []string{"get review", "x:y", "a@1"} {
				checkTrue(t, strings.Contains(text, wanted), "trigger text "+text+" lacks "+wanted)
			}
		}
	}
}

// Scenario: Trigger wording. The trigger names the dependency, the class, the assessment, and the
// action `get review`; no text says "vulnerable" or "compliant".
func TestTriggerTextAsksForReview(t *testing.T) {
	g := impact.NewGraph()
	route := impact.ItemNodeID(impact.KindPublicEntryPoint, "prod/ingress/web")
	assumption := impact.NodeID{Identity: "ASU-1", Version: "1"}
	assessment := impact.NodeID{Identity: "ASM-EDGE", Version: "2"}

	g.AddNode(assumption, impact.NodeAssumption)
	g.AddNode(assessment, impact.NodeAssessment)
	g.AddDependency(assumption, route, impact.EdgeDependsOn)
	g.AddDependency(assessment, assumption, impact.EdgeDependsOn)

	result := g.Traverse([]impact.Seed{{Node: route, Op: impact.ChangeAdded}}, impact.Options{})
	trigger := triggerFor(t, &result, assessment)
	got := impact.NewReviewTrigger(trigger, g)

	checkEq(t, "class", got.Class, impact.ClassExposure)
	checkEq(t, "change", got.Change, impact.ChangeAdded)
	checkEq(t, "dependency", got.Dependency, "public-entry-point:prod/ingress/web")
	checkEq(t, "assessment", got.Assessment, "ASM-EDGE@2")
	checkEq(t, "action", got.Action, "get review")
	checkSlice(t, "assumption", got.Assumption, []string{"ASU-1@1"})
	checkSlice(t, "path", got.Path, []string{"public-entry-point:prod/ingress/web", "ASU-1@1", "ASM-EDGE@2"})

	assertTriggerTextWording(t)
}

func TestReviewTriggerKeepsRelatedChanges(t *testing.T) {
	g := impact.NewGraph()
	a := impact.ItemNodeID(impact.KindSecretReference, "db")
	b := impact.ItemNodeID(impact.KindProtocol, "tls")
	assessment := impact.NodeID{Identity: "ASM-1", Version: "1"}

	g.AddNode(assessment, impact.NodeAssessment)
	g.AddDependency(assessment, a, impact.EdgeDependsOn)
	g.AddDependency(assessment, b, impact.EdgeDependsOn)

	result := g.Traverse([]impact.Seed{{Node: a, Op: impact.ChangeModified}, {Node: b, Op: impact.ChangeRemoved}}, impact.Options{})
	got := impact.NewReviewTrigger(triggerFor(t, &result, assessment), g)

	if len(got.Related) != 1 {
		t.Fatalf("related = %+v, primary = %s", got.Related, got.Dependency)
	}

	related := got.Related[0]
	checkTrue(t, related.Class != got.Class || related.Dependency != got.Dependency,
		"related change repeats the primary: "+related.Dependency)

	classes := []impact.Class{got.Class, related.Class}
	checkTrue(t, slices.Contains(classes, impact.ClassSensitiveReference), "classes lack the sensitive reference")
	checkTrue(t, slices.Contains(classes, impact.ClassRemoval), "classes lack the removal")
}

func TestChangeOfSeedRoundTrip(t *testing.T) {
	seed := impact.Seed{Node: impact.ItemNodeID(impact.KindPlacement, "prod/web"), Op: impact.ChangeModified}

	got, ok := impact.ChangeOfSeed(seed)
	checkTrue(t, ok, "an item seed has a change")
	checkEq(t, "kind", got.Kind, impact.KindPlacement)
	checkEq(t, "identity", got.Identity, "prod/web")
	checkEq(t, "op", got.Op, impact.ChangeModified)

	_, ok = impact.ChangeOfSeed(impact.Seed{Node: impact.NodeID{Identity: "ASM-1", Version: "1"}})
	checkTrue(t, !ok, "an assessment node is not an item seed")
}
