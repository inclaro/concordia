package cloudfw

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/canonjson"
)

// tri is a three-valued truth value. Every rule check gives one, and `maybe` is what an unsupported or unresolvable
// construct gives: it is neither allow nor deny. `and` is the minimum and `or` the maximum, so allowing rules can only
// raise a result (the monotonicity that the property test checks).
type tri int8

const (
	triNo    tri = 0
	triMaybe tri = 1
	triYes   tri = 2
)

func and(a, b tri) tri { return min(a, b) }
func or(a, b tri) tri  { return max(a, b) }
func not(a tri) tri    { return triYes - a }

// side is the result of one check point on the path of the traffic (the outbound check at the source, the inbound
// check at the destination, or one Azure NSG): its verdict, the rules behind it, and the gaps it found.
type side struct {
	rules   []RuleRef
	gaps    []assurance.Gap
	verdict tri
}

// gap records a gap of the side.
func (s *side) gap(kind, detail string) {
	s.gaps = append(s.gaps, assurance.Gap{Kind: kind, Detail: detail})
}

// rule records a rule that took part in the decision of the side.
func (s *side) rule(address, rule string) {
	s.rules = append(s.rules, RuleRef{Address: address, Rule: rule})
}

// decide combines the sides of a path: the traffic is allowed when every side allows it, denied when one side denies
// it, and unknown otherwise. The rules of the sides that have the combined verdict decide it; the gaps of the
// `maybe` sides explain an `unknown` result.
func decide(sides ...*side) Result {
	verdict := triYes
	for _, s := range sides {
		verdict = and(verdict, s.verdict)
	}

	var res Result

	for _, s := range sides {
		if s.verdict != verdict {
			continue
		}

		res.Rules = append(res.Rules, s.rules...)

		if verdict == triMaybe {
			res.Gaps = append(res.Gaps, s.gaps...)
		}
	}

	res.Verdict = verdictOf(verdict)
	if verdict == triMaybe {
		res.Reason = reasonFor(res.Gaps)
	}

	return finish(&res)
}

func verdictOf(t tri) Verdict {
	switch t {
	case triYes:
		return VerdictAllowed
	case triNo:
		return VerdictDenied
	default:
		return VerdictUnknown
	}
}

// unknownFrom is an `unknown` result for gaps found before any rule was evaluated, with the reason that the gaps
// give.
func unknownFrom(gaps []assurance.Gap) Result {
	res := unknownResult(reasonFor(gaps), gaps...)

	return finish(&res)
}

// constructGaps are the gap kinds of a construct that the evaluator does not support. Every other kind is missing
// evidence.
var constructGaps = []string{
	evaluate.GapUnsupportedKind,
	evaluate.GapUnsupportedResource,
	evaluate.GapPeeringNotEvaluated,
	evaluate.GapAddressDependent,
	evaluate.GapInvalidRange,
	evaluate.GapMixedProviders,
}

// reasonFor is the reason of an `unknown` result: an unsupported construct when every gap is a construct gap, else
// incomplete coverage.
func reasonFor(gaps []assurance.Gap) string {
	construct := func(g assurance.Gap) bool { return slices.Contains(constructGaps, g.Kind) }
	if len(gaps) > 0 && !slices.ContainsFunc(gaps, func(g assurance.Gap) bool { return !construct(g) }) {
		return evaluate.ReasonUnsupportedConstruct
	}

	return evaluate.ReasonCoverageIncomplete
}

func sortRules(refs []RuleRef) []RuleRef {
	if refs == nil {
		return []RuleRef{}
	}

	slices.SortFunc(refs, func(a, b RuleRef) int {
		return cmpChain(canonjson.CompareUTF16(a.Address, b.Address), canonjson.CompareUTF16(a.Rule, b.Rule))
	})

	return slices.Compact(refs)
}

func sortGaps(gaps []assurance.Gap) []assurance.Gap {
	if gaps == nil {
		return []assurance.Gap{}
	}

	slices.SortFunc(gaps, func(a, b assurance.Gap) int {
		return cmpChain(canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Detail, b.Detail))
	})

	return slices.Compact(gaps)
}

func cmpChain(cmps ...int) int {
	for _, c := range cmps {
		if c != 0 {
			return c
		}
	}

	return 0
}

