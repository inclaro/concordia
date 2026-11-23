package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/projection"
)

// hasStatus reports whether the capability has the status and a detail with every one of the parts.
func (c capState) hasStatus(status string, detailParts ...string) bool {
	return c.Status == status && containsEvery(c.Detail, detailParts...)
}

// is reports whether the capability has the version, the status and a detail with every one of the parts.
func (c capState) is(status, version string, detailParts ...string) bool {
	return c.Version == version && c.hasStatus(status, detailParts...)
}

// doctorRun runs `doctor` in the directory with a healthy CBM client, the probes of a complete toolchain and the
// environment.
func doctorRun(t *testing.T, dir string, env map[string]string, args ...string) result {
	t.Helper()

	deps := clientDeps(fakeCbm("0.12.1", "doctor-repo"), okProbe)

	return runWith(t, &runConfig{Dir: dir, Deps: deps, Env: env}, append([]string{"doctor"}, args...)...)
}

// requireExit0 stops the test unless the run exited 0.
func requireExit0(t *testing.T, res *result) {
	t.Helper()

	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// expectStdoutHas reports each of the wanted parts that the standard output lacks.
func expectStdoutHas(t *testing.T, stdout string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A41: CBM 0.12.1 has a tested capability profile, so `cbm` is available and
// the detail names the profile.
func TestDoctorA41CbmWithProfileIsAvailable(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil, "--json")
	requireExit0(t, &res)

	cbm := capabilities(t, res.Stdout)["cbm"]
	if !cbm.hasStatus("available", "cbm-0.12.1") || strings.Contains(cbm.Detail, "newer") {
		t.Errorf("cbm = %+v", cbm)
	}

	if strings.Contains(res.Stderr, "CO-OBS-004") {
		t.Errorf("version warning: %q", res.Stderr)
	}
}

// firstMissingKey gives the first of the keys that the item does not have.
func firstMissingKey(item map[string]any, keys ...string) (key string, missing bool) {
	for _, key := range keys {
		if _, ok := item[key]; !ok {
			return key, true
		}
	}

	return "", false
}

// doctorCbmItem is the `cbm` item of the capabilities in the JSON of `doctor`; nil when there is none.
func doctorCbmItem(t *testing.T, stdout string) map[string]any {
	t.Helper()

	var doc struct {
		Capabilities []map[string]any `json:"capabilities"`
	}

	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}

	var cbm map[string]any

	for _, c := range doc.Capabilities {
		if c["name"] == "cbm" {
			cbm = c
		}
	}

	return cbm
}

// requireCbmProfile gives the profile object of the `cbm` item, which has to be the applied profile of CBM 0.12.1.
func requireCbmProfile(t *testing.T, cbm map[string]any) map[string]any {
	t.Helper()

	if key, missing := firstMissingKey(cbm, "name", "status", "version", "detail", "profile"); missing {
		t.Fatalf("cbm item has no %q: %v", key, cbm)
	}

	profile, _ := cbm["profile"].(map[string]any)
	if !isAppliedCbmProfile(profile) {
		t.Fatalf("profile = %v", profile)
	}

	return profile
}

// isAppliedCbmProfile says whether the profile object is the applied profile of CBM 0.12.1 with a digest.
func isAppliedCbmProfile(profile map[string]any) bool {
	profileDigest, _ := profile["digest"].(string)

	return profile["id"] == "cbm-0.12.1" && profile["applies"] == true && strings.HasPrefix(profileDigest, "sha256:")
}

// profileLossModes gives the loss modes of each family of the profile, and requires the keys of every family.
func profileLossModes(t *testing.T, profile map[string]any) map[string][]any {
	t.Helper()

	families, _ := profile["families"].([]any)
	found := map[string][]any{}

	for _, f := range families {
		fm, _ := f.(map[string]any)
		family, _ := fm["family"].(string)
		found[family], _ = fm["lossModes"].([]any)

		if key, missing := firstMissingKey(fm, "family", "level", "maturity", "lossModes"); missing {
			t.Fatalf("family %v has no %q", fm, key)
		}
	}

	return found
}

