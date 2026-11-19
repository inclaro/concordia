package trust_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/trust"
)

func decl(class string, kv ...string) trust.Declaration {
	d := trust.Declaration{Classification: class}

	for i := 0; i+1 < len(kv); i += 2 {
		if d.Dimensions == nil {
			d.Dimensions = map[string]string{}
		}

		d.Dimensions[kv[i]] = kv[i+1]
	}

	return d
}

func byDimension(t *testing.T, crossings []trust.Crossing, dimension string) trust.Crossing {
	t.Helper()

	for i := range crossings {
		if crossings[i].Dimension == dimension {
			return crossings[i]
		}
	}

	t.Fatalf("no crossing for dimension %q in %v", dimension, crossings)

	return trust.Crossing{}
}

type crossingCase struct {
	check func(t *testing.T, got []trust.Crossing)
	want  map[string]trust.State
	name  string
	flow  trust.Flow
}

func checkZoneEndsUnknown(t *testing.T, got []trust.Crossing) {
	t.Helper()

	z := byDimension(t, got, "zone")
	if z.From != trust.Unknown {
		t.Fatalf("zone ends = %q/%q, want unknown/unknown", z.From, z.To)
	}

	if z.To != trust.Unknown {
		t.Fatalf("zone ends = %q/%q, want unknown/unknown", z.From, z.To)
	}
}

func checkDirectionToUnknown(t *testing.T, got []trust.Crossing) {
	t.Helper()

	if z := byDimension(t, got, "zone"); z.Direction != "internal → unknown" {
		t.Fatalf("direction = %q", z.Direction)
	}
}

func checkDirectionKept(t *testing.T, got []trust.Crossing) {
	t.Helper()

	z := byDimension(t, got, "zone")
	if z.Direction != "internet → internal" {
		t.Fatalf("zone crossing = %+v", z)
	}

	if z.From != "internet" || z.To != "internal" {
		t.Fatalf("zone crossing = %+v", z)
	}
}

func checkObservedExternalUnknown(t *testing.T, got []trust.Crossing) {
	t.Helper()

	for i := range got {
		c := &got[i]

		if !c.ExternalDestination {
			t.Fatalf("crossing = %+v", c)
		}

		if c.State != trust.StateUnknown || c.Kind != trust.FlowObserved {
			t.Fatalf("crossing = %+v", c)
		}
	}
}

func checkClassificationUnknown(t *testing.T, got []trust.Crossing) {
	t.Helper()

	for i := range got {
		if got[i].Classification != "unknown" {
			t.Fatalf("classification = %q, want unknown", got[i].Classification)
		}
	}
}

func checkClassificationUnclassified(t *testing.T, got []trust.Crossing) {
	t.Helper()

	if got[0].Classification != "unclassified" {
		t.Fatalf("classification = %q", got[0].Classification)
	}
}

func declaredCrossingCases() []crossingCase {
	return []crossingCase{
		{
			// Scenario: SEC-01 one flow with many boundaries.
			name: "SEC-01 one flow with many boundaries",
			flow: trust.Flow{
				ID:          "rel-1",
				Source:      trust.Endpoint{ID: "a", Trust: decl("", "zone", "internet", "account", "a1")},
				Destination: trust.Endpoint{ID: "b", Trust: decl("", "zone", "internal", "account", "a2")},
			},
			want: map[string]trust.State{
				"zone": trust.StateCrossing, "account": trust.StateCrossing,
				"organization": trust.StateUnknown, "principal": trust.StateUnknown, "region": trust.StateUnknown,
			},
		},
		{
			name: "equal known values are same",
			flow: trust.Flow{
				ID:          "rel-2",
				Source:      trust.Endpoint{ID: "a", Trust: decl("", "region", "eu")},
				Destination: trust.Endpoint{ID: "b", Trust: decl("", "region", "eu")},
			},
			want: map[string]trust.State{"region": trust.StateSame, "zone": trust.StateUnknown},
		},
	}
}

func directionCrossingCases() []crossingCase {
	return []crossingCase{
		{
			// Scenario: SEC-02 namespace name does not prove isolation.
			name: "SEC-02 namespace name gives unknown",
			flow: trust.Flow{
				ID:          "rel-3",
				Source:      trust.Endpoint{ID: "trusted/workload"},
				Destination: trust.Endpoint{ID: "untrusted/workload"},
			},
			want:  map[string]trust.State{"zone": trust.StateUnknown},
			check: checkZoneEndsUnknown,
		},
		{
			name: "one end without a value is unknown",
			flow: trust.Flow{
				ID:          "rel-4",
				Source:      trust.Endpoint{ID: "a", Trust: decl("", "zone", "internal")},
				Destination: trust.Endpoint{ID: "b"},
			},
			want:  map[string]trust.State{"zone": trust.StateUnknown},
			check: checkDirectionToUnknown,
		},
		{
			// Scenario: SEC-02 direction is kept.
			name: "SEC-02 direction is kept",
			flow: trust.Flow{
				ID:          "rel-5",
				Source:      trust.Endpoint{ID: "a", Trust: decl("", "zone", "internet")},
				Destination: trust.Endpoint{ID: "b", Trust: decl("", "zone", "internal")},
			},
			want:  map[string]trust.State{"zone": trust.StateCrossing},
			check: checkDirectionKept,
		},
	}
}