// pathTokens splits an attribute path into its keys and indexes. Both `ingress.0.cidr_blocks` and
// `ingress[0].cidr_blocks` give `ingress`, `0`, `cidr_blocks`.
func pathTokens(path string) []string {
	path = strings.ReplaceAll(strings.ReplaceAll(path, "[", "."), "]", "")

	return strings.FieldsFunc(path, func(r rune) bool { return r == '.' })
}

// planPath is the path in the form of plan.Resource.Unknown and plan.Resource.References: `ingress[0].cidr_blocks`.
func planPath(path string) string {
	var b strings.Builder

	for _, tok := range pathTokens(path) {
		if _, err := strconv.Atoi(tok); err == nil {
			b.WriteString("[" + tok + "]")

			continue
		}

		if b.Len() > 0 {
			b.WriteByte('.')
		}

		b.WriteString(tok)
	}

	return b.String()
}

// attr is the known value at the path of the planned values. ok is false when the value is absent, null, or unknown.
func attr(r *plan.Resource, path string) (value any, ok bool) {
	var cur any = r.Values

	for _, tok := range pathTokens(path) {
		if cur, ok = child(cur, tok); !ok {
			return nil, false
		}
	}

	return cur, cur != nil
}

func child(cur any, tok string) (any, bool) {
	switch typed := cur.(type) {
	case map[string]any:
		v, ok := typed[tok]

		return v, ok
	case []any:
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(typed) {
			return nil, false
		}

		return typed[i], true
	}

	return nil, false
}

// attrString is the known string at the path.
func attrString(r *plan.Resource, path string) (string, bool) {
	v, _ := attr(r, path)
	s, ok := scalarText(v)

	return s, ok
}

// attrStrings is the known string, or the known strings of the list, at the path. Numbers become their text; empty
// strings and other values are left out.
func attrStrings(r *plan.Resource, path string) []string {
	v, _ := attr(r, path)

	list, isList := v.([]any)
	if !isList {
		list = []any{v}
	}

	var out []string

	for _, item := range list {
		if s, ok := scalarText(item); ok && s != "" {
			out = append(out, s)
		}
	}

	return out
}

func scalarText(v any) (string, bool) {
	switch typed := v.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}

	return "", false
}

// attrInt is the known integer at the path (a number, or a string that holds one).
func attrInt(r *plan.Resource, path string) (int, bool) {
	s, ok := attrString(r, path)
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(s)

	return n, err == nil
}

// attrBool is true when the known value at the path is true.
func attrBool(r *plan.Resource, path string) bool {
	v, _ := attr(r, path)
	b, ok := v.(bool)

	return ok && b
}

// blockCount is the number of known elements of the list or nested block at the path.
func blockCount(r *plan.Resource, path string) int {
	v, _ := attr(r, path)
	list, _ := v.([]any)

	return len(list)
}

// unknownAt reports whether the attribute at the path, a part of it, or a value that holds it is unknown until apply.
func unknownAt(r *plan.Resource, path string) bool {
	path = planPath(path)

	return slices.ContainsFunc(r.Unknown, func(u string) bool { return u == path || within(u, path) || within(path, u) })
}

// within reports whether the path is a part of the parent path.
func within(path, parent string) bool {
	if len(path) <= len(parent) || !strings.HasPrefix(path, parent) {
		return false
	}

	next := path[len(parent)]

	return next == '.' || next == '['
}

// refs resolves the attribute at the path (one ID or a list of IDs) to the resources of the plan that it names. A
// known ID resolves through the `id` index. An attribute that is unknown until apply resolves through the
// configuration references of the resource. ok is false when a part does not resolve; the gaps say why. An absent
// attribute gives no resources and ok true.
func (e *Evaluator) refs(r *plan.Resource, path string, s *side) (found []*plan.Resource, ok bool) {
	path = planPath(path)
	found, ok = e.knownRefs(r, path, s)

	if unknownAt(r, path) {
		more, resolved := e.configRefs(r, path, s)
		found = append(found, more...)
		ok = ok && resolved
	}

	slices.SortFunc(found, func(a, b *plan.Resource) int { return canonjson.CompareUTF16(a.Address, b.Address) })

	return slices.Compact(found), ok
}

