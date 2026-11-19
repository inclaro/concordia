package provenance_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestClaimIDsAreTheRegistryIDs(t *testing.T) {
	t.Parallel()

	for got, want := range map[string]claims.ID{
		provenance.ClaimExactLineage: claims.ExactLineage, provenance.ClaimDeployedDigest: claims.DeployedDigest,
		provenance.ClaimDeploymentSucceeded: claims.DeploymentSucceeded, provenance.ClaimRuntimeDrift: claims.RuntimeDrift,
	} {
		if got != string(want) {
			t.Errorf("claim id %q differs from the registry id %q", got, want)
		}
	}

	if provenance.ContractID != "provenance" {
		t.Errorf("contract id = %q", provenance.ContractID)
	}
}

// stmtSpec describes one provenance statement of a case; an empty field takes the value of a good statement.
type stmtSpec struct {
	verification statement.Verification
	path         string
	digest       string
	builder      string
	source       string
}

func statements(t *testing.T, specs []stmtSpec) []provenance.Statement {
	t.Helper()

	out := make([]provenance.Statement, 0, len(specs))

	for i := range specs {
		spec := &specs[i]

		if spec.path == "" {
			spec.path = "build/attestations/shop.json"
		}

		if spec.digest == "" {
			spec.digest = digestA
		}

		if spec.builder == "" {
			spec.builder = goodBuilder
		}

		if spec.source == "" {
			spec.source = goodSourceURI
		}

		if spec.verification.Signature == "" {
			spec.verification = verified("ci-build")
		}

		out = append(out, slsa(t, spec.path, spec.digest, spec.builder, spec.source, spec.verification))
	}

	return out
}

// lineageCase is one case of TestLineage.
type lineageCase struct {
	name        string
	image       string
	wantReason  string
	wantDetail  string
	wantVerdict provenance.Verdict
	statements  []stmtSpec
	wantMissing []string
}

// lineageOutcome is the claim, the verdict, and the reason of a lineage result.
type lineageOutcome struct {
	claim   string
	reason  string
	verdict provenance.Verdict
}

// checkLineage checks the verdict, the missing items, and the detail of a lineage result against the case.
func checkLineage(t *testing.T, got *provenance.LineageResult, want *lineageCase) {
	t.Helper()

	gotOutcome := lineageOutcome{claim: got.Claim, reason: got.Reason, verdict: got.Verdict}
	wantOutcome := lineageOutcome{claim: provenance.ClaimExactLineage, reason: want.wantReason, verdict: want.wantVerdict}

	if gotOutcome != wantOutcome {
		t.Fatalf("result = %q %q (%q), want %q %q", got.Claim, got.Verdict, got.Reason, want.wantVerdict, want.wantReason)
	}

	if !slices.Equal(got.Missing, want.wantMissing) {
		t.Errorf("missing = %v, want %v", got.Missing, want.wantMissing)
	}

	if want.wantDetail != "" && got.Detail != want.wantDetail {
		t.Errorf("detail = %q, want %q", got.Detail, want.wantDetail)
	}
}

// occurrenceKey is the digest, the tag, and the container that a result names for its occurrence.
type occurrenceKey struct {
	digest    string
	tag       string
	container string
}

// checkLineageRefs checks that a lineage result names the trust contract and the occurrence it is about.
func checkLineageRefs(t *testing.T, got *provenance.LineageResult, occ *render.ContainerOccurrence, contract *provenance.TrustContract) {
	t.Helper()

	if got.Contract.ID != provenance.ContractID || got.Contract.Digest != contract.Digest() {
		t.Errorf("the result does not name the trust contract: %+v", got.Contract)
	}

	gotKey := occurrenceKey{digest: got.Occurrence.Digest, tag: got.Occurrence.Tag, container: got.Occurrence.Container}
	wantKey := occurrenceKey{digest: occ.Digest, tag: occ.Tag, container: "api"}

	if gotKey != wantKey {
		t.Errorf("occurrence = %+v", got.Occurrence)
	}
}

