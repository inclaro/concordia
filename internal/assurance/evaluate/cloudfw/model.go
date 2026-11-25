package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Protocols of a query.
const (
	ProtocolTCP = netpol.ProtocolTCP
	ProtocolUDP = netpol.ProtocolUDP
)

// Endpoint is one end of a query: a plan address, with an instance key when the resource has one.
type Endpoint struct {
	Address string
}

// Query asks whether traffic from From to To on one destination port and protocol is allowed.
type Query struct {
	From Endpoint
	To   Endpoint
	// Protocol is TCP (the default) or UDP.
	Protocol string
	// Port is the destination port number (1-65535).
	Port int
}

// Verdict is the result of one query.
type Verdict string

// Verdicts.
const (
	VerdictAllowed Verdict = "allowed"
	VerdictDenied  Verdict = "denied"
	VerdictUnknown Verdict = "unknown"
)

// RuleRef names a firewall rule that took part in a decision. Rule is the rule within the resource, for example
// `ingress[0]`, `security_rule[2]`, `rule`, or `priority 100 deny`.
type RuleRef struct {
	Address string `json:"address"`
	Rule    string `json:"rule"`
}

// Result is the answer to one Query.
type Result struct {
	Verdict Verdict `json:"verdict"`
	// Reason is set when the verdict is `unknown`: one of the `evaluate.Reason*` values.
	Reason string `json:"reason,omitempty"`
	// Rules are the rules that decided the result, sorted by address then rule.
	Rules []RuleRef `json:"rules"`
	// Assumptions are the enforcement assumptions the result depends on: set for `denied` only.
	Assumptions []string `json:"assumptions"`
	// Limits are what the evaluation does not cover.
	Limits []string `json:"limits"`
	// Gaps explain an `unknown` result, sorted by kind then detail.
	Gaps []assurance.Gap `json:"gaps"`
}

// Input is the evidence of one environment: the resources of each of its plans.
type Input struct {
	// Environment names the environment in gaps.
	Environment string
	// Coverage is the worst coverage of the plans.
	Coverage assurance.Coverage
	// Digest is the digest of the evaluated plan bytes.
	Digest string
	// Plans are the usable plans of the environment.
	Plans []Plan
}

// Plan is the resources of one plan file. A configuration reference resolves only inside the plan of the resource
// that holds it; a known ID or name resolves across all plans of the input.
type Plan struct {
	// File is the workspace-relative path of the plan file.
	File string
	// Resources are the resources of the plan, sorted by address.
	Resources []plan.Resource
}

// configKey finds the instances of a configuration address (the address without instance keys) in one plan.
type configKey struct {
	address string
	plan    int
}

// Evaluator answers queries over one Input. It is safe for concurrent use after New.
type Evaluator struct {
	in *Input
	// byAddress, byID, and byName list the resources of all plans with the address, known `id`, or known name: more
	// than one when several plans hold one.
	byAddress map[string][]*plan.Resource
	byID      map[string][]*plan.Resource
	byName    map[nameKey][]*plan.Resource
	byConfig  map[configKey][]*plan.Resource
	// planOf is the index in Input.Plans of the plan of each resource.
	planOf map[*plan.Resource]int
	// live are the resources that exist after apply, sorted by address.
	live []*plan.Resource
	// changed are the rule resources that the plan deletes or updates, including those that do not exist after apply.
	changed []*plan.Resource
}

// New indexes the input. It does not fail: whatever the evaluator cannot use becomes a gap of the results that
// depend on it. The input must not be changed while the evaluator is in use.
func New(in *Input) *Evaluator {
	evaluator := &Evaluator{
		in:        in,
		byAddress: map[string][]*plan.Resource{},
		byID:      map[string][]*plan.Resource{},
		byName:    map[nameKey][]*plan.Resource{},
		byConfig:  map[configKey][]*plan.Resource{},
		planOf:    map[*plan.Resource]int{},
	}

	for p := range in.Plans {
		evaluator.indexPlan(p)
	}

	byAddress := func(a, b *plan.Resource) int { return canonjson.CompareUTF16(a.Address, b.Address) }
	slices.SortStableFunc(evaluator.live, byAddress)
	slices.SortStableFunc(evaluator.changed, byAddress)

	return evaluator
}

// indexPlan indexes the resources of the plan at index p of the input.
func (e *Evaluator) indexPlan(p int) {
	resources := e.in.Plans[p].Resources

	for i := range resources {
		res := &resources[i]
		e.planOf[res] = p

		if exists(res) {
			e.index(res, p)
		}

		if changesRule(res) {
			e.changed = append(e.changed, res)
		}
	}
}

// exists reports whether the resource exists after apply: it is not a deposed object and it is not destroyed.
func exists(r *plan.Resource) bool { return r.Deposed == "" && r.Values != nil }

func (e *Evaluator) index(res *plan.Resource, p int) {
	e.live = append(e.live, res)
	e.byAddress[res.Address] = append(e.byAddress[res.Address], res)
	config := configKey{plan: p, address: plan.ConfigAddress(res.Address)}
	e.byConfig[config] = append(e.byConfig[config], res)

	if id, ok := attrString(res, "id"); ok && id != "" {
		e.byID[id] = append(e.byID[id], res)
	}

	e.indexName(res)
}

// InputDigest is the digest of the evaluated input.
func (e *Evaluator) InputDigest() string { return e.in.Digest }

// resource is the one resource at the plan address, or nil when no plan or more than one plan holds it.
func (e *Evaluator) resource(address string) *plan.Resource { return only(e.byAddress[address]) }

// resourceByID is the one resource whose known `id` is id, or nil when none or more than one has it.
func (e *Evaluator) resourceByID(id string) *plan.Resource { return only(e.byID[id]) }

// instances are the resources of a configuration address (without instance keys) in the plan of the resource from,
// sorted by address.
func (e *Evaluator) instances(from *plan.Resource, config string) []*plan.Resource {
	return e.byConfig[configKey{plan: e.planOf[from], address: config}]
}

// only is the single resource of the list, or nil.
func only(found []*plan.Resource) *plan.Resource {
	if len(found) != 1 {
		return nil
	}

	return found[0]
}

// ofType lists the resources of the given types, sorted by address.
func (e *Evaluator) ofType(types ...string) []*plan.Resource {
	var out []*plan.Resource

	for _, r := range e.live {
		if slices.Contains(types, r.Type) {
			out = append(out, r)
		}
	}

	return out
}