// mergesImageOccurrences reports whether the uses-image family lists merged-occurrences first.
func mergesImageOccurrences(found map[string][]any) bool {
	modes := found["uses-image"]

	return len(modes) > 0 && modes[0] == "merged-occurrences"
}

func TestDoctorJSONHasTheProfileObject(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil, "--json")

	profile := requireCbmProfile(t, doctorCbmItem(t, res.Stdout))
	found := profileLossModes(t, profile)

	if !mergesImageOccurrences(found) || len(found["index-result"]) != 3 {
		t.Fatalf("loss modes = %v", found)
	}

	for family, want := range map[string]int{"placed-in": 0, "deployment-workload-set": 4, "dockerfile-set": 2} {
		modes, ok := found[family]
		if !ok || len(modes) != want {
			t.Errorf("doctor family %s loss modes = %v (listed %v), want %d", family, modes, ok, want)
		}
	}
}

func TestDoctorTextShowsTheProfileAndLossModes(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil)

	expectStdoutHas(t, res.Stdout, "profile cbm-0.12.1 sha256:", "uses-image (supported, advisory): merged-occurrences", "index-result (partial, advisory): default-excluded-directory")
}

// corruptLockProfileDigest changes the first digits of the capability profile digest in the lock of the directory.
func corruptLockProfileDigest(t *testing.T, dir string) {
	t.Helper()

	lockPath := filepath.Join(dir, ".concordia.lock")

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	text := string(data)

	profile := strings.Index(text, "profile:")
	if profile < 0 {
		t.Fatalf("init wrote no profile into the lock:\n%s", text)
	}

	// The lock can hold other digests (the CBM executable, resources): change the digest of the profile only.
	at := strings.Index(text[profile:], "sha256:")
	if at < 0 {
		t.Fatalf("the profile in the lock has no digest:\n%s", text)
	}

	i := profile + at
	if err := os.WriteFile(lockPath, []byte(text[:i+len("sha256:")]+"00"+text[i+len("sha256:")+2:]), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorLockProfileDigestMismatch(t *testing.T) {
	t.Parallel()

	dir := initialized(t)
	corruptLockProfileDigest(t, dir)

	res := doctorRun(t, dir, nil, "--json")

	lock := capabilities(t, res.Stdout)["lock"]
	if !lock.hasStatus("degraded", "capability profile cbm-0.12.1") {
		t.Errorf("lock = %+v", lock)
	}
}

func TestDoctorReportsAvailableCapabilitiesAndExits0(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil, "--json")
	requireExit0(t, &res)

	caps := capabilities(t, res.Stdout)
	for name, status := range map[string]string{
		"workspace": "available", "calm-schema": "available", "cbm": "available", "git": "available",
		"java": "available", "structurizr": "missing", "lock": "available",
	} {
		if caps[name].Status != status {
			t.Errorf("%s = %q, want %q", name, caps[name].Status, status)
		}
	}

	for name, version := range map[string]string{"git": "2.43.0", "java": "21.0.1", "cbm": "0.12.1"} {
		if caps[name].Version != version {
			t.Errorf("%s version = %q, want %q: %+v", name, caps[name].Version, version, caps)
		}
	}
}

func TestDoctorMissingGitIsOptional(t *testing.T) {
	t.Parallel()

	noGit := func(ctx context.Context, cmd string, args []string) *cli.ProbeResult {
		if cmd == "git" {
			return nil
		}

		return okProbe(ctx, cmd, args)
	}

	deps := clientDeps(fakeCbm("0.12.1", "doctor-repo"), noGit)
	res := runWith(t, &runConfig{Dir: initialized(t), Deps: deps}, "doctor", "--json")
	requireExit0(t, &res)

	if git := capabilities(t, res.Stdout)["git"]; !git.hasStatus("missing", "optional:") {
		t.Errorf("git = %+v", git)
	}
}

func TestDoctorPrettyOutput(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil)
	requireExit0(t, &res)

	if !strings.Contains(res.Stdout, "calm-schema     available    1.2,1.3\n") || !strings.Contains(res.Stdout, "structurizr     missing      optional: Structurizr CLI not found\n") {
		t.Errorf("stdout = %q", res.Stdout)
	}

	if res.Stdout[0] == '{' {
		t.Error("pretty output is JSON")
	}
}

