package policy_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func TestSelectorKeysCalmNodesNodeTypesTagsAndRefKeepUnionSemantics(t *testing.T) {
	t.Parallel()

	res := compileTyped(t, pack(rule("    subject: {calmNodes: [db], nodeTypes: [actor], tags: [core], ref: named}\n    declaredOnly: {edges: [IMPORTS]}\n"),
		"selectors:\n  named: {calmNodes: [fulfillment]}\n"))
	wantCodes(t, &res)

	if got := res.Plans[0].Subjects; !slices.Equal(got, []string{"billing", "db", "fulfillment", "ledger", "user"}) {
		t.Errorf("subjects = %v", got)
	}

	// The same union inside an expression is one leaf on the CALM identity.
	typed := typedBody(t, "    subject: {calmNodes: [billing]}\n    obligation:\n      type: required\n"+
		"      relation: {kind: uses-image, direction: from-workload}\n      applies:\n        anyOf:\n"+
		"          - {calmNodes: [db], nodeTypes: [actor], tags: [core], ref: named}\n          - {environment: production}\n",
		"selectors:\n  named: {calmNodes: [fulfillment]}\n")
	wantCodes(t, &typed)

	applies := typed.Plans[0].Obligation.Applies
	if want := policy.AnyOf(
		policy.MatchProperties(policy.DimensionArchitecture, map[string][]string{"id": {"billing", "db", "fulfillment", "ledger", "user"}}),
		policy.Match(policy.DimensionEnvironment, "production"),
	); !reflect.DeepEqual(applies, want) {
		t.Errorf("applies = %s, want %s", applies, want)
	}

	ctx := func(id string) policy.StaticValues {
		return policy.StaticValues{policy.DimensionArchitecture: {"id": id}}
	}
	if applies.Eval(ctx("ledger")).Value != policy.TriTrue || applies.Eval(ctx("other")).Value != policy.TriUnknown {
		t.Errorf("eval = %v / %v", applies.Eval(ctx("ledger")), applies.Eval(ctx("other")))
	}
}

func TestCompilesPOL01EnvironmentAndWorkloadSelectorsWithOperators(t *testing.T) {
	t.Parallel()

	res := typedBody(t, "    subject: {tags: [core]}\n    obligation:\n      type: forbidden\n      relation: {kind: routes-to, direction: to-workload}\n"+
		"      applies:\n        allOf:\n          - {environment: production}\n          - {workload: {namespace: payments, kind: [Deployment, StatefulSet]}}\n"+
		"          - not: {protocol: [http]}\n          - anyOf:\n              - {architecture: {nodeType: service}}\n              - {evidenceRole: parsed-source-fact}\n")
	wantCodes(t, &res)

	want := policy.AllOf(
		policy.Match(policy.DimensionEnvironment, "production"),
		policy.MatchProperties(policy.DimensionWorkload, map[string][]string{"namespace": {"payments"}, "kind": {"Deployment", "StatefulSet"}}),
		policy.Not(policy.Match(policy.DimensionProtocol, "http")),
		policy.AnyOf(
			policy.MatchProperties(policy.DimensionArchitecture, map[string][]string{"nodeType": {"service"}}),
			policy.Match(policy.DimensionEvidenceRole, "parsed-source-fact"),
		),
	)
	if got := res.Plans[0].Obligation.Applies; !reflect.DeepEqual(got, want) {
		t.Errorf("applies = %s, want %s", got, want)
	}

	// The workload is in staging: the rule does not apply (false), whatever the unknown operands say.
	staging := policy.StaticValues{policy.DimensionEnvironment: {policy.PropertyValue: "staging"}}
	if got := res.Plans[0].Obligation.Applies.Eval(staging).Value; got != policy.TriFalse {
		t.Errorf("staging = %s, want false", got)
	}
}

