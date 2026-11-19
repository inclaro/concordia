package conformance_test

import (
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// The typed-obligation world: CALM services `billing` and `ledger`, the container `ns-prod`, and observed
// workloads, namespaces, and facts that bind to them.

var typedSeq atomic.Int64

func typedModel(relationships ...intent.RelationshipDeclaration) *intent.ArchitectureModel {
	ns := node("ns-prod")
	ns.NodeType = "system"

	billing, ledger := node("billing"), node("ledger")
	billing.Tags = []string{"deployable"}
	ledger.Tags = []string{"deployable"}

	return &intent.ArchitectureModel{
		SourceURI: "architecture/a.calm.json", Digest: "sha256:0", SchemaVersion: "1.2",
		Nodes: []intent.NodeDeclaration{billing, ledger, ns}, Relationships: relationships,
		Controls: []intent.ControlAttachment{}, Metadata: map[string]any{},
	}
}

// resource makes a Kubernetes resource node. The observed ID is `cbm:p:<id>`.
func resource(id, kind, env, name string) observation.ObservedNode {
	return observation.ObservedNode{
		ObservedID: "cbm:p:" + id, Label: "Resource", Kind: kind, QualifiedName: "k8s." + name,
		FilePath: "k8s/" + env + "/" + name + ".yaml", StartLine: 3, Environment: env,
		ScopedID: "k8s:" + env + "/c1/apps/" + kind + "/payments/" + name,
	}
}

func fact(kind observation.CanonicalEdgeKind, from, to string, edit func(*observation.EvidenceEdge)) *observation.EvidenceEdge {
	n := typedSeq.Add(1)
	e := &observation.EvidenceEdge{
		ID: "t" + strconv.FormatInt(n, 10), From: "cbm:p:" + from, To: "cbm:p:" + to, Kind: kind, NativeKind: "X",
		Confidence: observation.ConfidenceSyntactic, Role: observation.RoleParsedSourceFact, Resolution: observation.ResolutionExplicitlyBound,
		ResolutionMethod: "cbm:" + string(kind), Properties: map[string]string{}, Facets: map[string]string{},
		SourceSpan: &observation.SourceSpan{Path: "k8s/prod/" + from + ".yaml", Line: int(n)},
		Analyzer:   observation.AnalyzerDescriptor{Name: "t", Version: "0", AdapterSchema: "concordia-cbm/v2"}, GraphGeneration: "g",
	}

	if edit != nil {
		edit(e)
	}

	return e
}

// records makes a coverage record of the family of each kind in the project and in both environments.
func records(status observation.CoverageStatus, kinds ...observation.CanonicalEdgeKind) []observation.PredicateCoverage {
	out := make([]observation.PredicateCoverage, 0, 3*len(kinds))

	for _, kind := range kinds {
		family, _ := observation.FamilyForKind(kind)

		for _, scope := range []observation.CoverageScope{{Project: "p"}, {Environment: "production"}, {Environment: "staging"}} {
			rec := observation.PredicateCoverage{Predicate: family, Scope: scope, Status: status, LossModes: []observation.LossMode{}, Reasons: []string{}}
			if status != observation.CoverageComplete {
				rec.Reasons = []string{"test: " + string(status)}
			}

			out = append(out, rec)
		}
	}

	return out
}

type world struct { //nolint:govet // a test fixture: the fields are grouped by meaning.
	nodes  map[string]observation.ObservedNode
	bound  map[string][]string
	set    binding.Set
	edges  []*observation.EvidenceEdge
	scope  conformance.ScopeContext
	typed  conformance.TypedContext
	model  *intent.ArchitectureModel
	plans  []policy.RulePlan
	loaded *binding.Loaded
	// enforcement is the workspace enforcement setting of the evaluation (zero: the rule's own mode, then defaults).
	enforcement conformance.EnforcementConfig
}

// newWorld has the environments production and staging, complete coverage of the kinds, and no entity.
func newWorld(kinds ...observation.CanonicalEdgeKind) *world {
	w := &world{nodes: map[string]observation.ObservedNode{}, bound: map[string][]string{}, model: typedModel(), loaded: &binding.Loaded{}}
	w.scope = conformance.ScopeContext{Projects: []string{"p"}, Predicates: records(observation.CoverageComplete, kinds...)}
	w.typed = conformance.TypedContext{
		Workspace: &workspace.Workspace{Environments: []workspace.Environment{
			{ID: "production", Sources: []string{"k8s/production/**"}}, {ID: "staging", Sources: []string{"k8s/staging/**"}},
		}},
		Bindings: w.loaded,
	}

	return w
}

// add puts a node in the graph and binds it to the CALM IDs.
func (w *world) add(n observation.ObservedNode, calm ...string) { //nolint:gocritic // a test helper; the node is a small literal.
	w.nodes[n.ObservedID] = n

	if len(calm) > 0 {
		w.bound[n.ObservedID] = calm
	}
}

func (w *world) evaluate() conformance.EvaluateOutput {
	w.set.ByObserved = w.bound

	if w.set.MatchCounts == nil {
		w.set.MatchCounts = map[string]int{}
	}

	return conformance.Evaluate(&conformance.EvaluateInput{
		Plans: w.plans, Snapshot: conformance.Snapshot{Project: "p", GraphGeneration: "g"}, Scope: w.scope, Typed: w.typed, Enforcement: w.enforcement,
		ReconcileInput: conformance.ReconcileInput{
			Model: w.model, BindingSet: w.set, Evidence: conformance.EvidenceSet{Nodes: w.nodes, Edges: w.edges},
			BindingRootsByNode: map[string][]string{},
		},
	})
}

// typedRule makes a typed plan with the compiled defaults of the obligation block.
func typedRule(id string, subjects []string, typ policy.ObligationType, kind observation.CanonicalEdgeKind, dir policy.RelationDirection, edit func(*policy.ObligationSpec)) policy.RulePlan {
	spec := &policy.ObligationSpec{
		Type: typ, Multiplicity: policy.DefaultMultiplicity, MinimumResolution: policy.DefaultMinimumResolution,
		Completeness: policy.DefaultCompleteness, Roles: policy.DefaultEvidenceRoles(),
		Relation: policy.RelationSpec{Kind: kind, Direction: dir, Target: []string{}},
		Scope:    policy.ObligationScope{Environments: []string{"production"}, Repositories: []string{}},
	}

	if edit != nil {
		edit(spec)
	}

	return plan(policy.Predicate{Family: policy.FamilyTyped}, func(p *policy.RulePlan) {
		p.RuleID, p.Subjects, p.Targets, p.Obligation = id, subjects, spec.Relation.Target, spec
		p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{kind}
		p.Enforcement = policy.EnforcementMandatory
	})
}

func typedRuleResult(t *testing.T, out *conformance.EvaluateOutput, id string) *conformance.RuleResult {
	t.Helper()

	for i := range out.RuleResults {
		if out.RuleResults[i].RuleID == id {
			return &out.RuleResults[i]
		}
	}

	t.Fatalf("no result for rule %s in %+v", id, out.RuleResults)

	return nil
}

func obligationOf(t *testing.T, rule *conformance.RuleResult, subject, env string) *conformance.ObligationResult {
	t.Helper()

	for i := range rule.Obligations {
		if o := &rule.Obligations[i]; o.Subject == subject && o.Environment == env {
			return o
		}
	}

	t.Fatalf("no obligation for %s in %q among %+v", subject, env, rule.Obligations)

	return nil
}

func countResults(o *conformance.ObligationResult, result conformance.RuleVerdict) int {
	n := 0

	for i := range o.Occurrences {
		if o.Occurrences[i].Result == result {
			n++
		}
	}

	return n
}

func occurrenceOfClass(t *testing.T, o *conformance.ObligationResult, class conformance.OccurrenceClass) *conformance.OccurrenceResult {
	t.Helper()

	for i := range o.Occurrences {
		if o.Occurrences[i].Class == class {
			return &o.Occurrences[i]
		}
	}

	t.Fatalf("no %s occurrence in %+v", class, o.Occurrences)

	return nil
}

// usesImageRule makes the rule T-1: each workload of billing uses an image.
func usesImageRule(edit func(*policy.ObligationSpec)) policy.RulePlan {
	return typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, edit)
}

