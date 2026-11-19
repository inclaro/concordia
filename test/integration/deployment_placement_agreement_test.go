package integration_test

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	deploy "github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/workspace"
)

// These tests check that `deploy.Placements` agrees with INFRA-PLC-001 and INFRA-PLC-002 of the `infrastructure`
// pack on the infra fixture (specs/deployment-placement, "Placement records agree with INFRA-PLC-001 and INFRA-PLC-002").
// Each scenario runs the real check, reads the rule results, and computes the placements from the evidence and the
// bindings of the same run. For each declared node and environment, the verdicts of the rules and the records of the
// helper make one pair.

// plcPair is one of the pairs of a rule verdict and a placement record or gap.
type plcPair string

const (
	// pairPass: INFRA-PLC-001 has a satisfying fact; the helper has a record in the declared container.
	pairPass plcPair = "pass"
	// pairWrongTarget: INFRA-PLC-002 has an unexpected placement in another container; the helper has a record in another container.
	pairWrongTarget plcPair = "wrong-target"
	// pairWrongDirection: INFRA-PLC-002 has a wrong-direction placement; the helper has a reverse record.
	pairWrongDirection plcPair = "wrong-direction"
	// pairBindingGap: a rule is unknown with `binding-gap`; the helper has a gap with that reason.
	pairBindingGap plcPair = "binding-gap"
	// pairAmbiguous: a rule is unknown with `ambiguous-binding`; the helper has a gap with that reason.
	pairAmbiguous plcPair = "ambiguous-binding"
	// pairAbsent: INFRA-PLC-001 has a definite absence and INFRA-PLC-002 passes; the helper has neither a record nor a gap.
	pairAbsent plcPair = "absent"
)

// plcRuleSide is what the results of the two rules say about one node in one environment.
type plcRuleSide struct {
	pass, wrongTarget, wrongDirection, bindingGap, ambiguous, absent bool
}

// plcRecordSide is what the helper says about the same node and environment.
type plcRecordSide struct {
	declared, other, reverse, bindingGap, ambiguous, none bool
}

// plcCell is the pair of one node and environment.
type plcCell struct {
	node, env string
	rules     plcRuleSide
	records   plcRecordSide
}

func (c *plcCell) rulesHave(pair plcPair) bool {
	switch pair {
	case pairPass:
		return c.rules.pass
	case pairWrongTarget:
		return c.rules.wrongTarget
	case pairWrongDirection:
		return c.rules.wrongDirection
	case pairBindingGap:
		return c.rules.bindingGap
	case pairAmbiguous:
		return c.rules.ambiguous
	case pairAbsent:
		return c.rules.absent
	}

	return false
}

// plcAnyOccurrence reports whether an occurrence of the obligation has the class and the result.
func plcAnyOccurrence(ob *conformance.ObligationResult, class conformance.OccurrenceClass, result conformance.RuleVerdict) bool {
	for i := range ob.Occurrences {
		if occ := &ob.Occurrences[i]; occ.Class == class && occ.Result == result {
			return true
		}
	}

	return false
}

// plcHasBindingGapReason reports whether an occurrence of the obligations is an unknown binding gap with the reason.
func plcHasBindingGapReason(reason conformance.UnknownReason, obligations ...*conformance.ObligationResult) bool {
	for _, ob := range obligations {
		if plcAnyOccurrenceWithReason(ob, conformance.OccurrenceBindingGap, conformance.VerdictUnknown, reason) {
			return true
		}
	}

	return false
}

// plcAnyOccurrenceWithReason reports whether an occurrence of the obligation has the class, the result, and the reason.
func plcAnyOccurrenceWithReason(ob *conformance.ObligationResult, class conformance.OccurrenceClass, result conformance.RuleVerdict, reason conformance.UnknownReason) bool {
	for i := range ob.Occurrences {
		occ := &ob.Occurrences[i]
		if occ.Reason != reason || occ.Class != class {
			continue
		}

		if occ.Result == result {
			return true
		}
	}

	return false
}

// plcDefiniteAbsence reports whether INFRA-PLC-001 shows nothing but absences and INFRA-PLC-002 passes.
func plcDefiniteAbsence(first, second *conformance.ObligationResult) bool {
	if first.Verdict != conformance.VerdictViolation || len(first.Occurrences) == 0 {
		return false
	}

	if second.Verdict != conformance.VerdictPass {
		return false
	}

	for i := range first.Occurrences {
		occ := &first.Occurrences[i]
		if occ.Class != conformance.OccurrenceAbsent || occ.Result != conformance.VerdictViolation {
			return false
		}
	}

	return true
}