func TestANamedSelectorThatIsAnExpressionIsUsableInApplies(t *testing.T) {
	t.Parallel()

	res := typedBody(t, "    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n"+
		"      applies: {allOf: [{ref: prod}, {protocol: https}]}\n",
		"selectors:\n  prod: {environment: production}\n")
	wantCodes(t, &res)

	want := policy.AllOf(policy.Match(policy.DimensionEnvironment, "production"), policy.Match(policy.DimensionProtocol, "https"))
	if got := res.Plans[0].Obligation.Applies; !reflect.DeepEqual(got, want) {
		t.Errorf("applies = %s, want %s", got, want)
	}

	// Used where CALM IDs are needed, the same named selector is rejected.
	bad := compileTyped(t, pack(rule("    subject: {ref: prod}\n    declaredOnly: {edges: [IMPORTS]}\n"), "selectors:\n  prod: {environment: production}\n"))
	wantCodes(t, &bad, diag.POL002)

	cycle := typedBody(t, "    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n"+
		"      applies: {ref: a}\n", "selectors:\n  a: {not: {ref: a}}\n")
	wantCodes(t, &cycle, diag.POL002)
}

func TestRejectsImplicitCompositionAndUnknownSelectorKeysWithPOL002(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"two dimensions":           {appliesRule("{environment: production, protocol: https}"), "no implicit composition"},
		"dimension and operator":   {appliesRule("{environment: production, not: {protocol: https}}"), "no implicit composition"},
		"two operators":            {appliesRule("{allOf: [{environment: production}], anyOf: [{protocol: https}]}"), "no implicit composition"},
		"legacy key and dimension": {appliesRule("{tags: [core], environment: production}"), "no implicit composition"},
		"unknown key":              {appliesRule("{owners: [a]}"), "selector key 'owners' is not supported"},
		"unknown key in operator":  {appliesRule("{allOf: [{environment: production}, {colour: red}]}"), "selector key 'colour' is not supported"},
		"nested implicit":          {appliesRule("{not: {environment: production, protocol: https}}"), "no implicit composition"},
		"bad evidence role":        {appliesRule("{evidenceRole: guess}"), "'evidenceRole' value 'guess' is not an evidence role"},
		"dimension in a subject":   {"    subject: {environment: production}\n    declaredOnly: {edges: [IMPORTS]}\n", "only valid in obligation.applies"},
		"operator in a target": {
			"    subject: {tags: [core]}\n    forbid:\n      target: {not: {tags: [core]}}\n      edges: [IMPORTS]\n", "only valid in obligation.applies",
		},
		"implicit composition in a subject": {"    subject: {tags: [core], protocol: https}\n    declaredOnly: {edges: [IMPORTS]}\n", "no implicit composition"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := typedBody(t, tc.body)
			wantCodes(t, &res, diag.POL002)

			if !strings.Contains(res.Diagnostics[0].Message, tc.want) {
				t.Errorf("message = %q, want %q", res.Diagnostics[0].Message, tc.want)
			}
		})
	}
}

func TestRejectsWrongSelectorValueTypesWithPOL001(t *testing.T) {
	t.Parallel()

	for name, sel := range map[string]string{
		"number value":     "{environment: 5}",
		"empty list":       "{environment: []}",
		"empty allOf":      "{allOf: []}",
		"not as a list":    "{not: [{environment: production}]}",
		"workload string":  "{workload: payments}",
		"workload unknown": "{workload: {colour: red}}",
		"empty workload":   "{workload: {}}",
	} {
		res := typedBody(t, appliesRule(sel))
		if !rejectedAt(&res, diag.POL001, "") {
			t.Errorf("%s: plans %d, diagnostics %+v", name, len(res.Plans), res.Diagnostics)
		}
	}
}

// appliesRule is a typed rule whose obligation applies to the selector sel.
func appliesRule(sel string) string {
	return "    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n      applies: " + sel + "\n"
}