// allEnvironmentsRule makes a required uses-image rule that is not limited to an environment.
func allEnvironmentsRule(id string, subjects ...string) policy.RulePlan {
	return typedRule(id, subjects, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload,
		func(s *policy.ObligationSpec) { s.Scope.Environments = []string{} })
}

// billingImageNode is the image that the workloads of billing use.
func billingImageNode() observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing"}
}

// moduleNode makes a source module of a service: the file is services/<service>/<id>.ts.
func moduleNode(id, service string) observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:p:" + id, Label: "Module", QualifiedName: id, FilePath: "services/" + service + "/" + id + ".ts"}
}

// projectCoverage is a complete coverage record of a predicate in the project.
func projectCoverage(predicate string) observation.PredicateCoverage {
	return observation.PredicateCoverage{
		Predicate: predicate, Scope: observation.CoverageScope{Project: "p"}, Status: observation.CoverageComplete,
		LossModes: []observation.LossMode{}, Reasons: []string{},
	}
}

// serviceModuleWorld has the module a of billing and the module b of ledger, no declared environment, and complete
// coverage of the predicates in the project.
func serviceModuleWorld(predicates ...string) *world {
	w := newWorld()
	w.typed.Workspace = nil
	w.scope.Predicates = nil

	for _, predicate := range predicates {
		w.scope.Predicates = append(w.scope.Predicates, projectCoverage(predicate))
	}

	w.add(moduleNode("a", "billing"), "billing")
	w.add(moduleNode("b", "ledger"), "ledger")

	return w
}