func TestDoctorFormatJSONFromGlobalOption(t *testing.T) {
	t.Parallel()

	dir := initialized(t)

	res := runWith(t, &runConfig{Dir: dir, Deps: clientDeps(fakeCbm("0.12.1", "doctor-repo"), okProbe)}, "--format", "json", "doctor")
	if res.Code != 0 || !strings.HasPrefix(res.Stdout, "{") {
		t.Fatalf("exit code = %d, stdout = %q", res.Code, res.Stdout)
	}
}

func TestDoctorDegradedCbmExits0ButStrictExits5(t *testing.T) {
	t.Parallel()

	dir := initialized(t)
	deps := clientDeps(fakeCbm("0.12.1"), okProbe)

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "doctor")
	requireExit0(t, &res)

	res = runWith(t, &runConfig{Dir: dir, Deps: deps}, "doctor", "--strict")
	if res.Code != 5 {
		t.Fatalf("--strict exit code = %d", res.Code)
	}

	if !strings.Contains(res.Stdout, "run concordia index") {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

type versionFailure struct {
	observation.Client
	err error
}

func (v versionFailure) Version(context.Context) (string, error) { return "", v.err }

func TestDoctorMissingOrIncompatibleCbmExits5(t *testing.T) {
	t.Parallel()

	dir := initialized(t)

	missing := versionFailure{Client: fakeCbm("0.12.1"), err: diag.Fail(diag.OBS003, diag.Params{"executable": "codebase-memory-mcp"})}

	res := runWith(t, &runConfig{Dir: dir, Deps: clientDeps(missing, okProbe)}, "doctor", "--json")
	if res.Code != 5 || capabilities(t, res.Stdout)["cbm"].Status != "missing" {
		t.Fatalf("missing: exit code = %d, stdout = %q", res.Code, res.Stdout)
	}

	res = runWith(t, &runConfig{Dir: dir, Deps: clientDeps(fakeCbm("2.0.0"), okProbe)}, "doctor", "--json")
	if res.Code != 5 || capabilities(t, res.Stdout)["cbm"].Status != "incompatible" {
		t.Fatalf("incompatible: exit code = %d, stdout = %q", res.Code, res.Stdout)
	}
}

func TestDoctorMissingWorkspaceExits5(t *testing.T) {
	t.Parallel()

	noProbe := func(context.Context, string, []string) *cli.ProbeResult { return nil }

	res := runWith(t, &runConfig{Deps: clientDeps(fakeCbm("0.12.1"), noProbe)}, "--offline", "doctor", "--json")
	if res.Code != 5 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	caps := capabilities(t, res.Stdout)
	if caps["workspace"].Status != "missing" || caps["git"].Status != "missing" {
		t.Errorf("caps = %+v", caps)
	}

	if caps["network-policy"].Detail != "offline" || caps["lock"].Status != "degraded" {
		t.Errorf("caps = %+v", caps)
	}
}

// structurizrProbe answers `version` of /opt/structurizr with the given output.
func structurizrProbe(output string) func(context.Context, string, []string) *cli.ProbeResult {
	return func(ctx context.Context, cmd string, args []string) *cli.ProbeResult {
		if cmd == "/opt/structurizr" {
			return &cli.ProbeResult{Stdout: output}
		}

		return okProbe(ctx, cmd, args)
	}
}

func doctorStructurizr(t *testing.T, output string, args ...string) (state capState, code int) {
	t.Helper()

	dir := initialized(t)
	res := runWith(t, &runConfig{
		Dir: dir, Deps: clientDeps(fakeCbm("0.12.1", "doctor-repo"), structurizrProbe(output)),
		Env: map[string]string{"STRUCTURIZR_CLI": "/opt/structurizr"},
	}, append([]string{"doctor", "--json"}, args...)...)

	return capabilities(t, res.Stdout)["structurizr"], res.Code
}

func TestDoctorFindsStructurizrFromEnvironmentAndWarnsWhenItIsNotThePinnedVersion(t *testing.T) {
	t.Parallel()

	got, code := doctorStructurizr(t, "\nStructurizr CLI 2024.1\n")
	if code != 0 || !got.is("degraded", "Structurizr CLI 2024.1", "warning", projection.PinnedStructurizrCLI) {
		t.Errorf("structurizr = %+v, exit code = %d", got, code)
	}

	if _, strict := doctorStructurizr(t, "structurizr-cli: 2024.1.0\n", "--strict"); strict != 5 {
		t.Errorf("--strict exit code = %d, want 5", strict)
	}
}

func TestDoctorAcceptsThePinnedStructurizrVersion(t *testing.T) {
	t.Parallel()

	got, code := doctorStructurizr(t, "structurizr-cli: "+projection.PinnedStructurizrCLI+"\nstructurizr-java: 5.0.2\nJava: 21.0.8\n", "--strict")
	if want := (capState{Status: "available", Version: projection.PinnedStructurizrCLI}); got != want || code != 0 {
		t.Errorf("structurizr = %+v, exit code = %d", got, code)
	}
}

func TestDoctorLockDisagreement(t *testing.T) {
	t.Parallel()

	dir := initialized(t)

	res := runWith(t, &runConfig{Dir: dir, Deps: clientDeps(fakeCbm("0.12.3", "doctor-repo"), okProbe)}, "doctor", "--json")

	lock := capabilities(t, res.Stdout)["lock"]
	if lock.Status != "degraded" || lock.Detail != "lock has cbm 0.12.1, found 0.12.3" {
		t.Errorf("lock = %+v", lock)
	}
}

// doctorClaims reads the `claims` object of the doctor JSON.
type doctorClaims struct {
	Claims struct {
		Digest    string   `json:"digest"`
		Claims    []string `json:"claims"`
		Contracts []struct {
			ID         string   `json:"id"`
			Kind       string   `json:"kind"`
			Enabled    []any    `json:"enabled"`
			NotEnabled []string `json:"notEnabled"`
		} `json:"contracts"`
	} `json:"claims"`
}

// claimContractIndex is the claim contracts of the doctor JSON by their id.
type claimContractIndex struct {
	kinds      map[string]string
	enabled    map[string]int
	notEnabled map[string][]string
}

func indexClaimContracts(doc *doctorClaims) claimContractIndex {
	idx := claimContractIndex{kinds: map[string]string{}, enabled: map[string]int{}, notEnabled: map[string][]string{}}

	for _, contract := range doc.Claims.Contracts {
		idx.kinds[contract.ID] = contract.Kind
		idx.enabled[contract.ID] = len(contract.Enabled)
		idx.notEnabled[contract.ID] = contract.NotEnabled
	}

	return idx
}

// evaluators is the number of contracts of the kind evaluator.
func (idx claimContractIndex) evaluators() int {
	count := 0

	for _, kind := range idx.kinds {
		if kind == "evaluator" {
			count++
		}
	}

	return count
}

// listsEnabledAndNotEnabled reports whether the contract says what it enables and what it does not enable.
func (idx claimContractIndex) listsEnabledAndNotEnabled(id string) bool {
	return idx.enabled[id] > 0 && len(idx.notEnabled[id]) > 0
}

// isAdapter reports whether the contract is an adapter that says what it enables and what it does not enable.
func (idx claimContractIndex) isAdapter(id string) bool {
	return idx.kinds[id] == "adapter" && idx.listsEnabledAndNotEnabled(id)
}

// requireClaimRegistry decodes the `claims` object of `doctor --json` and requires a digest and the thirteen claims.
func requireClaimRegistry(t *testing.T, stdout string) *doctorClaims {
	t.Helper()

	var doc doctorClaims
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(doc.Claims.Digest, "sha256:") || len(doc.Claims.Claims) != 13 {
		t.Fatalf("registry = %+v", doc.Claims)
	}

	return &doc
}

func TestDoctorJSONListsTheClaimContracts(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil, "--json")
	requireExit0(t, &res)

	idx := indexClaimContracts(requireClaimRegistry(t, res.Stdout))

	for _, id := range []string{"rendering", "plan", "provenance", "verification", "quality"} {
		if !idx.isAdapter(id) {
			t.Errorf("contract %s: kind %q, %d enabled, not enabled %v", id, idx.kinds[id], idx.enabled[id], idx.notEnabled[id])
		}
	}

	if got := idx.evaluators(); got != 3 {
		t.Errorf("evaluator contracts = %d, want the network, the access, and the cloud firewall evaluator", got)
	}

	if !slices.Contains(idx.notEnabled["provenance"], "slsa-level") {
		t.Errorf("provenance does not enable: %v", idx.notEnabled["provenance"])
	}
}

