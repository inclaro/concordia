package e2e_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// asrStabilityPackFile is the pack file that owns the `ASR-` rule ids.
const asrStabilityPackFile = "assurance.policy.yaml"

// asrStabilityRun is a check run: its results document and the process result.
type asrStabilityRun struct {
	doc jv
	res cliResult
}

// Tasks 11.2 and INT-03/INT-05: a workspace without an `assurance` key has no `assurance` object, schemaVersion stays
// 1.1, and the assurance pack is the only pack that owns `ASR-` rule ids.
func TestAssuranceAbsentKeyLeavesResultsUnchanged(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	t.Run("commerce", func(t *testing.T) {
		t.Parallel()

		asrStabilityCommerceWithoutKey(t)
	})

	t.Run("pack without the key", func(t *testing.T) {
		t.Parallel()

		asrStabilityPackWithoutKey(t)
	})

	t.Run("rule ids", func(t *testing.T) {
		t.Parallel()

		asrStabilityRuleIDs(t)
	})
}

func asrStabilityCommerceWithoutKey(t *testing.T) {
	t.Helper()

	r := newRepo(t, repoSpec{source: fixtureDir, oldName: "concordia-fixture-commerce", project: "e2e-assurance-commerce", dirName: "commerce"})

	if res := r.gu("index"); res.code != 0 {
		t.Fatalf("index: exit %d: %s", res.code, res.output())
	}

	res := r.cli(nil, "--format", "json", "check")
	asrExitInSet(t, res)

	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("check output is not JSON (exit %d): %v\n%s", res.code, err, res.stderr)
	}

	asrStabilityRequireNoAssuranceInResults(t, doc)

	// The run manifest and the bundle have no assurance files either.
	_, manifest := (&asrRepo{repo: r}).exportBundle()
	for _, f := range manifest.Files {
		if strings.HasPrefix(f.Path, "assurance/") {
			t.Errorf("the bundle has the assurance file %s", f.Path)
		}
	}
}

func asrStabilityRequireNoAssuranceInResults(t *testing.T, doc map[string]any) {
	t.Helper()

	results := jv{v: doc}
	if results.at("schemaVersion").str() != "1.1" {
		t.Errorf("schemaVersion = %q, want 1.1", results.at("schemaVersion").str())
	}

	if _, has := doc["assurance"]; has {
		t.Errorf("results.json has an assurance object without an assurance key: %s", results.at("assurance").text())
	}

	if results.at("toolchain", "assurance").present() {
		t.Errorf("the toolchain has an assurance entry: %s", results.at("toolchain", "assurance").text())
	}

	if text := string(mustMarshal(t, doc["rules"])); strings.Contains(text, "ASR-") {
		t.Error("a rule of the assurance pack ran in a workspace that does not list the pack")
	}
}

func asrStabilityPackWithoutKey(t *testing.T) {
	t.Helper()

	r := (&asrWorld{t: t, noAssurance: true}).build()
	doc, _ := r.check()

	if doc.at("schemaVersion").str() != "1.1" || doc.at("assurance").present() {
		t.Errorf("schemaVersion %q, assurance %s", doc.at("schemaVersion").str(), doc.at("assurance").text())
	}

	// The rules say which input is missing and decide nothing.
	for _, rule := range []string{"ASR-REN-001", "ASR-PRV-001", "ASR-PRV-002"} {
		if got := doc.rule(rule).at("verdict").str(); got == "pass" || got == "violation" {
			t.Errorf("%s = %s without evidence", rule, got)
		}
	}
}

func asrStabilityRuleIDs(t *testing.T) {
	t.Helper()

	owner := asrStabilityRuleOwners(t)
	assurance := 0

	for _, id := range asrStabilityGoldenRuleIDs(t) {
		file, ok := owner[id]
		if !ok {
			t.Errorf("golden rule %s is in no pack", id)

			continue
		}

		asrStabilityRequireOwner(t, id, file)

		if strings.HasPrefix(id, "ASR-") {
			assurance++
		}
	}

	for id, file := range owner {
		asrStabilityRequireOwner(t, id, file)
	}

	if assurance != 3 {
		t.Errorf("the golden list has %d ASR- ids, want 3 (ASR-PRV-001, ASR-PRV-002, ASR-REN-001)", assurance)
	}
}

// asrStabilityRequireOwner says that an `ASR-` id is in the assurance pack and no other id is.
func asrStabilityRequireOwner(t *testing.T, id, file string) {
	t.Helper()

	if strings.HasPrefix(id, "ASR-") != (file == asrStabilityPackFile) {
		t.Errorf("rule %s is in %s: ASR- ids exist only in the assurance pack", id, file)
	}
}

// asrStabilityRuleOwners maps every rule id of the packs to the pack file that declares it.
func asrStabilityRuleOwners(t *testing.T) map[string]string {
	t.Helper()

	root := "../../internal/policy/packs/"

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	pattern := regexp.MustCompile(`(?m)^\s*- id: (\S+)\s*$`)
	owner := map[string]string{}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".policy.yaml") {
			continue
		}

		data, err := os.ReadFile(root + entry.Name())
		if err != nil {
			t.Fatal(err)
		}

		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			if previous, dup := owner[match[1]]; dup {
				t.Errorf("rule id %s is in %s and in %s", match[1], previous, entry.Name())
			}

			owner[match[1]] = entry.Name()
		}
	}

	return owner
}