// addImageFact adds a uses-image fact in production from the workload to the image; edit may change it.
func (w *world) addImageFact(from string, edit func(*observation.EvidenceEdge)) {
	w.edges = append(w.edges, fact(observation.KindUsesImage, from, "img", func(e *observation.EvidenceEdge) {
		e.Environment = "production"

		if edit != nil {
			edit(e)
		}
	}))
}

// billingObligation evaluates the world and gives the obligation of the rule T-1 for billing in the environment.
func (w *world) billingObligation(t *testing.T, env string) *conformance.ObligationResult {
	t.Helper()

	out := w.evaluate()

	return obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", env)
}

// requireVerdict requires the verdict of an obligation.
func requireVerdict(t *testing.T, o *conformance.ObligationResult, want conformance.RuleVerdict) {
	t.Helper()

	if o.Verdict != want {
		t.Fatalf("verdict %s, want %s (%s)", o.Verdict, want, o.Detail)
	}
}

// requireUnknown requires that an obligation is unknown with the reason.
func requireUnknown(t *testing.T, o *conformance.ObligationResult, reason conformance.UnknownReason) {
	t.Helper()

	if o.Verdict != conformance.VerdictUnknown || o.Reason != reason {
		t.Fatalf("verdict %s reason %q, want unknown %q (%s)", o.Verdict, o.Reason, reason, o.Detail)
	}
}

// ---- 10.3: BuildObligations ----

// isStableObligationID says whether two builds of an obligation gave the same 16 lowercase hex characters.
func isStableObligationID(id, again string) bool {
	if id != again || len(id) != 16 {
		return false
	}

	return strings.Trim(id, "0123456789abcdef") == ""
}