func TestDoctorTextHasTheClaimContractSection(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil)

	expectStdoutHas(t, res.Stdout, "claim contracts (registry sha256:", "  provenance ", "    enables: exact-lineage", "    does not enable: ")
}

// withKustomizeSource declares a rendering source that runs kustomize.
func withKustomizeSource(t *testing.T, dir string) {
	t.Helper()

	text := readFile(t, dir, "concordia.yaml")
	if err := os.WriteFile(filepath.Join(dir, "concordia.yaml"), []byte(text+`
environments:
  - {id: staging, sources: [deploy/**]}
assurance:
  rendering:
    sources:
      - {environment: staging, tool: kustomize, path: deploy/overlays/staging}
`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorProbesTheRenderersAsOptionalCapabilities(t *testing.T) {
	t.Parallel()

	env := map[string]string{"HELM": fakeRenderer(t, "helm", "v4.3.1+gc0d08d0"), "KUSTOMIZE": "/nonexistent/kustomize"}

	res := doctorRun(t, initialized(t), env, "--json")
	requireExit0(t, &res)

	caps := capabilities(t, res.Stdout)
	if !caps["helm"].is("available", "v4.3.1") {
		t.Errorf("helm = %+v", caps["helm"])
	}

	if !caps["kustomize"].hasStatus("missing", "optional") {
		t.Errorf("kustomize = %+v", caps["kustomize"])
	}
}

func TestDoctorFailsWhenASourceNeedsAMissingRenderer(t *testing.T) {
	t.Parallel()

	dir := initialized(t)
	withKustomizeSource(t, dir)

	res := doctorRun(t, dir, map[string]string{"KUSTOMIZE": "/nonexistent/kustomize"}, "--json")
	if res.Code != 5 {
		t.Fatalf("exit code = %d, want 5; stdout = %q", res.Code, res.Stdout)
	}

	if kustomize := capabilities(t, res.Stdout)["kustomize"]; !kustomize.hasStatus("missing", "required by a declared rendering source") {
		t.Errorf("kustomize = %+v", kustomize)
	}
}

func TestDoctorRendererPinMismatchIsDegraded(t *testing.T) {
	t.Parallel()

	dir := initialized(t)
	withKustomizeSource(t, dir)

	env := map[string]string{"KUSTOMIZE": fakeRenderer(t, "kustomize", "v5.8.3")}

	// No pin yet: a source uses the renderer and `check` would stop on it.
	res := doctorRun(t, dir, env, "--json")
	if lock := capabilities(t, res.Stdout)["lock"]; !lock.hasStatus("degraded", "kustomize v5.8.3 is used by a rendering source and is not pinned") {
		t.Errorf("unpinned lock = %+v", lock)
	}

	if res := runWith(t, &runConfig{Dir: dir, Deps: cbmVersion("0.12.1"), Env: env}, "lock", "update"); res.Code != 0 {
		t.Fatalf("lock update: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res = doctorRun(t, dir, env, "--json")
	if lock := capabilities(t, res.Stdout)["lock"]; lock.Status != "available" {
		t.Errorf("pinned lock = %+v", lock)
	}

	// A new build of the renderer no longer matches the pin.
	env["KUSTOMIZE"] = fakeRenderer(t, "kustomize", "v5.9.0")

	res = doctorRun(t, dir, env, "--json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, want 0 (degraded is not a failure)", res.Code)
	}

	if lock := capabilities(t, res.Stdout)["lock"]; !lock.hasStatus("degraded", "lock has kustomize v5.8.3", "found v5.9.0") {
		t.Errorf("mismatched lock = %+v", lock)
	}
}