// asrStabilityGoldenRuleIDs reads the rule ids of the golden list.
func asrStabilityGoldenRuleIDs(t *testing.T) []string {
	t.Helper()

	var golden struct {
		Rules []string `json:"rules"`
	}

	data, err := os.ReadFile("../golden/rule-ids.json")
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}

	return golden.Rules
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// Task 11.3: a second evaluation of the same inputs gives the same semantic results and digests: the whole results
// document but the run identity, an incremental run, the files of two bundles, and the statements that a bundle holds.
func TestAssuranceRepeatedEvaluationsAreEqual(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := asrFullWorld(t).build()
	first, res := r.check()

	if res.code != 0 {
		t.Fatalf("exit code = %d\n%s", res.code, res.stderr)
	}

	asrStabilityRequireDecisions(t, first)

	second, secondRes := r.check()
	asrStabilityRequireRunsAgree(t, "results.json differs between two runs", asrStabilityRun{first, res}, asrStabilityRun{second, secondRes})

	asrStabilityRequireIncrementalAgrees(t, r, first)
	asrStabilityRequireBundlesAgree(t, r, first)

	if verify := r.gu("export", "verify", "build/concordia/conformance.tar.gz"); verify.code != 0 {
		t.Errorf("export verify: exit %d: %s", verify.code, verify.output())
	}

	// A bundle is a valid left side of `diff`, and nothing differs between it and the same inputs.
	if diff := r.cli(r.env, "diff", "--from", "build/concordia/conformance.tar.gz", "--to", "worktree"); diff.code != 0 {
		t.Errorf("diff from the bundle: exit %d: %s", diff.code, diff.output())
	}
}

// asrStabilityRequireDecisions says that the world gives every adapter a decision: it is not a run that equals itself
// by being empty.
func asrStabilityRequireDecisions(t *testing.T, first jv) {
	t.Helper()

	for _, container := range []string{"app", "sidecar"} {
		if entry := lineageOf(t, first, container); entry.at("verdict").str() != "pass" {
			t.Errorf("%s lineage = %s", container, entry.text())
		}
	}

	for _, want := range []struct{ rule, verdict string }{
		{"ASR-REN-001", "pass"}, {"ACC-001", "pass"}, {"ACC-002", "pass"}, {"CHK-001", "pass"}, {"quality:QS-FAILOVER", "pass"},
	} {
		if got := first.rule(want.rule).at("verdict").str(); got != want.verdict {
			t.Errorf("%s = %s, want %s: %s", want.rule, got, want.verdict, first.rule(want.rule).text())
		}
	}
}

// asrStabilityRequireRunsAgree says that two runs have the same results but the volatile fields, and the same digest.
func asrStabilityRequireRunsAgree(t *testing.T, what string, first, second asrStabilityRun) {
	t.Helper()

	if a, b := withoutVolatile(t, first.res.stdout), withoutVolatile(t, second.res.stdout); a != b {
		t.Errorf("%s: %s", what, firstDifference(a, b))
	}

	if a, b := first.doc.at("summary", "resultDigest").str(), second.doc.at("summary", "resultDigest").str(); a == "" || a != b {
		t.Errorf("result digests differ: %q, %q", a, b)
	}
}

// asrStabilityRequireIncrementalAgrees says that an incremental evaluation gives the results of the full one.
func asrStabilityRequireIncrementalAgrees(t *testing.T, r *asrRepo, first jv) {
	t.Helper()

	incremental, _ := r.check("--incremental")
	if a, b := first.at("assurance").text(), incremental.at("assurance").text(); a != b {
		t.Errorf("the assurance section differs between a full and an incremental run: %s", firstDifference(a, b))
	}

	if a, b := first.at("summary", "resultDigest").str(), incremental.at("summary", "resultDigest").str(); a != b {
		t.Errorf("result digests differ between a full and an incremental run: %s, %s", a, b)
	}
}

// asrStabilityRequireBundlesAgree says that two bundles of the same inputs have the same assurance files, and that the
// statements that a bundle holds are the statements of the run (a replay from the bundle gives the same statement
// results).
func asrStabilityRequireBundlesAgree(t *testing.T, r *asrRepo, first jv) {
	t.Helper()

	dirA, manifestA := r.exportBundle()
	_, manifestB := r.exportBundle()

	asrStabilityRequireSameAssuranceFiles(t, manifestA.Integrity.Files, manifestB.Integrity.Files)

	paths := make([]string, len(manifestA.Files))
	for i := range manifestA.Files {
		paths[i] = manifestA.Files[i].Path
	}

	persisted := bundleStatements(t, dirA, paths)
	listed := first.at("assurance", "statements").list()

	if len(persisted) != len(listed) {
		t.Errorf("the bundle holds %d statements, the run lists %d", len(persisted), len(listed))
	}

	for _, summary := range listed {
		want := asrStabilitySortedSubjects(summary)

		got := persisted[summary.at("digest").str()]
		if !slices.Equal(got, want) {
			t.Errorf("statement %s: the bundle has subjects %v, the run %v", summary.at("digest").str(), got, want)
		}
	}
}

// asrStabilitySortedSubjects gives the sorted subject digests of a statement summary.
func asrStabilitySortedSubjects(summary jv) []string {
	listed := summary.at("subjectDigests").list()
	subjects := make([]string, 0, len(listed))

	for _, subject := range listed {
		subjects = append(subjects, subject.str())
	}

	slices.Sort(subjects)

	return subjects
}

// asrStabilityRequireSameAssuranceFiles says that the assurance files of the first integrity map have the digests of the
// second, and that there is at least one.
func asrStabilityRequireSameAssuranceFiles(t *testing.T, a, b map[string]string) {
	t.Helper()

	assuranceFiles := 0

	for path, digest := range a {
		if !strings.HasPrefix(path, "assurance/") {
			continue
		}

		assuranceFiles++

		if b[path] != digest {
			t.Errorf("%s differs between two bundles: %s, %s", path, digest, b[path])
		}
	}

	if assuranceFiles == 0 {
		t.Fatal("the bundle has no assurance file")
	}
}