func TestBuildObligationsAreStableAndOrdered(t *testing.T) {
	t.Parallel()

	rule := allEnvironmentsRule("T-1", "ledger", "billing")
	w := newWorld()

	first := conformance.BuildObligations(&rule, w.model, w.typed.Workspace, w.loaded, nil)
	second := conformance.BuildObligations(&rule, w.model, w.typed.Workspace, w.loaded, nil)

	if len(first) != 4 {
		t.Fatalf("obligations = %d, want 2 subjects x 2 environments", len(first))
	}

	order := make([]string, 0, len(first))

	for i := range first {
		if !isStableObligationID(first[i].ID, second[i].ID) {
			t.Fatalf("id %q is not stable 16 hex characters: %q", first[i].ID, second[i].ID)
		}

		order = append(order, first[i].Subject+"/"+first[i].Environment)
	}

	if want := []string{"billing/production", "billing/staging", "ledger/production", "ledger/staging"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}

	other := allEnvironmentsRule("T-2", "billing")
	if got := conformance.BuildObligations(&other, w.model, w.typed.Workspace, w.loaded, nil); got[0].ID == first[0].ID {
		t.Fatal("two rules gave one obligation id")
	}
}

func TestBuildObligationsDropsExcludedEnvironments(t *testing.T) {
	t.Parallel()

	rule := allEnvironmentsRule("T-1", "billing")
	w := newWorld(observation.KindUsesImage)

	selected := &conformance.RunEnvironments{Selected: []string{"production"}, Excluded: []string{"staging"}}
	got := conformance.BuildObligations(&rule, w.model, w.typed.Workspace, w.loaded, selected)

	if len(got) != 1 || got[0].Environment != "production" {
		t.Fatalf("obligations = %+v, want production only", got)
	}

	w.plans = []policy.RulePlan{rule}
	w.typed.Environments = selected

	out := w.evaluate()
	if obs := typedRuleResult(t, &out, "T-1").Obligations; len(obs) != 1 {
		t.Fatalf("the excluded environment has a verdict: %+v", obs)
	}
}

func TestBuildObligationsWithoutDeclaredEnvironments(t *testing.T) {
	t.Parallel()

	rule := allEnvironmentsRule("T-1", "billing")

	got := conformance.BuildObligations(&rule, typedModel(), &workspace.Workspace{}, nil, nil)
	if len(got) != 1 || got[0].Environment != "" {
		t.Fatalf("obligations = %+v, want one with no environment", got)
	}
}

func TestEmptyGraphKeepsEveryObligation(t *testing.T) { // POL-03
	t.Parallel()

	for _, tc := range []struct {
		name    string
		status  observation.CoverageStatus
		verdict conformance.RuleVerdict
		reason  conformance.UnknownReason
	}{
		{"complete coverage", observation.CoverageComplete, conformance.VerdictViolation, ""},
		{"partial coverage", observation.CoveragePartial, conformance.VerdictUnknown, conformance.ReasonCoverageIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld()
			w.scope.Predicates = records(tc.status, observation.KindUsesImage)
			w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing", "ledger"}, policy.ObligationRequired, observation.KindUsesImage,
				policy.DirectionFromWorkload, nil)}

			out := w.evaluate()
			rule := typedRuleResult(t, &out, "T-1")

			if len(rule.Obligations) != 2 {
				t.Fatalf("obligations = %d, want one for each service", len(rule.Obligations))
			}

			for _, subject := range []string{"billing", "ledger"} {
				o := obligationOf(t, rule, subject, "production")
				if o.Verdict != tc.verdict || o.Reason != tc.reason {
					t.Fatalf("%s: verdict %s reason %q, want %s %q (%s)", subject, o.Verdict, o.Reason, tc.verdict, tc.reason, o.Detail)
				}
			}

			if tc.verdict == conformance.VerdictViolation && len(out.Findings) != 2 {
				t.Fatalf("findings = %d, want 2", len(out.Findings))
			}
		})
	}
}

// isFactOfTheWorkload says whether the only occurrence is the fact of the workload, and not an absence.
func isFactOfTheWorkload(occurrences []conformance.OccurrenceResult) bool {
	if len(occurrences) != 1 || occurrences[0].Class == conformance.OccurrenceAbsent {
		return false
	}

	return occurrences[0].Anchor == "cbm:p:wl"
}

