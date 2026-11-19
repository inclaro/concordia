package cli_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// lockCalmRepin runs `lock update` in a workspace of the given calm release, as the tests of the repin do.
type lockCalmRepin struct {
	dir string
}

func (r *lockCalmRepin) writeArch(t *testing.T, version string) {
	t.Helper()

	testutil.WriteTree(t, r.dir, map[string]string{
		"architecture/architecture.json": `{"$schema":"https://calm.finos.org/release/` + version + `/meta/calm.json","nodes":[],"relationships":[]}`,
	})
}

func (r *lockCalmRepin) lockHas(t *testing.T, version string) {
	t.Helper()

	if lock := readFile(t, r.dir, ".concordia.lock"); !strings.Contains(lock, `schemaVersion: "`+version+`"`) {
		t.Fatalf("lock = %q, want schemaVersion %s", lock, version)
	}
}

func (r *lockCalmRepin) update(t *testing.T) result {
	t.Helper()

	return runWith(t, &runConfig{Dir: r.dir, Deps: cbmVersion("0.12.1")}, "lock", "update")
}

func TestLockUpdateRepinsTheCalmSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if res := initIn(t, dir, cbmVersion("0.12.1")); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	repin := &lockCalmRepin{dir: dir}

	repin.lockHas(t, "1.3")
	repin.writeArch(t, "1.2")

	res := repin.update(t)
	if res.Code != 0 || !strings.HasPrefix(res.Stdout, "updated calm schema 1.3 -> 1.2\n") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "calm-schema: 1.3 -> 1.2") {
		t.Fatalf("the report does not name the calm release change: %q", res.Stdout)
	}

	repin.lockHas(t, "1.2")

	res = repin.update(t)
	if res.Code != 0 || res.Stdout != "unchanged calm schema 1.2\n" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	repin.writeArch(t, "1.4")

	res = repin.update(t)
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-INT-003") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	repin.lockHas(t, "1.2")
}

// wantLockStdout reports each part that stdout lacks.
func wantLockStdout(t *testing.T, stdout string, parts ...string) {
	t.Helper()

	for _, want := range parts {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, stdout)
		}
	}
}

// wantLockReport reports each part that the impact report lacks.
func wantLockReport(t *testing.T, report string, parts ...string) {
	t.Helper()

	for _, want := range parts {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %s: %s", want, report)
		}
	}
}

// lockText makes a lockfile text for the impact tests. profile is the `profile:` block ("" for none).
func lockText(normalization, profile string) string {
	text := "lockVersion: 1\nresolved:\n  calm:\n    schemaVersion: \"1.2\"\n  cbm:\n    executableVersion: 0.12.1\n    adapterSchema: concordia-cbm/v2\n"
	if normalization != "" {
		text += "    normalizationVersion: \"" + normalization + "\"\n"
	}

	return text + profile + "resources: []\n"
}

func shippedLockProfile(t *testing.T) string {
	t.Helper()

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no shipped profile for CBM 0.12.1")
	}

	return "    profile:\n      id: " + profile.ID + "\n      digest: " + profile.Digest + "\n"
}

func TestLockUpdateNormalizationVersionChangeWritesTheImpactReport(t *testing.T) {
	t.Parallel()

	setup := newChkSetup(t, map[string]string{".concordia.lock": lockText("1.0", shippedLockProfile(t))}, nil)
	setup.cfg.Deps.Cbm = cbmVersion("0.12.1").Cbm

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout,
		"digest impact (build/concordia/digest-impact.json)",
		"normalization-version: 1.0 -> "+observation.NormalizationVersion,
		"latest run: none recorded",
		"equivalence not shown",
		"no run is recorded to compare with")

	if strings.Contains(res.Stdout, "capabilities") {
		t.Errorf("the profile did not change but the report has a capability section: %q", res.Stdout)
	}

	report := readFile(t, setup.dir, "build/concordia/digest-impact.json")
	wantLockReport(t, report, `"component": "normalization-version"`, `"from": "1.0"`, `"shown": false`, `"statement": "equivalence not shown"`)

	if lock := readFile(t, setup.dir, ".concordia.lock"); !strings.Contains(lock, "normalizationVersion: \""+observation.NormalizationVersion+"\"") {
		t.Errorf("lock = %q, want the new normalization version", lock)
	}

	// A second update changes no pinned component and writes no report.
	res = setup.run(t, "lock", "update")
	if res.Code != 0 || strings.Contains(res.Stdout, "digest impact") {
		t.Fatalf("second update: exit code = %d, stdout = %q", res.Code, res.Stdout)
	}
}

