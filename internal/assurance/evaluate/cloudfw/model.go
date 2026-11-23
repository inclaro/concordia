package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/plan"
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

// Input is the evidence of one environment: the resources of all its plans.
type Input struct {
	// Resources are every resource of the plans of the environment, sorted by address.
	Resources []plan.Resource
	// Coverage is the worst coverage of the plans.
	Coverage assurance.Coverage
	// Digest is the digest of the evaluated plan bytes.
	Digest string
}

// nameKey finds an Azure resource by its type, `name`, and `resource_group_name`.
type nameKey struct {
	typ, name, group string
}

// Evaluator answers queries over one Input. It is safe for concurrent use after New.
type Evaluator struct {
	in *Input
	// live are the resources that exist after apply, in input order (sorted by address).
	live      []*plan.Resource
	byAddress map[string]*plan.Resource
	byID      map[string]*plan.Resource
	byName    map[nameKey]*plan.Resource
	// byConfig lists the instances of each configuration address (the address without instance keys).
	byConfig map[string][]*plan.Resource
}

// New indexes the input. It does not fail: whatever the evaluator cannot use becomes a gap of the results that
// depend on it. The input must not be changed while the evaluator is in use.
func New(in *Input) *Evaluator {
	e := &Evaluator{
		in:        in,
		byAddress: map[string]*plan.Resource{},
		byID:      map[string]*plan.Resource{},
		byName:    map[nameKey]*plan.Resource{},
		byConfig:  map[string][]*plan.Resource{},
	}

	for i := range in.Resources {
		if r := &in.Resources[i]; exists(r) {
			e.index(r)
		}
	}

	return e
}

// exists reports whether the resource exists after apply: it is not a deposed object and it is not destroyed.
func exists(r *plan.Resource) bool { return r.Deposed == "" && r.Values != nil }

func (e *Evaluator) index(r *plan.Resource) {
	e.live = append(e.live, r)
	e.byAddress[r.Address] = r
	config := plan.ConfigAddress(r.Address)
	e.byConfig[config] = append(e.byConfig[config], r)

	if id, ok := attrString(r, "id"); ok && id != "" {
		e.byID[id] = r
	}

	name, hasName := attrString(r, "name")
	group, hasGroup := attrString(r, "resource_group_name")

	if hasName && hasGroup {
		e.byName[nameKey{typ: r.Type, name: name, group: group}] = r
	}
}

// InputDigest is the digest of the evaluated input.
func (e *Evaluator) InputDigest() string { return e.in.Digest }

// resource is the resource at the plan address, or nil.
func (e *Evaluator) resource(address string) *plan.Resource { return e.byAddress[address] }

// resourceByID is the resource whose known `id` is id, or nil.
func (e *Evaluator) resourceByID(id string) *plan.Resource { return e.byID[id] }

// named is the resource of the type with the known `name` and `resource_group_name`, or nil.
func (e *Evaluator) named(typ, name, group string) *plan.Resource {
	return e.byName[nameKey{typ: typ, name: name, group: group}]
}

// instances are the resources of a configuration address (without instance keys), sorted by address.
func (e *Evaluator) instances(config string) []*plan.Resource { return e.byConfig[config] }

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