// The adapter reads the provider `kind` only for nodes that are the target of a reference edge: a workload that
// nothing references has no Kind, and its obj_key still says Deployment. A `*-workload` direction takes it.
func TestWorkloadWithoutProviderKindIsAWorkloadByItsObjKey(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	worker := resource("wl", "", "production", "worker")
	worker.Properties = map[string]string{"obj_key": "apps/Deployment/payments/worker"}
	image := observation.ObservedNode{ObservedID: "cbm:p:img", Label: "Image", QualifiedName: "__image__registry.example.com/payments/worker"}

	w.add(worker, "billing")
	w.add(image)
	w.addImageFact("wl", nil)
	w.plans = []policy.RulePlan{usesImageRule(nil)}

	o := w.billingObligation(t, "production")

	if !isFactOfTheWorkload(o.Occurrences) {
		t.Fatalf("occurrences = %+v, want the fact of the workload, not an absence", o.Occurrences)
	}
}

// competingWorld has two manifests of one environment that declare the same Deployment, and a certain conflict.
func competingWorld() *world {
	w := newWorld(observation.KindUsesImage)
	first, second := resource("a", "Deployment", "production", "reports"), resource("b", "Deployment", "production", "reports")
	first.FilePath, second.FilePath = "k8s/prod/reports.yaml", "k8s/overrides/reports.yaml"
	image := observation.ObservedNode{ObservedID: "cbm:p:img", Label: "Image", QualifiedName: "__image__registry.example.com/payments/reports"}

	w.add(first, "billing")
	w.add(second, "billing")
	w.add(image)

	for _, id := range []string{"a", "b"} {
		w.addImageFact(id, nil)
	}

	w.typed.Conflicts = []observation.Conflict{{
		ID: observation.ScopedID{Environment: "production", Cluster: "c1", APIGroup: "apps", Kind: "Deployment", Namespace: "payments", Name: "reports"}, Certain: true,
		Locations: []observation.Location{{NodeID: "cbm:p:a", Path: "k8s/prod/reports.yaml"}, {NodeID: "cbm:p:b", Path: "k8s/overrides/reports.yaml"}},
	}}
	w.plans = []policy.RulePlan{usesImageRule(nil)}

	return w
}

// isConflictOfBothLocations says whether the occurrence is a conflict that names both manifests.
func isConflictOfBothLocations(occ *conformance.OccurrenceResult) bool {
	if occ.Class != conformance.OccurrenceConflict || len(occ.Sources) != 2 {
		return false
	}

	return strings.Contains(occ.Detail, "k8s/overrides/reports.yaml") && strings.Contains(occ.Detail, "k8s/prod/reports.yaml")
}

// A08: two manifests of one environment declare the same Deployment. They are not two instances and no
// declaration wins, so the obligation is unknown with the reason evidence-conflict and both locations.
func TestCompetingDeclarationsMakeTheObligationUnknown(t *testing.T) {
	t.Parallel()

	w := competingWorld()
	o := w.billingObligation(t, "production")

	requireUnknown(t, o, conformance.ReasonEvidenceConflict)

	for i := range o.Occurrences {
		if !isConflictOfBothLocations(&o.Occurrences[i]) {
			t.Fatalf("occurrence = %+v, want a conflict that names both locations", o.Occurrences[i])
		}
	}

	// A conflict whose environment or namespace is unresolved does not block: the declarations may be different resources.
	w.typed.Conflicts[0].Certain = false

	if o = w.billingObligation(t, "production"); o.Reason == conformance.ReasonEvidenceConflict {
		t.Fatalf("an uncertain conflict blocked the obligation: %+v", o)
	}
}

func TestUndeclaredScopeEnvironmentIsUnknownNeverNotApplicable(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage,
		policy.DirectionFromWorkload, func(s *policy.ObligationSpec) { s.Scope.Environments = []string{"qa"} })}

	requireUnknown(t, w.billingObligation(t, "qa"), conformance.ReasonEnvironmentUnresolved)
}