// plcRuleSideOf reads the occurrences of the two obligations of a node and an environment.
func plcRuleSideOf(t *testing.T, d *deployment, node, env string) plcRuleSide {
	t.Helper()

	first := d.obligationOf(t, "INFRA-PLC-001", node, env)
	second := d.obligationOf(t, "INFRA-PLC-002", node, env)

	return plcRuleSide{
		pass:           plcAnyOccurrence(first, conformance.OccurrenceMatching, conformance.VerdictPass),
		wrongTarget:    plcAnyOccurrence(second, conformance.OccurrenceMatching, conformance.VerdictViolation),
		wrongDirection: plcAnyOccurrence(second, conformance.OccurrenceWrongDirection, conformance.VerdictViolation),
		bindingGap:     plcHasBindingGapReason(conformance.ReasonBindingGap, first, second),
		ambiguous:      plcHasBindingGapReason(conformance.ReasonAmbiguousBinding, first, second),
		absent:         plcDefiniteAbsence(first, second),
	}
}

// plcHelper runs `deploy.Placements` on the evidence of the run and on the bindings and the model of its workspace.
// The binding set is resolved as the pipeline does for the nodes that a placement fact reaches.
func plcHelper(t *testing.T, d *deployment) (deploy.PlacementSet, *intent.ArchitectureModel) {
	t.Helper()

	wspace, err := workspace.Load(filepath.Join(d.w.dir, "concordia.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}

	loaded := intent.LoadArchitecture(filepath.Join(d.w.dir, "architecture", "infra.calm.json"), intent.LoadContext{Root: d.w.dir})
	if loaded.Model == nil {
		t.Fatalf("the model did not load: %+v", loaded.Diagnostics)
	}

	bindings, err := binding.Load(wspace, loaded.Model)
	if err != nil {
		t.Fatal(err)
	}

	evidence := d.run.Evidence
	if evidence == nil {
		t.Fatal("the run kept no evidence")
	}

	clusters := map[string]string{}
	environments := make([]deploy.Environment, 0, len(wspace.Environments))

	for i := range wspace.Environments {
		clusters[wspace.Environments[i].ID] = wspace.Environments[i].Cluster
		environments = append(environments, deploy.Environment{ID: wspace.Environments[i].ID, Cluster: wspace.Environments[i].Cluster})
	}

	observed := slices.Collect(maps.Values(evidence.Nodes))
	observed = append(observed, evidence.Entities...)
	observed = append(observed, binding.ImageDigestNodes(evidence.Edges)...)

	set := binding.Resolve(bindings.Bindings, observed, nil, binding.WithClusterOf(func(env string) string { return clusters[env] }))

	return deploy.Placements(evidence, &set, bindings.Bindings, environments, loaded.Model), loaded.Model
}

// plcDeclaredContainers gives the containers of the `deployed-in` relationships of each deployed node.
func plcDeclaredContainers(model *intent.ArchitectureModel) map[string][]string {
	out := map[string][]string{}

	for i := range model.Relationships {
		if rel := &model.Relationships[i]; rel.Kind == intent.RelDeployedIn && rel.Container != "" {
			for _, node := range rel.Targets {
				out[node] = append(out[node], rel.Container)
			}
		}
	}

	return out
}

// plcAddPlacement records the kind of a placement of the helper: reverse, in a declared container, or in another one.
func plcAddPlacement(side *plcRecordSide, p *deploy.Placement, declared []string) {
	switch {
	case p.Reverse:
		side.reverse = true
	case slices.Contains(declared, p.Container):
		side.declared = true
	default:
		side.other = true
	}
}

// plcAddGap records the reason of a gap of the helper.
func plcAddGap(side *plcRecordSide, g *deploy.PlacementGap) {
	side.bindingGap = side.bindingGap || g.Reason == deploy.ReasonBindingGap
	side.ambiguous = side.ambiguous || g.Reason == deploy.ReasonAmbiguousBinding
}

// plcPlacementsOf lists the records of the helper for the node and the environment.
func plcPlacementsOf(set *deploy.PlacementSet, node, env string) []*deploy.Placement {
	var out []*deploy.Placement

	for i := range set.Placements {
		if p := &set.Placements[i]; p.Node == node && p.Environment == env {
			out = append(out, p)
		}
	}

	return out
}

// plcGapsOf lists the gaps of the helper for the node and the environment.
func plcGapsOf(set *deploy.PlacementSet, node, env string) []*deploy.PlacementGap {
	var out []*deploy.PlacementGap

	for i := range set.Gaps {
		if g := &set.Gaps[i]; g.Node == node && g.Environment == env {
			out = append(out, g)
		}
	}

	return out
}

// plcRecordSideOf reads the records and gaps of one node and environment.
func plcRecordSideOf(set *deploy.PlacementSet, declared []string, node, env string) plcRecordSide {
	var side plcRecordSide

	for _, p := range plcPlacementsOf(set, node, env) {
		plcAddPlacement(&side, p, declared)
	}

	for _, g := range plcGapsOf(set, node, env) {
		plcAddGap(&side, g)
	}

	side.none = side == plcRecordSide{}

	return side
}

// plcCells gives the pair of each deployed node and each declared environment of the run.
func plcCells(t *testing.T, d *deployment) []plcCell {
	t.Helper()

	set, model := plcHelper(t, d)
	containers := plcDeclaredContainers(model)
	nodes := slices.Sorted(maps.Keys(containers))

	cells := make([]plcCell, 0, len(nodes)*2)

	for _, node := range nodes {
		for _, env := range []string{"production", "staging"} {
			cells = append(cells, plcCell{
				node: node, env: env, rules: plcRuleSideOf(t, d, node, env), records: plcRecordSideOf(&set, containers[node], node, env),
			})
		}
	}

	return cells
}

// plcImplication is one direction of the agreement of a cell: when one side has a pair, the other side has it too.
type plcImplication struct {
	message    string
	when, then bool
}

// plcImplications lists the agreements of a cell: each pair of the rules has one of the helper, and the converse.
func plcImplications(c *plcCell) []plcImplication {
	return []plcImplication{
		{"INFRA-PLC-001 has a satisfying fact and the helper has no record in the declared container", c.rules.pass, c.records.declared},
		{"INFRA-PLC-002 has a placement in another container and the helper has no such record", c.rules.wrongTarget, c.records.other},
		{"INFRA-PLC-002 has a wrong-direction placement and the helper has no reverse record", c.rules.wrongDirection, c.records.reverse},
		{"a rule has `binding-gap` and the helper has no such gap", c.rules.bindingGap, c.records.bindingGap},
		{"a rule has `ambiguous-binding` and the helper has no such gap", c.rules.ambiguous, c.records.ambiguous},
		{"the rules show a definite absence and the helper has a record or a gap", c.rules.absent, c.records.none},
		// The converse: a record or a gap of the helper has a rule result of the same kind.
		{"the helper has a record in the declared container and INFRA-PLC-001 has no satisfying fact", c.records.declared, c.rules.pass},
		{"the helper has a record in another container and INFRA-PLC-002 has no such placement", c.records.other, c.rules.wrongTarget},
		{"the helper has a `binding-gap` and no rule has one", c.records.bindingGap, c.rules.bindingGap},
		{"the helper has a reverse record and INFRA-PLC-002 has no wrong-direction placement", c.records.reverse, c.rules.wrongDirection},
		{"the helper has an ambiguous gap and no rule has `ambiguous-binding`", c.records.ambiguous, c.rules.ambiguous},
	}
}

// plcAgree reports each disagreement of the rules and the helper in a cell.
func plcAgree(t *testing.T, c *plcCell) {
	t.Helper()

	for _, imp := range plcImplications(c) {
		if imp.when && !imp.then {
			t.Errorf("%s in %s: %s: %+v", c.node, c.env, imp.message, c)
		}
	}
}

// plcCheckExpectation checks that the rules show each expected pair for the node and the environment.
func plcCheckExpectation(t *testing.T, cells []plcCell, want plcExpectation) {
	t.Helper()

	i := slices.IndexFunc(cells, func(c plcCell) bool { return c.node == want.node && c.env == want.env })
	if i < 0 {
		t.Fatalf("no pair for %s in %s", want.node, want.env)
	}

	for _, pair := range want.pairs {
		if !cells[i].rulesHave(pair) {
			t.Errorf("%s in %s: the rules do not show the pair %q: %+v", want.node, want.env, pair, cells[i].rules)
		}
	}
}

func TestPlacementAgreement(t *testing.T) {
	t.Parallel()

	for _, tc := range plcAgreementCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := deploymentRun(t, tc.spec(t))
			cells := plcCells(t, d)

			for i := range cells {
				plcAgree(t, &cells[i])
			}

			for _, want := range tc.expect {
				plcCheckExpectation(t, cells, want)
			}
		})
	}
}

