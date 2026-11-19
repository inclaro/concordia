package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// The helpers of the placement tests (INFRA-PLC-001 and INFRA-PLC-002 of the `infrastructure` pack).

const (
	plcRule1 = "INFRA-PLC-001"
	plcRule2 = "INFRA-PLC-002"

	plcFixImplementation = "fix implementation"
	plcResolveBinding    = "resolve binding"
	plcSupplyEvidence    = "supply evidence"

	plcBindingsHead = "apiVersion: concordia.io/v1alpha1\nkind: BindingSet\nbindings:\n"

	plcProductionAndStaging = "environments:\n  - id: production\n    cluster: prod-eu\n    sources: [\"k8s/prod/**\"%s]\n  - id: staging\n    sources: [\"k8s/staging/**\"]\n"
)

// plcBothRules gives the two placement rules.
func plcBothRules() []string {
	return []string{plcRule1, plcRule2}
}

// plcObligationKey names one obligation: the rule, the subject, and the environment.
type plcObligationKey struct {
	rule, subject, env string
}

// plc001 names the INFRA-PLC-001 obligation of the subject in the environment.
func plc001(subject, env string) plcObligationKey {
	return plcObligationKey{rule: plcRule1, subject: subject, env: env}
}

// plc002API names the INFRA-PLC-002 obligation of api-service in the environment.
func plc002API(env string) plcObligationKey {
	return plcObligationKey{rule: plcRule2, subject: "api-service", env: env}
}

// plcOutcome is the verdict and the unknown reason that an obligation is expected to have.
type plcOutcome struct {
	verdict conformance.RuleVerdict
	reason  conformance.UnknownReason
}

// plcSubtest is one named parallel subtest that builds its own run.
type plcSubtest struct {
	run  func(t *testing.T)
	name string
}

// plcSharedSubtest is one named parallel subtest on a run that the subtests share.
type plcSharedSubtest struct {
	run  func(t *testing.T, d *deployment)
	name string
}

func plcRunSubtests(t *testing.T, cases []plcSubtest) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func plcRunSharedSubtests(t *testing.T, d *deployment, cases []plcSharedSubtest) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, d)
		})
	}
}

// plcBindings makes a binding file of the items.
func plcBindings(items ...string) string {
	if len(items) == 0 {
		return strings.Replace(plcBindingsHead, "bindings:\n", "bindings: []\n", 1)
	}

	return plcBindingsHead + strings.Join(items, "")
}

// plcNonExclusive makes the binding item non-exclusive: two such bindings of one tier leave the entity ambiguous.
func plcNonExclusive(item string) string {
	return strings.Replace(item, "    match:", "    exclusive: false\n    match:", 1)
}

// plcNamespace binds the namespace of an environment (the hosting target) to a CALM node.
func plcNamespace(id, node, env, namespace string) string {
	return "  - id: " + id + "\n    architectureRef: calm:node:" + node + "\n    match:\n      any:\n        - {kind: infraResource, environment: " + env +
		", address: core/Namespace/_/" + namespace + "}\n"
}

// plcAgreement gives the binding item the agreement of the node bindings.
func plcAgreement(item, agreement string) string {
	return strings.Replace(item, "    match:", "    agreement: "+agreement+"\n    match:", 1)
}

// plcEnvironments declares production and staging. extraSources are added to the sources of production.
func plcEnvironments(extraSources ...string) string {
	var extra strings.Builder

	for _, source := range extraSources {
		extra.WriteString(", \"" + source + "\"")
	}

	return strings.Replace(plcProductionAndStaging, "%s", extra.String(), 1)
}

// plcArchitecture adds nodes to the default model (each is a service without a placement).
func plcArchitecture(t *testing.T, nodes ...string) string {
	t.Helper()

	var extra strings.Builder

	for _, node := range nodes {
		extra.WriteString(",\n    {\"unique-id\": \"" + node + "\", \"node-type\": \"system\", \"name\": \"" + node + "\", \"description\": \"An extra node.\"}")
	}

	anchor := "The production hosting target.\"}"

	text := strings.Replace(deploymentArchitecture, anchor, anchor+extra.String(), 1)
	if text == deploymentArchitecture {
		t.Fatal("the default model has no production hosting target node")
	}

	return text
}

