package policy_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
)

func TestTriNot(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, not policy.Tri
	}{
		{policy.TriTrue, policy.TriFalse},
		{policy.TriFalse, policy.TriTrue},
		{policy.TriUnknown, policy.TriUnknown},
	} {
		if got := tc.in.Not(); got != tc.not {
			t.Errorf("Not(%s) = %s, want %s", tc.in, got, tc.not)
		}
	}
}

func TestTriAndOrOverEveryPair(t *testing.T) {
	t.Parallel()

	all := []policy.Tri{policy.TriFalse, policy.TriTrue, policy.TriUnknown}

	for _, a := range all {
		for _, b := range all {
			pair := []policy.Tri{a, b}

			if got, want := policy.And(a, b), triPairResult(pair, policy.TriFalse, policy.TriTrue); got != want {
				t.Errorf("And(%s, %s) = %s, want %s", a, b, got, want)
			}

			if got, want := policy.Or(a, b), triPairResult(pair, policy.TriTrue, policy.TriFalse); got != want {
				t.Errorf("Or(%s, %s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

// triPairResult gives the result of a three-valued connective: decisive when an operand is the decisive value,
// otherwise unknown when an operand is unknown, otherwise neutral.
func triPairResult(operands []policy.Tri, decisive, neutral policy.Tri) policy.Tri {
	switch {
	case slices.Contains(operands, decisive):
		return decisive
	case slices.Contains(operands, policy.TriUnknown):
		return policy.TriUnknown
	}

	return neutral
}

func TestTriEmptyOperandListsAndTheZeroValue(t *testing.T) {
	t.Parallel()

	if policy.And() != policy.TriTrue || policy.Or() != policy.TriFalse {
		t.Errorf("And() = %s, Or() = %s", policy.And(), policy.Or())
	}

	var zero policy.Tri
	if zero != policy.TriUnknown {
		t.Errorf("the zero value is %s, want unknown", zero)
	}
}

func TestA07NotOfAnUnknownPropertyStaysUnknown(t *testing.T) {
	t.Parallel()

	expr := policy.Not(policy.Match(policy.DimensionProtocol, "https"))

	// The protocol of the fact is unknown: the context has no value for it.
	got := expr.Eval(policy.StaticValues{policy.DimensionEnvironment: {policy.PropertyValue: "production"}})
	if got.Value != policy.TriUnknown {
		t.Fatalf("not(protocol=https) with an unknown protocol = %s, want unknown", got.Value)
	}

	if want := []policy.Property{{Dimension: policy.DimensionProtocol, Name: policy.PropertyValue}}; !slices.Equal(got.Unknown, want) {
		t.Errorf("unknown = %v, want %v", got.Unknown, want)
	}

	// With a known protocol the negation is decided.
	for protocol, want := range map[string]policy.Tri{"https": policy.TriFalse, "grpc": policy.TriTrue} {
		known := policy.StaticValues{policy.DimensionProtocol: {policy.PropertyValue: protocol}}
		if got := expr.Eval(known).Value; got != want {
			t.Errorf("not(protocol=https) with protocol %s = %s, want %s", protocol, got, want)
		}
	}
}

// anyUnknownCount marks a case that does not check how many properties are unknown.
const anyUnknownCount = -1

func TestAllOfWithAFalseOperandIsFalseAndAnyOfWithATrueOperandIsTrue(t *testing.T) {
	t.Parallel()

	ctx := policy.StaticValues{policy.DimensionEnvironment: {policy.PropertyValue: "staging"}}
	production := policy.Match(policy.DimensionEnvironment, "production")
	https := policy.Match(policy.DimensionProtocol, "https") // unknown in ctx
	staging := policy.Match(policy.DimensionEnvironment, "staging")

	for _, tc := range []struct {
		expr    *policy.SelectorExpr
		name    string
		want    policy.Tri
		unknown int
	}{
		{policy.AllOf(production, https), "allOf(false, unknown)", policy.TriFalse, 0},
		{policy.AllOf(https, production), "allOf(unknown, false)", policy.TriFalse, anyUnknownCount},
		{policy.AllOf(staging, https), "allOf(true, unknown)", policy.TriUnknown, 1},
		{policy.AnyOf(staging, https), "anyOf(true, unknown)", policy.TriTrue, anyUnknownCount},
		{policy.AnyOf(production, https), "anyOf(false, unknown)", policy.TriUnknown, anyUnknownCount},
		{policy.AnyOf(production, production), "anyOf(false, false)", policy.TriFalse, anyUnknownCount},
	} {
		got := tc.expr.Eval(ctx)
		if got.Value != tc.want {
			t.Errorf("%s = %+v, want %s", tc.name, got, tc.want)
		}

		if tc.unknown != anyUnknownCount && len(got.Unknown) != tc.unknown {
			t.Errorf("%s = %+v, want %d unknown properties", tc.name, got, tc.unknown)
		}
	}
}

func TestOperatorsNest(t *testing.T) {
	t.Parallel()

	expr := policy.AllOf(
		policy.Match(policy.DimensionEnvironment, "production"),
		policy.AnyOf(policy.Match(policy.DimensionProtocol, "https"), policy.Match(policy.DimensionProtocol, "grpc")),
		policy.Not(policy.AllOf(policy.Match(policy.DimensionOperation, "admin"))),
	)

	for _, tc := range []struct {
		name                string
		protocol, operation string
		want                policy.Tri
	}{
		{"https read", "https", "read", policy.TriTrue},
		{"https admin", "https", "admin", policy.TriFalse},
		{"tcp read", "tcp", "read", policy.TriFalse},
	} {
		values := policy.StaticValues{
			policy.DimensionEnvironment: {policy.PropertyValue: "production"},
			policy.DimensionProtocol:    {policy.PropertyValue: tc.protocol},
			policy.DimensionOperation:   {policy.PropertyValue: tc.operation},
		}

		if got := expr.Eval(values).Value; got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}

	if want := "allOf(environment=production,anyOf(protocol=https,protocol=grpc),not(allOf(operation=admin)))"; expr.String() != want {
		t.Errorf("String() = %s, want %s", expr.String(), want)
	}
}

func TestALeafWithSeveralPropertiesNeedsAllAndAcceptsAnyValueOfOne(t *testing.T) {
	t.Parallel()

	expr := policy.MatchProperties(policy.DimensionWorkload, map[string][]string{
		"namespace": {"payments", "billing"},
		"kind":      {"Deployment"},
	})

	for _, tc := range []struct {
		namespace, kind string
		missing         []string
		want            policy.Tri
	}{
		{"billing", "Deployment", nil, policy.TriTrue},
		{"billing", "StatefulSet", nil, policy.TriFalse},
		{"other", "", nil, policy.TriFalse}, // a known false property decides
		{"billing", "", []string{"workload.kind"}, policy.TriUnknown},
		{"", "", []string{"workload.kind", "workload.namespace"}, policy.TriUnknown},
	} {
		got := expr.Eval(workloadSelectorValues(tc.namespace, tc.kind))
		if got.Value != tc.want {
			t.Errorf("%+v: value = %s, want %s", tc, got.Value, tc.want)
		}

		if names := unknownPropertyNames(got); !slices.Equal(names, tc.missing) {
			t.Errorf("%+v: unknown = %v, want %v", tc, names, tc.missing)
		}
	}
}

// workloadSelectorValues gives a context with the workload properties that are not empty.
func workloadSelectorValues(namespace, kind string) policy.StaticValues {
	values := map[string]string{}
	if namespace != "" {
		values["namespace"] = namespace
	}

	if kind != "" {
		values["kind"] = kind
	}

	return policy.StaticValues{policy.DimensionWorkload: values}
}

// unknownPropertyNames gives the names of the unknown properties of an evaluation.
func unknownPropertyNames(got policy.Evaluation) []string {
	names := make([]string, 0, len(got.Unknown))
	for _, p := range got.Unknown {
		names = append(names, p.String())
	}

	return names
}

func TestDimensionsWithoutASourceAreUnknownUntilAResolverIsSupplied(t *testing.T) {
	t.Parallel()

	for _, dim := range []policy.Dimension{policy.DimensionTrustContext, policy.DimensionDataClassification, policy.DimensionGovernanceScope} {
		expr := policy.Match(dim, "internet")

		// A context that has resolvers for other dimensions, a nil context: unknown, never false.
		withOthers := policy.ResolverSet{policy.DimensionEnvironment: func(string) (string, bool) { return "production", true }}
		if got := expr.Eval(withOthers).Value; got != policy.TriUnknown {
			t.Errorf("%s without a resolver = %s, want unknown", dim, got)
		}

		if got := expr.Eval(nil).Value; got != policy.TriUnknown {
			t.Errorf("%s with a nil context = %s, want unknown", dim, got)
		}

		if got := policy.Not(expr).Eval(nil).Value; got != policy.TriUnknown {
			t.Errorf("not(%s) = %s, want unknown", dim, got)
		}

		// The owning change supplies the resolver.
		withResolver := policy.ResolverSet{dim: func(property string) (string, bool) { return "internet", property == policy.PropertyValue }}
		if got := expr.Eval(withResolver).Value; got != policy.TriTrue {
			t.Errorf("%s with a resolver = %s, want true", dim, got)
		}
	}
}

func TestANilExpressionIsTrueAndTheExpressionMarshalsAsJSON(t *testing.T) {
	t.Parallel()

	var none *policy.SelectorExpr
	if got := none.Eval(nil).Value; got != policy.TriTrue {
		t.Errorf("nil expression = %s, want true", got)
	}

	data, err := json.Marshal(policy.Not(policy.Match(policy.DimensionEnvironment, "production")))
	if err != nil {
		t.Fatal(err)
	}

	want := `{"op":"not","operands":[{"op":"match","dimension":"environment","match":{"value":["production"]}}]}`
	if string(data) != want {
		t.Errorf("json = %s, want %s", data, want)
	}
}

func TestDimensionsListAllSelectorDimensions(t *testing.T) {
	t.Parallel()

	want := []policy.Dimension{
		"architecture", "container", "dataClassification", "environment", "evidenceRole", "governanceScope", "interface",
		"observedKind", "operation", "protocol", "repository", "trustContext", "workload",
	}
	if got := policy.Dimensions(); !slices.Equal(got, want) {
		t.Errorf("Dimensions() = %v, want %v", got, want)
	}

	if got := policy.DimensionWorkload.Properties(); !slices.Equal(got, []string{"cluster", "kind", "name", "namespace"}) {
		t.Errorf("workload properties = %v", got)
	}
}
