package impact_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/trust"
)

const secretValue = "hunter2-very-secret"

// fixtureFor gives one item of the kind with a value for every field of the contract.
func fixtureFor(c impact.Contract, kind impact.Kind) impact.Item {
	facets := impact.Facets{}
	for _, f := range c.FieldsOf(kind) {
		facets[f] = []string{"v-" + f}
	}

	return c.NewItem(kind, "fixture/"+string(kind), facets, nil, nil)
}

func fixtures(c impact.Contract) []impact.Item {
	out := make([]impact.Item, 0, len(impact.Kinds()))
	for _, k := range impact.Kinds() {
		out = append(out, fixtureFor(c, k))
	}

	return out
}

func routeItem(c impact.Contract, host string) impact.Item {
	return c.NewItem(impact.KindPublicEntryPoint, "prod/ingress/api", impact.Facets{
		"host": {host}, "path": {"/v1"}, "backend": {"svc/api"}, "exposureClass": {"internet"},
	}, nil, nil)
}

func TestContractCoversEveryKind(t *testing.T) {
	c := impact.V1()
	requireEq(t, "version", c.Version, "review-digest/1")
	requireEq(t, "kinds", len(impact.Kinds()), len(trust.DependencyKinds()))

	for _, k := range impact.Kinds() {
		checkTrue(t, k.Valid(), string(k)+" is not valid")
		checkTrue(t, len(c.FieldsOf(k)) > 0, "kind "+string(k)+" has no contract fields")
	}

	checkTrue(t, !impact.Kind("firewall-rule").Valid(), "firewall-rule must not be a kind")
	checkEq(t, "ReasonReferenceUnresolved", impact.ReasonReferenceUnresolved, "reference-unresolved")
	checkEq(t, "DetailContractChanged", impact.DetailContractChanged, "digest contract changed")
	checkSlice(t, "public-entry-point fields", c.FieldsOf(impact.KindPublicEntryPoint), []string{"backend", "exposureClass", "host", "path"})
}

func TestDigestIgnoresFieldsOutsideContract(t *testing.T) {
	c := impact.V1()
	base := routeItem(c, "api.example.com")
	want := c.Digest(base, nil)

	withExtra := c.NewItem(impact.KindPublicEntryPoint, "prod/ingress/api", impact.Facets{
		"host": {"api.example.com"}, "path": {"/v1"}, "backend": {"svc/api"}, "exposureClass": {"internet"},
		"annotation": {"owner=team-a"}, "line": {"12"},
	}, nil, nil)

	if got := c.Digest(withExtra, nil); got != want {
		t.Errorf("a facet outside the contract changed the digest: %s != %s", got, want)
	}

	// The identity is the key of the item and not part of the digest.
	renamed := base
	renamed.Identity = "prod/ingress/other"

	if got := c.Digest(renamed, nil); got != want {
		t.Errorf("identity changed the digest")
	}

	// A facet outside the contract stays on a non-reference item for explanation.
	if _, ok := withExtra.Facets["annotation"]; !ok {
		t.Error("non-reference item lost its extra facet")
	}
}

func TestDigestChangesWhenContractFieldChanges(t *testing.T) {
	c := impact.V1()
	base := c.Digest(routeItem(c, "api.example.com"), nil)

	for _, k := range impact.Kinds() {
		for _, field := range c.FieldsOf(k) {
			item := fixtureFor(c, k)
			before := c.Digest(item, nil)

			changed := item
			changed.Facets = impact.Facets{}
			maps.Copy(changed.Facets, item.Facets)
			changed.Facets[field] = []string{"changed"}

			if after := c.Digest(changed, nil); after == before {
				t.Errorf("%s.%s: digest did not change", k, field)
			}
		}
	}

	if got := c.Digest(routeItem(c, "other.example.com"), nil); got == base {
		t.Error("host change did not change the digest")
	}

	if !strings.HasPrefix(base, "sha256:") || len(base) != len("sha256:")+64 {
		t.Errorf("digest = %q", base)
	}
}