func classificationCrossingCases() []crossingCase {
	return []crossingCase{
		{
			// Scenario: SEC-02 observed flow without trust context.
			name: "SEC-02 observed destination without trust is unknown and external",
			flow: trust.Flow{
				ID: "observed:a->x", Kind: trust.FlowObserved,
				Source:      trust.Endpoint{ID: "a", Trust: decl("", "zone", "internal")},
				Destination: trust.Endpoint{ID: "x", External: true},
			},
			want:  map[string]trust.State{"zone": trust.StateUnknown},
			check: checkObservedExternalUnknown,
		},
		{
			// Scenario: SEC-01 no classification declared.
			name:  "SEC-01 missing classification stays unknown",
			flow:  trust.Flow{ID: "rel-6", Source: trust.Endpoint{ID: "a"}, Destination: trust.Endpoint{ID: "b"}},
			want:  map[string]trust.State{"zone": trust.StateUnknown},
			check: checkClassificationUnknown,
		},
		{
			// Scenario: Missing and explicitly empty differ.
			name:  "explicit unclassified differs from unknown",
			flow:  trust.Flow{ID: "rel-7", Classification: "unclassified", Source: trust.Endpoint{ID: "a"}, Destination: trust.Endpoint{ID: "b"}},
			want:  map[string]trust.State{"zone": trust.StateUnknown},
			check: checkClassificationUnclassified,
		},
	}
}

// runCrossingCase checks the crossings of one flow against the states and the extra check of the case.
func runCrossingCase(t *testing.T, vocab trust.Vocabulary, tc *crossingCase) {
	t.Helper()

	got := trust.Crossings(vocab, &tc.flow)
	if len(got) != len(vocab.Dimensions()) {
		t.Fatalf("got %d crossings, want one per dimension (%d)", len(got), len(vocab.Dimensions()))
	}

	for dimension, state := range tc.want {
		if c := byDimension(t, got, dimension); c.State != state {
			t.Errorf("%s: state = %q, want %q", dimension, c.State, state)
		}
	}

	if tc.check != nil {
		tc.check(t, got)
	}
}

func TestCrossings(t *testing.T) {
	t.Parallel()

	vocab := trust.NewVocabulary(nil, []string{"public", "internal", "restricted", "unclassified"})
	tests := slices.Concat(declaredCrossingCases(), directionCrossingCases(), classificationCrossingCases())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runCrossingCase(t, vocab, &tc)
		})
	}
}

func TestCrossingsReverseCallIsSeparate(t *testing.T) {
	t.Parallel()

	vocab := trust.NewVocabulary(nil, nil)
	out := trust.Endpoint{ID: "a", Trust: decl("", "zone", "internet")}
	in := trust.Endpoint{ID: "b", Trust: decl("", "zone", "internal")}

	forward := byDimension(t, trust.Crossings(vocab, &trust.Flow{ID: "f", Source: out, Destination: in}), "zone")
	reverse := byDimension(t, trust.Crossings(vocab, &trust.Flow{ID: "r", Source: in, Destination: out}), "zone")

	if forward.Direction != "internet → internal" || reverse.Direction != "internal → internet" {
		t.Fatalf("directions = %q, %q", forward.Direction, reverse.Direction)
	}
}

func TestCrossingsDeclaredDimensionsAndOrder(t *testing.T) {
	t.Parallel()

	vocab := trust.NewVocabulary([]string{"tenant", "zone"}, nil)
	got := trust.Crossings(vocab, &trust.Flow{
		ID:          "f",
		Source:      trust.Endpoint{ID: "a", Trust: decl("", "tenant", "t1")},
		Destination: trust.Endpoint{ID: "b", Trust: decl("", "tenant", "t2")},
	})

	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Dimension
	}

	want := []string{"account", "organization", "principal", "region", "tenant", "zone"}
	if !slices.Equal(names, want) {
		t.Fatalf("dimensions = %v, want %v", names, want)
	}

	if c := byDimension(t, got, "tenant"); c.State != trust.StateCrossing {
		t.Fatalf("tenant = %+v", c)
	}
}