// plcPlacementKey is the part of a placement record that is a plain value.
type plcPlacementKey struct {
	node, container, environment, workload, hostingTarget string
	reverse                                               bool
}

func plcKeyOfPlacement(p *deploy.Placement) plcPlacementKey {
	return plcPlacementKey{
		node: p.Node, container: p.Container, environment: p.Environment, workload: p.Workload,
		hostingTarget: p.HostingTarget, reverse: p.Reverse,
	}
}

func plcSamePlacement(a, b *deploy.Placement) bool {
	return plcKeyOfPlacement(a) == plcKeyOfPlacement(b) && slices.Equal(a.Locations, b.Locations)
}

// plcGapKey is the part of a placement gap that is a plain value.
type plcGapKey struct {
	environment, side, node, identity string
	reason                            deploy.Reason
}

func plcKeyOfGap(g *deploy.PlacementGap) plcGapKey {
	return plcGapKey{environment: g.Environment, side: g.Side, node: g.Node, identity: g.Identity, reason: g.Reason}
}

func plcSameGap(a, b *deploy.PlacementGap) bool {
	return plcKeyOfGap(a) == plcKeyOfGap(b) && slices.Equal(a.Locations, b.Locations)
}

// plcSameRecords reports whether both lists have the same records in the same order.
func plcSameRecords[T any](a, b []T, same func(x, y *T) bool) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !same(&a[i], &b[i]) {
			return false
		}
	}

	return true
}