func TestDigestStableAndOrderFree(t *testing.T) {
	c := impact.V1()

	a := c.NewItem(impact.KindRuntimeIdentity, "prod/deploy/api", impact.Facets{
		"serviceAccount": {"api"}, "roleReferences": {"view", "edit", "view"},
	}, nil, nil)
	b := c.NewItem(impact.KindRuntimeIdentity, "prod/deploy/api", impact.Facets{
		"roleReferences": {"edit", "view"}, "serviceAccount": {"api"},
	}, nil, nil)

	first, second := c.Digest(a, nil), c.Digest(a, nil)
	if first != second {
		t.Errorf("two runs differ: %s %s", first, second)
	}

	if c.Digest(b, nil) != first {
		t.Error("order or duplicates of a facet changed the digest")
	}
}

// A22 (digest side): empty fields do not enter the digest, so a field added to a contract keeps the
// digest of an item that has no value for it.
func TestDigestOmitsEmptyFields(t *testing.T) {
	c := impact.V1()
	with := c.NewItem(impact.KindProtocol, "x", impact.Facets{"protocol": {"https"}, "port": {"443"}, "flow": {}}, nil, nil)
	without := c.NewItem(impact.KindProtocol, "x", impact.Facets{"protocol": {"https"}, "port": {"443"}}, nil, nil)

	if c.Digest(with, nil) != c.Digest(without, nil) {
		t.Error("an empty facet changed the digest")
	}
}

// assertNoSecretValue fails when the JSON form of v holds the secret value.
func assertNoSecretValue(t *testing.T, what string, v any) {
	t.Helper()

	data, err := json.Marshal(v)
	mustOK(t, err)
	checkTrue(t, !bytes.Contains(data, []byte(secretValue)), what+" holds the secret value: "+string(data))
}

// assertRedactedOutsideContract checks that the records name exactly the wanted properties, each
// with the outside-contract class and no value.
func assertRedactedOutsideContract(t *testing.T, records []redact.Record, wantProperties []string) {
	t.Helper()

	props := make([]string, 0, len(records))

	for _, r := range records {
		checkEq(t, "record class", r.Class, impact.ClassOutsideContract)

		props = append(props, r.Property)
	}

	checkSlice(t, "redacted properties", props, wantProperties)
}

// Scenario: Secret value in a manifest.
// The item holds the reference name and role, no output holds the value, and the outside-contract facets are recorded
// by name and class.
func TestNeverContainsSecretValue(t *testing.T) {
	c := impact.V1()

	item := c.NewItem(impact.KindSecretReference, "prod/deploy/api/env/DB", impact.Facets{
		"reference": {"db-credentials"},
		"role":      {"env"},
		"consumer":  {"prod/deploy/api/container/app"},
		// Values that a manifest can carry next to a reference. None may survive.
		"value":    {secretValue},
		"password": {secretValue},
	}, nil, nil)

	assertNoSecretValue(t, "item JSON", item)

	_, kept := item.Facets["value"]
	checkTrue(t, !kept, "reference item kept a facet outside the contract")

	// The two facets are recorded by name and class, never by value.
	assertRedactedOutsideContract(t, item.Redactions, []string{"password", "value"})

	// The digest equals that of the item without the secret facets.
	clean := c.NewItem(impact.KindSecretReference, "prod/deploy/api/env/DB", impact.Facets{
		"reference": {"db-credentials"}, "role": {"env"}, "consumer": {"prod/deploy/api/container/app"},
	}, nil, nil)
	checkEq(t, "digest of the item with secret facets", c.Digest(item, nil), c.Digest(clean, nil))

	sealed, err := c.Seal(item, func(string) (string, bool, error) { return "", false, nil })
	mustOK(t, err)
	assertNoSecretValue(t, "sealed JSON", sealed)
}