func TestSortIsStable(t *testing.T) {
	t.Parallel()

	in := []trust.Crossing{
		{Flow: "b", Dimension: "zone"}, {Flow: "a", Dimension: "zone"}, {Flow: "a", Dimension: "account"},
	}
	trust.Sort(in)

	got := []string{in[0].Flow + "/" + in[0].Dimension, in[1].Flow + "/" + in[1].Dimension, in[2].Flow + "/" + in[2].Dimension}
	if !slices.Equal(got, []string{"a/account", "a/zone", "b/zone"}) {
		t.Fatalf("order = %v", got)
	}
}

func requireSingleProblem(t *testing.T, label string, problems []trust.Problem) trust.Problem {
	t.Helper()

	if len(problems) != 1 {
		t.Fatalf("%s problems = %v", label, problems)
	}

	return problems[0]
}

// requireUndeclaredDimension checks Scenario: SEC-01 undeclared dimension name.
func requireUndeclaredDimension(t *testing.T, vocab trust.Vocabulary) {
	t.Helper()

	problems := vocab.Check(decl("", "firewall", "x"))
	problem := requireSingleProblem(t, "undeclared dimension", problems)

	if problem.Kind != trust.UndeclaredDimension {
		t.Fatalf("undeclared dimension problems = %v", problems)
	}

	if problem.Name != "firewall" {
		t.Fatalf("undeclared dimension problems = %v", problems)
	}
}

// requireUnlistedClassification checks Scenario: SEC-01 classification outside the declared list.
func requireUnlistedClassification(t *testing.T, vocab trust.Vocabulary) {
	t.Helper()

	problems := vocab.Check(decl("secret"))
	problem := requireSingleProblem(t, "classification", problems)

	if problem.Kind != trust.UnlistedClassification {
		t.Fatalf("classification problems = %v", problems)
	}

	if problem.Value != "secret" {
		t.Fatalf("classification problems = %v", problems)
	}
}

func requireZeroVocabularyAcceptsBuiltIns(t *testing.T) {
	t.Helper()

	var zero trust.Vocabulary
	if !zero.HasDimension(trust.DimensionRegion) {
		t.Fatalf("zero vocabulary must accept built-ins")
	}

	if zero.HasClassification("public") {
		t.Fatalf("zero vocabulary must accept no classification")
	}
}

func TestVocabularyCheck(t *testing.T) {
	t.Parallel()

	vocab := trust.NewVocabulary([]string{"tenant"}, []string{"public", "internal", "restricted"})

	if p := vocab.Check(decl("restricted", "zone", "internal", "tenant", "t1")); len(p) != 0 {
		t.Fatalf("valid declaration gave problems: %v", p)
	}

	requireUndeclaredDimension(t, vocab)
	requireUnlistedClassification(t, vocab)

	if len(vocab.Check(decl("", "zone", ""))) != 1 {
		t.Fatalf("an empty value is a problem")
	}

	requireZeroVocabularyAcceptsBuiltIns(t)
}

// requireValues checks the listed dimension values of a merged declaration.
func requireValues(t *testing.T, label string, got trust.Declaration, values map[string]string) {
	t.Helper()

	for dimension, want := range values {
		if got.Value(dimension) != want {
			t.Fatalf("%s = %+v", label, got)
		}
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()

	got, conflicts := trust.Merge(decl("internal", "zone", "internal", "account", "a1"), decl("", "zone", "internal", "region", "eu"))
	if len(conflicts) != 0 {
		t.Fatalf("merge conflicts = %v", conflicts)
	}

	requireValues(t, "merge", got, map[string]string{"zone": "internal", "region": "eu"})

	if got.Classification != "internal" {
		t.Fatalf("merge = %+v", got)
	}

	got, conflicts = trust.Merge(decl("public", "zone", "a"), decl("internal", "zone", "b"))
	if !slices.Equal(conflicts, []string{"zone", "classification"}) {
		t.Fatalf("conflicting merge conflicts = %v", conflicts)
	}

	requireValues(t, "conflicting merge", got, map[string]string{"zone": ""})

	if got.Classification != "" {
		t.Fatalf("conflicting merge = %+v", got)
	}
}

func TestDependencyKinds(t *testing.T) {
	t.Parallel()

	kinds := trust.DependencyKinds()
	if len(kinds) != 17 {
		t.Fatalf("got %d kinds, want 17", len(kinds))
	}

	for _, k := range kinds {
		if !trust.IsDependencyKind(k) {
			t.Errorf("%q not recognized", k)
		}
	}

	if trust.IsDependencyKind("firewall-rule") {
		t.Fatal("firewall-rule must not be a dependency kind")
	}
}