// lineageDigestCases are the cases about the digest of the occurrence and the digest of the statement.
func lineageDigestCases() []lineageCase {
	const (
		tagOnly   = "registry.example/shop:1.4"
		pinned    = "registry.example/shop@" + digestA
		otherPath = "registry.example/shop:1.4@" + digestA
	)

	return []lineageCase{
		{
			// A33.
			name: "tag only and no provenance", image: tagOnly,
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingDigest, provenance.MissingProvenance},
		},
		{
			name: "tag moved after the build: the attestation is not connected to a tag", image: tagOnly,
			statements:  []stmtSpec{{}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingDigest, provenance.MissingProvenance},
		},
		{
			// A34.
			name: "digest and verified provenance agree", image: pinned,
			statements:  []stmtSpec{{}},
			wantVerdict: provenance.VerdictPass, wantMissing: []string{},
		},
		{
			name: "a tag with a digest is linked by the digest", image: otherPath,
			statements:  []stmtSpec{{}},
			wantVerdict: provenance.VerdictPass, wantMissing: []string{},
		},
		{
			name: "digest differs", image: pinned,
			statements:  []stmtSpec{{digest: digestB}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingProvenance}, wantDetail: "no provenance for this digest",
		},
	}
}

// lineageTrustCases are the cases about the signature, the producer, the builder, and the source of the statement.
func lineageTrustCases() []lineageCase {
	const (
		pinned = "registry.example/shop@" + digestA
		evil   = "https://ci.example/evil/build.yml@refs/heads/main"
	)

	failed := statement.Verification{Signature: statement.SignatureFailed, Subject: statement.SubjectUnchecked, Failure: "no matching signatures"}

	return []lineageCase{
		{
			name: "authentic provenance with an untrusted builder", image: pinned,
			statements:  []stmtSpec{{builder: evil}},
			wantVerdict: provenance.VerdictViolation, wantMissing: []string{},
		},
		{
			name: "builder that only starts like a trusted one is untrusted", image: pinned,
			statements:  []stmtSpec{{builder: "https://github.com/acme/shop/.github/workflows/build.yml-evil@refs/heads/main"}},
			wantVerdict: provenance.VerdictViolation, wantMissing: []string{},
		},
		{
			name: "source is not a declared repository", image: pinned,
			statements:  []stmtSpec{{source: "git+https://github.com/other/thing@refs/heads/main"}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingSource},
		},
		{
			name: "producer is not trusted for build provenance", image: pinned,
			statements:  []stmtSpec{{verification: verified("checks")}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingProvenance},
		},
		{
			name: "unsigned statement", image: pinned,
			statements:  []stmtSpec{{verification: unsigned()}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingProvenance},
		},
		{
			name: "signature failed (changed after signing)", image: pinned,
			statements:  []stmtSpec{{verification: failed}},
			wantVerdict: provenance.VerdictUnknown, wantReason: provenance.ReasonNoEvidenceSupplied,
			wantMissing: []string{provenance.MissingProvenance},
		},
		{
			name: "one tampered statement does not hide a verified one", image: pinned,
			statements:  []stmtSpec{{path: "build/attestations/a-tampered.json", verification: failed}, {}},
			wantVerdict: provenance.VerdictPass, wantMissing: []string{},
		},
		{
			name: "a trusted statement wins over an untrusted builder statement", image: pinned,
			statements:  []stmtSpec{{path: "build/attestations/a-evil.json", builder: "https://ci.example/evil/build.yml"}, {}},
			wantVerdict: provenance.VerdictPass, wantMissing: []string{},
		},
		{
			name: "an untrusted builder is a violation even next to a statement with an undeclared source", image: pinned,
			statements: []stmtSpec{
				{path: "build/attestations/a-nosource.json", source: "git+https://github.com/other/thing"},
				{path: "build/attestations/b-evil.json", builder: "https://ci.example/evil/build.yml"},
			},
			wantVerdict: provenance.VerdictViolation, wantMissing: []string{},
		},
	}
}

func TestLineage(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(lineageDigestCases(), lineageTrustCases())
	contract := provenance.NewTrustContract(config())

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			occ := occurrence(t, test.image)
			got := provenance.Lineage(&occ, statements(t, test.statements), &contract)

			checkLineage(t, &got, &test)
			checkLineageRefs(t, &got, &occ, &contract)
		})
	}
}