// Scenario: Secret value in a manifest.
// A secret-shaped value in a contract field is replaced by its marker and the redaction is recorded.
func TestSecretShapedValueInContractFieldIsRedacted(t *testing.T) {
	c := impact.V1()
	token := "ghp_" + strings.Repeat("a1B2", 9)

	item := c.NewItem(impact.KindConfigurationReference, "prod/deploy/api/envFrom/cfg", impact.Facets{
		"reference": {token}, "role": {"envFrom"},
	}, nil, nil)

	data, err := json.Marshal(item)
	mustOK(t, err)

	checkTrue(t, !bytes.Contains(data, []byte(token)), "token survived: "+string(data))
	checkSlice(t, "reference", item.Facets["reference"], []string{redact.Marker(redact.ClassToken)})
	requireEq(t, "redactions", len(item.Redactions), 1)
	checkEq(t, "redaction property", item.Redactions[0].Property, "reference")
	checkEq(t, "redaction class", item.Redactions[0].Class, redact.ClassToken)
	checkTrue(t, !bytes.Contains(data, []byte(`"Value"`)), "a redaction record must not carry a value field")
}

// sealOrFail seals the item and stops the test on an error.
func sealOrFail(t *testing.T, c impact.Contract, item *impact.Item, digester func(string) (string, bool, error)) impact.Sealed {
	t.Helper()

	sealed, err := c.Seal(*item, digester)
	mustOK(t, err)

	return sealed
}

// Scenario: SEC-03 authorization code change without topology change.
// A22: a content change of an implementation file changes the item digest while the topology item
// (same facets, no refs) stays equal.
func TestImplementationRefsDigestFileContent(t *testing.T) {
	c := impact.V1()
	files := map[string]string{"auth/policy.go": "sha256:aaa", "auth/policy_test.go": "sha256:t1"}
	digester := func(path string) (string, bool, error) {
		d, ok := files[path]

		return d, ok, nil
	}

	topology := routeItem(c, "api.example.com")
	withCode := c.NewItem(impact.KindImplementation, "authz", impact.Facets{"subject": {"authorization"}}, []string{"auth/policy.go"}, []string{"auth/policy_test.go"})

	topoBefore := sealOrFail(t, c, &topology, digester)
	codeBefore := sealOrFail(t, c, &withCode, digester)

	checkTrue(t, codeBefore.Unknown == nil, "the sealed code item is unknown")
	checkTrue(t, codeBefore.Digest != "", "the sealed code item has no digest")
	requireEq(t, "refs", len(codeBefore.Refs), 2)
	checkEq(t, "first ref role", codeBefore.Refs[0].Role, impact.RefImplementation)
	checkEq(t, "second ref role", codeBefore.Refs[1].Role, impact.RefVerification)

	files["auth/policy.go"] = "sha256:bbb"

	topoAfter := sealOrFail(t, c, &topology, digester)
	codeAfter := sealOrFail(t, c, &withCode, digester)

	checkEq(t, "topology item digest", topoAfter.Digest, topoBefore.Digest)
	checkTrue(t, codeAfter.Digest != codeBefore.Digest, "implementation file change did not change the item digest")

	// A verification file change counts too.
	files["auth/policy_test.go"] = "sha256:t2"

	again := sealOrFail(t, c, &withCode, digester)
	checkTrue(t, again.Digest != codeAfter.Digest, "verification file change did not change the item digest")
}

// Scenario: Missing implementation evidence path.
// Scenario: Reference path is missing.
func TestMissingReferencePathIsUnknown(t *testing.T) {
	c := impact.V1()
	digester := func(path string) (string, bool, error) {
		return "sha256:aaa", path == "auth/policy.go", nil
	}

	item := c.NewItem(impact.KindImplementation, "authz", impact.Facets{"subject": {"authorization"}},
		[]string{"auth/policy.go", "auth/removed.go"}, []string{"auth/missing_test.go"})

	sealed := sealOrFail(t, c, &item, digester)
	if sealed.Unknown == nil {
		t.Fatal("unknown = nil")
	}

	checkEq(t, "unknown reason", sealed.Unknown.Reason, "reference-unresolved")
	checkEq(t, "digest", sealed.Digest, "")

	for _, p := range []string{"auth/removed.go", "auth/missing_test.go"} {
		checkContains(t, sealed.Unknown.Detail, p)
	}

	e := sealed.Entry()
	checkEq(t, "entry reason", e.Reason, impact.ReasonReferenceUnresolved)
	checkEq(t, "entry digest", e.Digest, "")
}