func TestLockUpdateProfileThatGainsFamiliesListsThemWithTheirFixtures(t *testing.T) {
	t.Parallel()

	setup := newChkSetup(t, map[string]string{".concordia.lock": lockText(observation.NormalizationVersion, "")}, nil)
	setup.cfg.Deps.Cbm = cbmVersion("0.12.1").Cbm

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout, "capability-profile: none -> cbm-0.12.1", "capabilities added: ", "fixtures of the new profile: ", "equivalence not shown")

	report := readFile(t, setup.dir, "build/concordia/digest-impact.json")
	wantLockReport(t, report, `"change": "added"`, `"family": "deploys-artifact"`, `"comparable": true`)
}

func TestLockUpdateUnshippedLockedProfileIsNotComparable(t *testing.T) {
	t.Parallel()

	old := "    profile:\n      id: cbm-0.11.0\n      digest: sha256:aa\n"
	setup := newChkSetup(t, map[string]string{".concordia.lock": lockText(observation.NormalizationVersion, old)}, nil)
	setup.cfg.Deps.Cbm = cbmVersion("0.12.1").Cbm

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout, "cbm-0.11.0 (sha256:aa) is not shipped with this build", "equivalence not shown", "the capability profiles cannot be compared")
}

// tamperFirstFingerprint changes the first finding fingerprint of a results file.
func tamperFirstFingerprint(t *testing.T, results string) string {
	t.Helper()

	i := strings.Index(results, `"fingerprint": "sha256:`)
	if i < 0 {
		t.Fatalf("results.json has no finding fingerprint: %s", results)
	}

	j := strings.Index(results[i:], "sha256:") + i

	return results[:j+len("sha256:")] + "00000000" + results[j+len("sha256:")+8:]
}

func TestLockUpdateComparesTheLatestRunWithTheNewToolchain(t *testing.T) {
	t.Parallel()

	setup := lockCheckedSetup(t)

	results := readFile(t, setup.dir, "build/concordia/results.json")
	testutil.WriteTree(t, setup.dir, map[string]string{".concordia.lock": lockText("1.0", shippedLockProfile(t))})

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("lock update: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout,
		"findings digest: unchanged", "architecture digest: unchanged", "policy digest: unchanged",
		"normalizedGraph digest: not recorded by the latest run", "equivalence shown for the latest run")

	// A recorded run whose fingerprints the new toolchain does not reproduce lists them and shows no equivalence.
	testutil.WriteTree(t, setup.dir, map[string]string{
		"build/concordia/results.json": tamperFirstFingerprint(t, results),
		".concordia.lock":              lockText("1.0", shippedLockProfile(t)),
	})

	res = setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("lock update: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout, "fingerprint removed: ", "fingerprint added: ", "findings digest: sha256:", "equivalence not shown", "1 findings were removed and 1 added")
}

// lockCheckedSetup is a workspace with a lock at normalization 1.0 and the shipped profile, and a recorded run.
func lockCheckedSetup(t *testing.T) *chkSetup {
	t.Helper()

	setup := newChkSetup(t, map[string]string{".concordia.lock": lockText("1.0", shippedLockProfile(t))}, nil)
	setup.cfg.Deps.Cbm = cbmVersion("0.12.1").Cbm

	if res := setup.run(t, "check"); res.Code == 2 || res.Code == 3 {
		t.Fatalf("check: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return setup
}

func TestLockUpdateRecordsWhyTheLatestRunWasNotEvaluated(t *testing.T) {
	t.Parallel()

	setup := lockCheckedSetup(t)

	testutil.WriteTree(t, setup.dir, map[string]string{
		"policies/demo.policy.yaml": "not: [a policy",
		".concordia.lock":           lockText("1.0", shippedLockProfile(t)),
	})

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("lock update: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout, "not evaluated with the new toolchain", "equivalence not shown", "could not be evaluated with the new toolchain")
}