// ref resolves an attribute that names one resource. It gives nil and ok true when the attribute is absent, and ok
// false when it does not resolve to exactly one resource.
func (e *Evaluator) ref(r *plan.Resource, path string, s *side) (*plan.Resource, bool) {
	found, ok := e.refs(r, path, s)

	switch {
	case !ok:
		return nil, false
	case len(found) > 1:
		s.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s.%s names %d resources", r.Address, planPath(path), len(found)))

		return nil, false
	case len(found) == 1:
		return found[0], true
	}

	return nil, true
}

// knownRefs resolves the known IDs at the path.
func (e *Evaluator) knownRefs(r *plan.Resource, path string, s *side) (found []*plan.Resource, ok bool) {
	ok = true

	for _, id := range attrStrings(r, path) {
		if target := e.resourceByID(id); target != nil {
			found = append(found, target)

			continue
		}

		e.missingID(r, path, id, s)

		ok = false
	}

	return found, ok
}

// missingID records the gap of a known ID that no resource of the plan has: a data source when the configuration
// names one, else an unresolved reference.
func (e *Evaluator) missingID(r *plan.Resource, path, id string, s *side) {
	if data := slices.IndexFunc(r.References[path], isDataSource); data >= 0 {
		s.gap(evaluate.GapUnresolvedReference, dataSourceDetail(r.References[path][data]))

		return
	}

	s.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s.%s: %s is not the id of a resource in the plan", r.Address, path, id))
}

// configRefs resolves an unknown attribute through the configuration references of the resource.
func (e *Evaluator) configRefs(r *plan.Resource, path string, s *side) (found []*plan.Resource, ok bool) {
	addresses := r.References[path]
	if len(addresses) == 0 {
		s.gap(evaluate.GapUnknownAfterApply, r.Address+"."+path)

		return nil, false
	}

	ok = true

	for _, config := range addresses {
		target, resolved := e.resolveConfig(config, s)
		if !resolved {
			ok = false

			continue
		}

		found = append(found, target)
	}

	return found, ok
}

// resolveConfig resolves a configuration address (without instance keys) to its single instance in the plan.
func (e *Evaluator) resolveConfig(config string, s *side) (*plan.Resource, bool) {
	if isDataSource(config) {
		s.gap(evaluate.GapUnresolvedReference, dataSourceDetail(config))

		return nil, false
	}

	switch inst := e.instances(config); len(inst) {
	case 1:
		return inst[0], true
	case 0:
		s.gap(evaluate.GapUnresolvedReference, config+" is not a resource of the plan after apply")
	default:
		s.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s has %d instances", config, len(inst)))
	}

	return nil, false
}

func dataSourceDetail(config string) string {
	return config + " is a data source; its rules are not in the plan"
}

// isDataSource reports whether the configuration address names a data source (`data.<type>.<name>`, in any module).
func isDataSource(config string) bool {
	tokens := strings.Split(config, ".")

	i := 0
	for i+1 < len(tokens) && tokens[i] == "module" {
		i += 2
	}

	return i < len(tokens) && tokens[i] == "data"
}

// addrSet is the address set of an endpoint: one prefix, or unknown.
type addrSet struct {
	prefix netip.Prefix
	known  bool
}

// unknownAddrs is the address set of an endpoint whose addresses the plan does not give.
var unknownAddrs = addrSet{}

// prefixSet is the address set of a CIDR (a subnet range). Text that does not parse gives an unknown set.
func prefixSet(cidr string) addrSet {
	p, ok := parseRange(cidr)

	return addrSet{prefix: p, known: ok}
}

// hostSet is the address set of one IP address (a private IP), a /32 or a /128.
func hostSet(ip string) addrSet {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return unknownAddrs
	}

	addr = addr.Unmap()

	return addrSet{prefix: netip.PrefixFrom(addr, addr.BitLen()), known: true}
}

// commonSet is the set that all the sets share when they are known and equal, else unknown. Several subnets of one
// endpoint give an unknown set unless they have the same prefix.
func commonSet(sets ...addrSet) addrSet {
	if len(sets) == 0 {
		return unknownAddrs
	}

	for _, set := range sets {
		if !set.known || set.prefix != sets[0].prefix {
			return unknownAddrs
		}
	}

	return sets[0]
}

// parseRange reads a CIDR or a single IP address, masked.
func parseRange(text string) (netip.Prefix, bool) {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "/") {
		set := hostSet(text)

		return set.prefix, set.known
	}

	p, err := netip.ParsePrefix(text)
	if err != nil {
		return netip.Prefix{}, false
	}

	return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked(), true
}