// plcRecordingVersion writes the infra recording with another CBM version and gives its path.
func plcRecordingVersion(t *testing.T, version string) string {
	t.Helper()

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, infraRecordingFile), &doc); err != nil {
		t.Fatal(err)
	}

	doc["version"] = json.RawMessage(`"` + version + `"`)

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "recording.json")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// plcObligations gives the obligation results of a rule, or none when the rule has none.
func plcObligations(t *testing.T, d *deployment, rule string) []conformance.ObligationResult {
	t.Helper()

	return ruleOf(t, d.run, rule).Obligations
}

// plcObligation gives the obligation of the key.
func plcObligation(t *testing.T, d *deployment, key plcObligationKey) *conformance.ObligationResult {
	t.Helper()

	return d.obligationOf(t, key.rule, key.subject, key.env)
}

// plcNobody requires that the rule has no obligation for the subject in the environment.
func plcNobody(t *testing.T, d *deployment, key plcObligationKey) {
	t.Helper()

	obligations := plcObligations(t, d, key.rule)
	for i := range obligations {
		if ob := &obligations[i]; ob.Subject == key.subject && ob.Environment == key.env {
			t.Errorf("%s has an obligation for %s in %s: %+v", key.rule, key.subject, key.env, ob)
		}
	}
}

// plcWant checks the verdict and the reason of an obligation, and returns it.
func plcWant(t *testing.T, d *deployment, key plcObligationKey, want plcOutcome) *conformance.ObligationResult {
	t.Helper()

	ob := plcObligation(t, d, key)
	if ob.Verdict != want.verdict || ob.Reason != want.reason {
		t.Fatalf("%s for %s in %s = %s (%s): %s, want %s (%s)", key.rule, key.subject, key.env, ob.Verdict, ob.Reason, ob.Detail, want.verdict, want.reason)
	}

	return ob
}

// plcWantVerdict checks the verdict of an obligation that has no unknown reason, and returns it.
func plcWantVerdict(t *testing.T, d *deployment, key plcObligationKey, verdict conformance.RuleVerdict) *conformance.ObligationResult {
	t.Helper()

	return plcWant(t, d, key, plcOutcome{verdict: verdict})
}

// plcWantUnknown checks that an obligation is unknown for the reason, and returns it.
func plcWantUnknown(t *testing.T, d *deployment, key plcObligationKey, reason conformance.UnknownReason) *conformance.ObligationResult {
	t.Helper()

	return plcWant(t, d, key, plcOutcome{verdict: conformance.VerdictUnknown, reason: reason})
}

// plcOccurrence gives the first occurrence result of the class.
func plcOccurrence(t *testing.T, ob *conformance.ObligationResult, class conformance.OccurrenceClass) *conformance.OccurrenceResult {
	t.Helper()

	for i := range ob.Occurrences {
		if ob.Occurrences[i].Class == class {
			return &ob.Occurrences[i]
		}
	}

	t.Fatalf("obligation %s for %s in %s has no occurrence of class %s: %+v", ob.RuleID, ob.Subject, ob.Environment, class, ob.Occurrences)

	return nil
}

// plcOccurrencesWhere gives the occurrence results of the obligation for which the predicate holds.
func plcOccurrencesWhere(ob *conformance.ObligationResult, pred func(*conformance.OccurrenceResult) bool) []*conformance.OccurrenceResult {
	var out []*conformance.OccurrenceResult

	for i := range ob.Occurrences {
		if pred(&ob.Occurrences[i]) {
			out = append(out, &ob.Occurrences[i])
		}
	}

	return out
}

// plcIsPass reports whether the occurrence result is a pass.
func plcIsPass(occ *conformance.OccurrenceResult) bool {
	return occ.Result == conformance.VerdictPass
}

// plcLocatedAt reports whether the location is in the file.
func plcLocatedAt(loc *conformance.Location, path string) bool {
	return loc != nil && loc.Path == path
}