func TestLineagePassNamesTheEvidence(t *testing.T) {
	t.Parallel()

	contract := provenance.NewTrustContract(config())
	occ := occurrence(t, "registry.example/shop@"+digestA)
	stmt := slsa(t, "build/attestations/shop.json", digestA, goodBuilder, goodSourceURI, verified("ci-build"))

	got := provenance.Lineage(&occ, []provenance.Statement{stmt}, &contract)

	wantStatement := refKey{path: "build/attestations/shop.json", producer: "ci-build", integrity: assurance.IntegrityAuthenticated}
	if keyOf(got.Statement) != wantStatement {
		t.Errorf("statement = %+v", got.Statement)
	}

	if got.Builder != goodBuilder {
		t.Errorf("builder = %q", got.Builder)
	}

	want := provenance.SourceRef{ID: "shop", URI: goodSourceURI, Revision: "1111111111111111111111111111111111111111"}
	if got.Source == nil || *got.Source != want {
		t.Errorf("source = %+v, want %+v", got.Source, want)
	}
}

func TestLineageViolationHasTheBuilderAsEvidence(t *testing.T) {
	t.Parallel()

	contract := provenance.NewTrustContract(config())
	occ := occurrence(t, "registry.example/shop@"+digestA)
	evil := "https://ci.example/evil/build.yml@refs/heads/main"

	got := provenance.Lineage(&occ, []provenance.Statement{slsa(t, "x.json", digestA, evil, goodSourceURI, verified("ci-build"))}, &contract)

	if got.Verdict != provenance.VerdictViolation || got.Reason != "" {
		t.Errorf("result = %+v", got)
	}

	if got.Builder != evil || got.Statement == nil {
		t.Errorf("result = %+v", got)
	}
}

func TestLineageListsRejectedStatementsThatNameTheDigest(t *testing.T) {
	t.Parallel()

	contract := provenance.NewTrustContract(config())
	occ := occurrence(t, "registry.example/shop@"+digestA)

	statements := []provenance.Statement{
		slsa(t, "build/attestations/unsigned.json", digestA, goodBuilder, goodSourceURI, unsigned()),
		slsa(t, "build/attestations/other.json", digestB, goodBuilder, goodSourceURI, verified("ci-build")),
	}

	got := provenance.Lineage(&occ, statements, &contract)

	paths := rejectedPaths(got.Rejected)

	if !slices.Equal(paths, []string{"build/attestations/unsigned.json"}) || !rejectedWith(got.Rejected, assurance.DetailNotAuthenticated) {
		t.Errorf("rejected = %+v, want only the unsigned statement that names the digest", got.Rejected)
	}
}

func TestLineageStaleProvenanceIsRejected(t *testing.T) {
	t.Parallel()

	cfg := config()
	cfg.Provenance.MaxAgeDays = 1

	contract := provenance.NewTrustContract(cfg)
	occ := occurrence(t, "registry.example/shop@"+digestA)

	loaded := parse(t, "build/attestations/shop.json", digestA, statement.PredicateSLSAProvenance, slsaPredicate(goodBuilder, goodSourceURI))

	later := obligation()
	later.Now = later.Now.Add(time.Hour)

	stmt, err := provenance.NewStatement(&loaded, verified("ci-build"), later, cfg)
	if err != nil {
		t.Fatal(err)
	}

	got := provenance.Lineage(&occ, []provenance.Statement{stmt}, &contract)

	if got.Verdict != provenance.VerdictUnknown || !rejectedWith(got.Rejected, assurance.DetailStale) {
		t.Errorf("result = %+v, want unknown with a stale statement", got)
	}
}

func TestNewStatementRefusesOtherPredicates(t *testing.T) {
	t.Parallel()

	loaded := parse(t, "x.json", digestA, statement.PredicatePlan, map[string]any{})

	if _, err := provenance.NewStatement(&loaded, verified("ci-build"), obligation(), config()); err == nil {
		t.Error("a predicate of another type must be an error")
	}
}

func TestSourceRepositoryMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		declared string
		uri      string
		want     bool
	}{
		{"shop", "git+https://github.com/acme/shop@refs/heads/main", true},
		{"shop", "git+https://github.com/acme/shop.git@refs/tags/v1", true},
		{"acme/shop", "git+https://github.com/acme/shop@refs/heads/main", true},
		{"github.com/acme/shop", "git+https://github.com/acme/shop@refs/heads/main", true},
		{"https://github.com/acme/shop", "git+https://github.com/acme/shop@refs/heads/main", true},
		{"Acme/Shop", "https://GitHub.com/acme/shop/", true},
		{"shop", "git@github.com:acme/shop.git", true},
		{"shop", "ssh://git@github.com/acme/shop.git", true},
		{"shop", "git+https://user:pw@github.com/acme/shop#readme", true},
		{"other/shop", "git+https://github.com/acme/shop", false},
		{"shop", "git+https://github.com/acme/shop-extra", false},
		{"shop", "git+https://github.com/acme/myshop", false},
		{"gitlab.com/acme/shop", "git+https://github.com/acme/shop", false},
		{"shop", "pkg:oci/shop?repository_url=registry.example/shop", false},
		{"shop", "pkg:oci/shop?repository_url=https://registry.example/shop", false},
		{"shop", "https://github.com", false},
		{"shop", "", false},
		{"github.com", "https://github.com/acme/shop", false},
	}

	for _, test := range tests {
		t.Run(test.declared+" "+test.uri, func(t *testing.T) {
			t.Parallel()

			cfg := config()
			cfg.Provenance.SourceRepositories = []string{test.declared}

			contract := provenance.NewTrustContract(cfg)
			occ := occurrence(t, "registry.example/shop@"+digestA)
			stmt := slsa(t, "x.json", digestA, goodBuilder, test.uri, verified("ci-build"))

			got := provenance.Lineage(&occ, []provenance.Statement{stmt}, &contract)
			if passed := got.Verdict == provenance.VerdictPass; passed != test.want {
				t.Errorf("verdict = %q, want pass = %v", got.Verdict, test.want)
			}
		})
	}
}

func TestTrustContractDigest(t *testing.T) {
	t.Parallel()

	base := provenance.NewTrustContract(config())

	changed := config()
	changed.Provenance.Builders = []string{"b", "https://github.com/acme/shop/.github/workflows/build.yml"}

	checkDigestIgnoresOrder(t, changed)

	changedContract := provenance.NewTrustContract(changed)

	if base.Digest() == changedContract.Digest() {
		t.Error("the digest does not change with a builder")
	}

	moved := config()
	moved.Producers[0].Authentication.Key = "/keys/other.pub"
	movedContract := provenance.NewTrustContract(moved)

	if base.Digest() == movedContract.Digest() {
		t.Error("the digest does not change with the key of a producer")
	}

	checkLineageContractProducers(t)
	checkEmptyContract(t)
}

// checkDigestIgnoresOrder checks that a contract of the same declaration in another order has the same digest as
// the contract of changed.
func checkDigestIgnoresOrder(t *testing.T, changed *workspace.Assurance) {
	t.Helper()

	reordered := config()
	reordered.Producers = []workspace.Producer{reordered.Producers[1], reordered.Producers[0]}
	reordered.Producers[1].Claims = slices.Clone(reordered.Producers[1].Claims)
	slices.Reverse(reordered.Producers[1].Claims)
	reordered.Provenance.Builders = []string{"b", "https://github.com/acme/shop/.github/workflows/build.yml"}

	if a, b := provenance.NewTrustContract(reordered), provenance.NewTrustContract(changed); a.Digest() != b.Digest() {
		t.Error("the digest depends on the order of the declaration")
	}
}

// checkLineageContractProducers checks that a producer that may not attest build provenance is not part of the
// contract of lineage.
func checkLineageContractProducers(t *testing.T) {
	t.Helper()

	only := provenance.NewTrustContract(config())
	if len(only.Producers) != 1 || only.Producers[0].ID != "ci-build" {
		t.Errorf("producers = %+v", only.Producers)
	}
}