func TestSealErrors(t *testing.T) {
	c := impact.V1()

	_, err := c.Seal(impact.Item{Kind: "firewall-rule"}, nil)
	checkErrIs(t, err, impact.ErrUnknownKind)

	boom := errors.New("boom")
	item := c.NewItem(impact.KindImplementation, "x", nil, []string{"a.go"}, nil)

	_, err = c.Seal(item, func(string) (string, bool, error) { return "", false, boom })
	checkErrIs(t, err, boom)
}

type fakeFacts struct {
	files map[string]string
	rev   string
}

func (f *fakeFacts) Items(context.Context, impact.Selector) (impact.Match, error) {
	return impact.Match{State: impact.MatchEmpty, Items: []impact.Item{}}, nil
}

func (f *fakeFacts) FileDigest(_ context.Context, revision, path string) (digest string, found bool, err error) {
	f.rev = revision
	d, ok := f.files[path]

	return d, ok, nil
}

func (f *fakeFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{ID: "cbm-0.12.1", Kinds: []impact.Kind{impact.KindPublicEntryPoint}, Selectors: map[impact.Kind][]string{impact.KindPublicEntryPoint: {"host"}}}
}

func (f *fakeFacts) EvaluatorVersions() map[string]string {
	return map[string]string{impact.EvaluatorNormalization: "1"}
}

var _ impact.FactSource = (*fakeFacts)(nil)

func TestFileDigesterAtReadsAtTheRevision(t *testing.T) {
	facts := &fakeFacts{files: map[string]string{"a.go": "sha256:1"}}
	read := impact.FileDigesterAt(context.Background(), facts, "abc123")

	d, ok, err := read("a.go")
	mustOK(t, err)
	checkTrue(t, ok, "an existing file was reported missing")
	checkEq(t, "digest", d, "sha256:1")
	checkEq(t, "revision", facts.rev, "abc123")

	_, ok, _ = read("gone.go")
	checkTrue(t, !ok, "missing file reported found")

	p := facts.Profile()
	checkTrue(t, p.Supports(impact.KindPublicEntryPoint), "the profile lacks public-entry-point")
	checkTrue(t, !p.Supports(impact.KindProtocol), "the profile supports protocol")
	checkTrue(t, p.SupportsSelector(impact.KindPublicEntryPoint, "host"), "the profile lacks the host selector")
	checkTrue(t, !p.SupportsSelector(impact.KindPublicEntryPoint, "path"), "the profile supports the path selector")
}

func TestSealAllSorts(t *testing.T) {
	c := impact.V1()
	items := []impact.Item{routeItem(c, "b"), c.NewItem(impact.KindBinding, "z", nil, nil, nil), c.NewItem(impact.KindBinding, "a", nil, nil, nil)}

	sealed, err := c.SealAll(items, nil)
	mustOK(t, err)

	got := make([]string, 0, len(sealed))
	for _, s := range sealed {
		got = append(got, string(s.Item.Kind)+"/"+s.Item.Identity)
	}

	checkSlice(t, "order", got, []string{"binding/a", "binding/z", "public-entry-point/prod/ingress/api"})
}

func TestBuiltinRegistry(t *testing.T) {
	reg := impact.Builtin()
	checkSlice(t, "versions", reg.Versions(), []string{impact.ContractVersion})

	_, ok := reg.Lookup(impact.ContractVersion)
	checkTrue(t, ok, "v1 missing")

	// An approval bound under an unknown version is affected.
	report := reg.DigestImpact(impact.ContractVersion, []impact.Bound{{Assessment: "A@1", Approval: "p", Contract: "review-digest/0"}})
	checkEq(t, "affected", len(report.Affected), 1)
}