// matchPrefix matches a rule prefix (a CIDR or an IP) against the address set of an endpoint: yes when the rule
// contains the set, no when they do not overlap, and maybe with a gap otherwise.
func matchPrefix(rule string, ep addrSet, s *side) tri {
	p, ok := parseRange(rule)

	switch {
	case !ok:
		s.gap(evaluate.GapInvalidRange, fmt.Sprintf("%q is not a CIDR or an IP address", rule))

		return triMaybe
	case !ep.known:
		return matchUnknownSet(rule, p, s)
	case p.Bits() <= ep.prefix.Bits() && p.Contains(ep.prefix.Addr()):
		return triYes
	case !p.Overlaps(ep.prefix):
		return triNo
	}

	s.gap(evaluate.GapAddressDependent, fmt.Sprintf("%s covers only part of %s", rule, ep.prefix))

	return triMaybe
}

// matchUnknownSet matches a rule prefix against an endpoint whose addresses are unknown: only an all-addresses
// prefix is sure to match.
func matchUnknownSet(rule string, p netip.Prefix, s *side) tri {
	if p.Bits() == 0 {
		return triYes
	}

	s.gap(evaluate.GapAddressDependent, rule+" against an endpoint whose addresses are not known before apply")

	return triMaybe
}

// anyPrefix is the or of matchPrefix over the rule prefixes. An empty list matches nothing.
func anyPrefix(rules []string, ep addrSet, s *side) tri {
	result := triNo
	for _, rule := range rules {
		result = or(result, matchPrefix(rule, ep, s))
	}

	return result
}

// protocolAny is the normalized protocol of a rule that matches every protocol.
const protocolAny = "*"

// normProtocol gives the lower-case protocol name: `-1`, `all`, and `*` become protocolAny, `6` becomes `tcp`, `17`
// becomes `udp`.
func normProtocol(protocol string) string {
	switch p := strings.ToLower(strings.TrimSpace(protocol)); p {
	case "-1", "all", protocolAny:
		return protocolAny
	case "6":
		return "tcp"
	case "17":
		return "udp"
	default:
		return p
	}
}

// queryProtocol is the normalized protocol of the query; TCP when it is not set.
func queryProtocol(q *Query) string {
	if q.Protocol == "" {
		return "tcp"
	}

	return normProtocol(q.Protocol)
}

// matchProtocol is yes when the rule protocol is every protocol or the protocol of the query, else no. ICMP and
// other protocols never match a TCP or UDP query.
func matchProtocol(rule string, q *Query) tri {
	if p := normProtocol(rule); p == protocolAny || p == queryProtocol(q) {
		return triYes
	}

	return triNo
}

// matchPortRange matches the port against from-to. A rule for every protocol (`-1`) matches every port.
func matchPortRange(protocol string, from, to, port int) tri {
	if normProtocol(protocol) == protocolAny || (from <= port && port <= to) {
		return triYes
	}

	return triNo
}

// matchPortSpec matches the port against a port text: `*`, `80`, or `1000-2000`. Text that does not parse gives
// maybe with a gap.
func matchPortSpec(spec string, port int, s *side) tri {
	spec = strings.TrimSpace(spec)
	if spec == protocolAny {
		return triYes
	}

	from, to, ok := parsePortSpec(spec)
	if !ok {
		s.gap(evaluate.GapUnsupportedKind, fmt.Sprintf("port range %q", spec))

		return triMaybe
	}

	return matchPortRange("", from, to, port)
}

func parsePortSpec(spec string) (from, to int, ok bool) {
	low, high, isRange := strings.Cut(spec, "-")

	from, err := strconv.Atoi(strings.TrimSpace(low))
	if err != nil {
		return 0, 0, false
	}

	if !isRange {
		return from, from, true
	}

	to, err = strconv.Atoi(strings.TrimSpace(high))

	return from, to, err == nil && from <= to
}

// anyPortSpec is the or of matchPortSpec over the port texts. An empty list matches no port.
func anyPortSpec(specs []string, port int, s *side) tri {
	result := triNo
	for _, spec := range specs {
		result = or(result, matchPortSpec(spec, port, s))
	}

	return result
}