// plcLocatedUnder reports whether the location is in a file under the directory prefix.
func plcLocatedUnder(loc *conformance.Location, prefix string) bool {
	return loc != nil && strings.HasPrefix(loc.Path, prefix)
}

// plcOccurrenceWant is the class of an occurrence, the file of its location, and its next action.
type plcOccurrenceWant struct {
	class conformance.OccurrenceClass
	path  string
	// action is the next action of the occurrence.
	action string
}

// plcRequireOccurrenceAt checks that the first occurrence of the class is a fact of the file with the next action.
func plcRequireOccurrenceAt(t *testing.T, ob *conformance.ObligationResult, want plcOccurrenceWant) {
	t.Helper()

	occ := plcOccurrence(t, ob, want.class)
	if !plcLocatedAt(occ.Location, want.path) || occ.NextAction != want.action {
		t.Errorf("occurrence = %+v", occ)
	}
}

// plcRequireFixImplementation checks that the obligation has an absent occurrence with the next action `fix implementation`.
func plcRequireFixImplementation(t *testing.T, ob *conformance.ObligationResult) {
	t.Helper()

	if occ := plcOccurrence(t, ob, conformance.OccurrenceAbsent); occ.NextAction != plcFixImplementation {
		t.Errorf("occurrence = %+v", occ)
	}
}

// plcNextActions gives the next action of each occurrence result of the obligation.
func plcNextActions(ob *conformance.ObligationResult) []string {
	var out []string

	for i := range ob.Occurrences {
		if ob.Occurrences[i].NextAction != "" {
			out = append(out, ob.Occurrences[i].NextAction)
		}
	}

	return out
}

// plcWorkloadNode gives the observed ID of the manifest node with the scoped ID.
func plcWorkloadNode(t *testing.T, d *deployment, scopedID string) string {
	t.Helper()

	for id := range d.run.Evidence.Nodes {
		if d.run.Evidence.Nodes[id].ScopedID == scopedID {
			return id
		}
	}

	t.Fatalf("no manifest node %s", scopedID)

	return ""
}

// plcManifestNode gives the observed ID of the resource node that a file declares.
func plcManifestNode(t *testing.T, d *deployment, file string) string {
	t.Helper()

	for id := range d.run.Evidence.Nodes {
		if node := d.run.Evidence.Nodes[id]; node.FilePath == file && strings.HasPrefix(node.ScopedID, "k8s:") {
			return id
		}
	}

	t.Fatalf("no resource node in %s", file)

	return ""
}

// plcIsFindingOf reports whether the finding is of the rule for the subject in the environment.
func plcIsFindingOf(f *conformance.Finding, key plcObligationKey) bool {
	if f.RuleID != key.rule {
		return false
	}

	return f.Subject == key.subject && f.Environment == key.env
}

// plcFinding gives the finding of the rule for the subject in the environment.
func plcFinding(t *testing.T, run *conformance.Run, key plcObligationKey) *conformance.Finding {
	t.Helper()

	for i := range run.Findings {
		if f := &run.Findings[i]; plcIsFindingOf(f, key) {
			return f
		}
	}

	t.Fatalf("no finding of %s for %s in %s", key.rule, key.subject, key.env)

	return nil
}

// plcAPIBindings binds only the production workload api. Every other workload is unbound.
func plcAPIBindings(items ...string) string {
	return plcBindings(append([]string{workloadBinding("bind-api-workload", "api-service", "production", "api", "payments")}, items...)...)
}

// plcPairsContainer reports whether a reconciliation entry pairs the container prod-cluster.
func plcPairsContainer(entry *conformance.ReconciliationEntry) bool {
	return entry.From == "prod-cluster" || entry.To == "prod-cluster"
}

// plcRequireNoContainerPair checks that no reconciliation entry of the run pairs the container.
func plcRequireNoContainerPair(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Reconciliation {
		if plcPairsContainer(&run.Reconciliation[i]) {
			t.Errorf("reconciliation entry %+v pairs the container", run.Reconciliation[i])
		}
	}
}