func TestDescribePackShowsTheDeclarationOfTypedRules(t *testing.T) {
	t.Parallel()

	file := pack(rule("    family: deployment\n    enforcement: shadow\n    subject: {tags: [core]}\n    obligation:\n      type: required\n"+
		"      relation: {kind: uses-image, direction: from-workload}\n      scope: {environments: [production]}\n      applies: {environment: production}\n"+
		"    evidence: {roles: [parsed-source-fact], minimumResolution: exact, completeness: complete}\n", "CO-X-001")+
		rule("    subject: {tags: [core]}\n    declaredOnly: {edges: [IMPORTS]}\n", "CO-X-002"),
		"precedence:\n  version: 1\n  order: [CO-X-001]\n")

	info, problems := policy.DescribePack(file)
	if len(problems) != 0 {
		t.Fatalf("problems = %+v", problems)
	}

	requireTypedRuleInfo(t, &info)

	if info.Precedence == nil || info.Precedence.Version != 1 {
		t.Errorf("precedence = %+v", info.Precedence)
	}

	// A typed rule with an implicit composition is a problem of the pack, with no model.
	_, problems = policy.DescribePack(pack(rule(appliesRule("{environment: production, protocol: https}"))))
	if len(problems) != 1 || problems[0].Code != diag.POL002 {
		t.Errorf("problems = %+v", problems)
	}
}

// requireTypedRuleInfo checks the two rules of the pack of TestDescribePackShowsTheDeclarationOfTypedRules.
func requireTypedRuleInfo(t *testing.T, info *policy.PackInfo) {
	t.Helper()

	if len(info.Rules) != 2 {
		t.Fatalf("rules = %+v", info.Rules)
	}

	if info.Rules[1].Obligation != nil {
		t.Fatalf("rules = %+v", info.Rules)
	}

	if info.Rules[0].Family != "deployment" {
		t.Fatalf("rules = %+v", info.Rules)
	}

	if info.Rules[0].Enforcement != policy.EnforcementShadow {
		t.Fatalf("rules = %+v", info.Rules)
	}

	want := "required forAll from-workload uses-image scope=production roles=parsed-source-fact minimumResolution=exact completeness=complete applies=environment=production"
	if got := info.Rules[0].Obligation.Declaration(); got != want {
		t.Errorf("declaration = %q, want %q", got, want)
	}
}

// The schema lists the evidence roles and resolutions as literals. They must be the values of the evidence model.
func TestTheSchemaEnumsAreTheValuesOfTheEvidenceModel(t *testing.T) {
	t.Parallel()

	enums := schemaEnums(t)

	roles := make([]string, 0, len(observation.EvidenceRoles()))
	for _, r := range observation.EvidenceRoles() {
		roles = append(roles, string(r))
	}

	if got := enums["evidenceRole"]; !slices.Equal(got, roles) {
		t.Errorf("evidenceRole enum = %v, want %v", got, roles)
	}

	// `unbound` is the state of an entity, not a resolution of a fact: it is not a valid minimum.
	var resolutions []string

	for _, r := range observation.Resolutions() {
		if r != observation.ResolutionUnbound {
			resolutions = append(resolutions, string(r))
		}
	}

	if got := enums["resolution"]; !slices.Equal(got, resolutions) {
		t.Errorf("resolution enum = %v, want %v", got, resolutions)
	}

	requireCompletenessEnum(t, enums["completeness"])

	if policy.DefaultMinimumResolution.Rank() != observation.ResolutionExplicitlyBound.Rank() {
		t.Errorf("default resolution = %s", policy.DefaultMinimumResolution)
	}
}

// requireCompletenessEnum checks that the schema lists the completeness values in rank order.
func requireCompletenessEnum(t *testing.T, got []string) {
	t.Helper()

	completeness := []string{string(policy.CompletenessAny), string(policy.CompletenessPartial), string(policy.CompletenessComplete)}
	if !slices.Equal(got, completeness) {
		t.Errorf("completeness enum = %v, want %v in rank order", got, completeness)
	}

	if policy.CompletenessAny.Rank() >= policy.CompletenessPartial.Rank() {
		t.Errorf("completeness enum = %v, want %v in rank order", got, completeness)
	}

	if policy.CompletenessPartial.Rank() >= policy.CompletenessComplete.Rank() {
		t.Errorf("completeness enum = %v, want %v in rank order", got, completeness)
	}
}

// schemaEnums reads the `enum` list of each definition of the policy schema.
func schemaEnums(t *testing.T) map[string][]string {
	t.Helper()

	data, err := os.ReadFile("schemas/policy.schema.json")
	if err != nil {
		t.Fatal(err)
	}

	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	enums := make(map[string][]string, len(schema.Defs))
	for name, def := range schema.Defs {
		enums[name] = def.Enum
	}

	return enums
}