// checkEmptyContract checks that a contract without configuration has a digest, no builders, and passes nothing.
func checkEmptyContract(t *testing.T) {
	t.Helper()

	empty := provenance.NewTrustContract(nil)
	if empty.Digest() == "" || len(empty.Builders) != 0 {
		t.Errorf("empty contract = %+v", empty)
	}

	occ := occurrence(t, "registry.example/shop@"+digestA)
	stmt := slsa(t, "x.json", digestA, goodBuilder, goodSourceURI, verified("ci-build"))

	if got := provenance.Lineage(&occ, []provenance.Statement{stmt}, &empty); got.Verdict == provenance.VerdictPass {
		t.Error("an empty trust contract must not pass")
	}
}

func TestTrustsBuilder(t *testing.T) {
	t.Parallel()

	contract := provenance.TrustContract{Builders: []string{"https://ci.example/build.yml", "https://ci.example/pinned.yml@refs/heads/main"}}

	for id, want := range map[string]bool{
		"https://ci.example/build.yml":                      true,
		"https://ci.example/build.yml@refs/heads/feature":   true,
		"https://ci.example/build.yml2":                     false,
		"https://ci.example/pinned.yml@refs/heads/main":     true,
		"https://ci.example/pinned.yml@refs/heads/feature":  false,
		"https://ci.example/pinned.yml":                     false,
		"":                                                  false,
		"https://ci.example/build.yml/../../evil@refs/main": false,
	} {
		if got := contract.TrustsBuilder(id); got != want {
			t.Errorf("TrustsBuilder(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestLineageStatus: the image-to-build step is `verified` only for a passing exact lineage claim, and a static
// link such as a Dockerfile alone stays a candidate.
func TestLineageStatus(t *testing.T) {
	t.Parallel()

	contract := provenance.NewTrustContract(config())
	pinned := occurrence(t, "registry.example/shop@"+digestA)
	stmt := slsa(t, "x.json", digestA, goodBuilder, goodSourceURI, verified("ci-build"))

	pass := provenance.Lineage(&pinned, []provenance.Statement{stmt}, &contract)
	none := provenance.Lineage(&pinned, nil, &contract)
	evil := provenance.Lineage(&pinned, []provenance.Statement{slsa(t, "y.json", digestA, "https://evil.example/b", goodSourceURI, verified("ci-build"))}, &contract)

	tests := []struct {
		lineage *provenance.LineageResult
		name    string
		want    provenance.StepStatus
		static  bool
	}{
		{name: "verified lineage", lineage: &pass, static: false, want: provenance.StepVerified},
		{name: "verified lineage and a Dockerfile", lineage: &pass, static: true, want: provenance.StepVerified},
		{name: "Dockerfile link only", lineage: &none, static: true, want: provenance.StepCandidate},
		{name: "Dockerfile link and no lineage result", lineage: nil, static: true, want: provenance.StepCandidate},
		{name: "Dockerfile link and a violation", lineage: &evil, static: true, want: provenance.StepCandidate},
		{name: "nothing", lineage: &none, static: false, want: provenance.StepUnknown},
		{name: "nothing at all", lineage: nil, static: false, want: provenance.StepUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := provenance.LineageStatus(test.lineage, test.static); got != test.want {
				t.Errorf("LineageStatus = %q, want %q", got, test.want)
			}
		})
	}
}

// slsaLevelFields are the field names that would state a SLSA level.
var slsaLevelFields = []string{"Level", "SLSALevel", "BuildLevel"}

// TestNoResultFieldStatesASLSALevel: the result types carry no SLSA level.
func TestNoResultFieldStatesASLSALevel(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{
		reflect.TypeFor[provenance.LineageResult](), reflect.TypeFor[provenance.Statement](), reflect.TypeFor[provenance.SourceRef](),
	} {
		for field := range typ.Fields() {
			if slices.Contains(slsaLevelFields, field.Name) {
				t.Errorf("%s has the field %s", typ.Name(), field.Name)
			}
		}
	}
}