func plcSameSet(a, b *deploy.PlacementSet) bool {
	return plcSameRecords(a.Placements, b.Placements, plcSamePlacement) && plcSameRecords(a.Gaps, b.Gaps, plcSameGap)
}

// plcIsProductionAPIPlacement reports whether the record places the api node in prod-cluster in production, with the
// manifest of the production deployment as its only location.
func plcIsProductionAPIPlacement(p *deploy.Placement) bool {
	if p.Node != "api-service" || p.Container != "prod-cluster" {
		return false
	}

	if p.Environment != "production" {
		return false
	}

	return len(p.Locations) == 1 && p.Locations[0].Path == "k8s/prod/deployment.yaml"
}

func plcIsPlacementRule(ruleID string) bool {
	return slices.Contains([]string{"INFRA-PLC-001", "INFRA-PLC-002"}, ruleID)
}

// The helper reads the same facts with and without the `infrastructure` pack, and the waiver or baseline of a
// placement finding never changes it: it has no rule input.
func TestPlacementAgreementHelperNeedsNoPack(t *testing.T) {
	t.Parallel()

	with := deploymentRun(t, &deploymentSpec{})
	without := deploymentRun(t, &deploymentSpec{withoutPack: true})

	withSet, _ := plcHelper(t, with)
	withoutSet, _ := plcHelper(t, without)

	if len(withSet.Placements) == 0 || len(withSet.Gaps) == 0 {
		t.Fatalf("the infra fixture gives %d records and %d gaps", len(withSet.Placements), len(withSet.Gaps))
	}

	if !plcSameSet(&withSet, &withoutSet) {
		t.Errorf("with the pack: %+v\nwithout it: %+v", withSet, withoutSet)
	}

	for _, rule := range without.run.Rules {
		if plcIsPlacementRule(rule.RuleID) {
			t.Errorf("rule %s ran without the pack", rule.RuleID)
		}
	}

	// The observed placement is the one of the facts: the production api workload is in the namespace bound to prod-cluster.
	if !slices.ContainsFunc(withoutSet.Placements, func(p deploy.Placement) bool { return plcIsProductionAPIPlacement(&p) }) {
		t.Errorf("no record (api-service, prod-cluster, production): %+v", withoutSet.Placements)
	}
}
