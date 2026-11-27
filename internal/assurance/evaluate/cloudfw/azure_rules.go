package cloudfw

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Values of the rule fields that the evaluator compares, and the priorities of the default rules.
const (
	azureAllow            = "Allow"
	azureInbound          = "Inbound"
	azureOutbound         = "Outbound"
	azureProtocolAttr     = "protocol"
	azureVirtualNetwork   = "VirtualNetwork"
	azurePriorityVNet     = 65000
	azurePriorityPlatform = 65001
	azurePriorityDenyAll  = 65500
)

// azureRule is one rule of an NSG. Its fields are read from res under the path prefix base: an inline
// `security_rule` block of the NSG, an `azurerm_network_security_rule`, or a default rule.
type azureRule struct {
	res       *plan.Resource
	base      string
	address   string
	fixedGaps []assurance.Gap
	// index is the position of an inline rule in the `security_rule` list of the plan values.
	index int
	// fixed is no when the rule is not a rule of the NSG, maybe when its owner or its priority, direction, or access
	// is not known (fixedGaps say why), else yes.
	fixed tri
	// inline is true for a `security_rule` block of the NSG.
	inline bool
	// owned is the ownership of a rule resource by the NSG: yes when the rule surely belongs to it.
	owned tri
	// stale is true for an inline rule of an NSG that a rule resource the plan changes may belong to.
	stale bool
}

// azureDefault is one of the default rules that Azure adds to every NSG.
type azureDefault struct {
	name                string
	direction           string
	source, destination string
	access              string
	priority            int
}

var azureDefaults = []azureDefault{
	{
		name: "AllowVnetInBound", priority: azurePriorityVNet, direction: azureInbound,
		source: azureVirtualNetwork, destination: azureVirtualNetwork, access: azureAllow,
	},
	{
		name: "AllowAzureLoadBalancerInBound", priority: azurePriorityPlatform, direction: azureInbound,
		source: "AzureLoadBalancer", destination: protocolAny, access: azureAllow,
	},
	{
		name: "DenyAllInBound", priority: azurePriorityDenyAll, direction: azureInbound,
		source: protocolAny, destination: protocolAny, access: "Deny",
	},
	{
		name: "AllowVnetOutBound", priority: azurePriorityVNet, direction: azureOutbound,
		source: azureVirtualNetwork, destination: azureVirtualNetwork, access: azureAllow,
	},
	{
		name: "AllowInternetOutBound", priority: azurePriorityPlatform, direction: azureOutbound,
		source: protocolAny, destination: "Internet", access: azureAllow,
	},
	{
		name: "DenyAllOutBound", priority: azurePriorityDenyAll, direction: azureOutbound,
		source: protocolAny, destination: protocolAny, access: "Deny",
	},
}

// azureDefaultResources hold the default rules in the shape of a rule resource, so that one matcher reads all rules.
var azureDefaultResources = azureBuildDefaults()

func azureBuildDefaults() []*plan.Resource {
	out := make([]*plan.Resource, 0, len(azureDefaults))

	for _, d := range azureDefaults {
		out = append(out, &plan.Resource{Address: "default rule " + d.name, Type: azureNSGRule, Values: map[string]any{
			"name": d.name, "priority": strconv.Itoa(d.priority), "direction": d.direction, "access": d.access,
			azureProtocolAttr: protocolAny, "destination_port_range": protocolAny,
			"source_address_prefix": d.source, "destination_address_prefix": d.destination,
		}})
	}

	return out
}

// azureRulesOf lists the rules of the NSG for one direction in ascending priority: the inline rules, the rule
// resources of the NSG, and the default rules.
func (e *Evaluator) azureRulesOf(nsg *plan.Resource, outbound bool) []azureRule {
	standalone := e.azureStandaloneRules(nsg)
	rules := slices.Concat(e.azureFreshInline(nsg, standalone), standalone, azureDefaultRules(nsg))
	rules = slices.DeleteFunc(rules, func(r azureRule) bool { return !r.inDirection(outbound) })

	slices.SortStableFunc(rules, func(a, b azureRule) int { return cmp.Compare(a.priority(), b.priority()) })

	return rules
}

// azureRulesAttr is the list of inline rules of an NSG.
const azureRulesAttr = "security_rule"

func azureInlineRules(nsg *plan.Resource) []azureRule {
	count := blockCount(nsg, azureRulesAttr)
	rules := make([]azureRule, 0, count)

	for i := range count {
		rule := newAzureRule(nsg, fmt.Sprintf("%s[%d].", azureRulesAttr, i), nsg.Address, triYes)
		rule.inline, rule.index = true, i
		rules = append(rules, rule)
	}

	return rules
}

// azureRulesUnknown reports, with a gap, whether the inline rule list of the NSG is unknown until apply as a whole
// or in whole elements (a dynamic block, or the computed list of a new NSG).
func azureRulesUnknown(nsg *plan.Resource, report *side) bool {
	if !slices.Contains(nsg.Unknown, azureRulesAttr) && !listHasUnknownElements(nsg, azureRulesAttr) {
		return false
	}

	report.gap(evaluate.GapUnknownAfterApply, nsg.Address+"."+azureRulesAttr)

	return true
}

func azureDefaultRules(nsg *plan.Resource) []azureRule {
	rules := make([]azureRule, 0, len(azureDefaultResources))

	for _, d := range azureDefaultResources {
		rules = append(rules, newAzureRule(d, "", nsg.Address, triYes))
	}

	return rules
}

// azureStandaloneRules are the `azurerm_network_security_rule` resources that belong to the NSG, or may belong to it.
func (e *Evaluator) azureStandaloneRules(nsg *plan.Resource) []azureRule {
	var rules []azureRule

	for _, res := range e.ofType(azureNSGRule) {
		owner := &side{}
		if belongs := e.azureRuleOwner(res, nsg, owner); belongs != triNo {
			rule := newAzureRule(res, "", res.Address, belongs)
			rule.owned = belongs
			rule.fixedGaps = append(rule.fixedGaps, owner.gaps...)
			rules = append(rules, rule)
		}
	}

	return rules
}

// newAzureRule reads the fixed fields of a rule: a priority, direction, or access that is unknown makes it maybe.
func newAzureRule(res *plan.Resource, base, address string, owner tri) azureRule {
	rule := azureRule{res: res, base: base, address: address, fixed: owner}

	for _, field := range []string{"priority", "direction", "access"} {
		if unknownAt(res, base+field) {
			rule.fixed = and(rule.fixed, triMaybe)
			rule.fixedGaps = append(rule.fixedGaps, assurance.Gap{
				Kind: evaluate.GapUnknownAfterApply, Detail: res.Address + "." + planPath(base+field),
			})
		}
	}

	return rule
}

// azureRuleOwner is yes when the rule resource belongs to the NSG: azureOwnerOf finds the owner, and azureInScope
// keeps it only within one subscription.
func (e *Evaluator) azureRuleOwner(rule, nsg *plan.Resource, report *side) tri {
	return e.azureInScope(rule, nsg, e.azureOwnerOf(rule, nsg, report), report)
}

// azureInScope keeps a yes owner only when the rule and the NSG are in one plan and one provider configuration,
// which fix their subscription: Azure finds the NSG of a rule by name and resource group within the rule's
// subscription, and another subscription can have an NSG with the same name and resource group.
func (e *Evaluator) azureInScope(rule, nsg *plan.Resource, owner tri, report *side) tri {
	if owner != triYes || e.azureOneSubscription(rule, nsg) {
		return owner
	}

	report.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s names %s, but the plan does not show that the two are "+
		"in one subscription (one plan and one provider configuration)", rule.Address, nsg.Address))

	return triMaybe
}

// azureOneSubscription reports whether the two resources are in one plan and have one provider configuration.
func (e *Evaluator) azureOneSubscription(a, b *plan.Resource) bool {
	return a.ProviderConfig != "" && a.ProviderConfig == b.ProviderConfig && e.planOf[a] == e.planOf[b]
}

// azureOwnerOf is yes when the rule resource names the NSG. It uses the known name and resource group of the NSG
// first, then the configuration reference of `network_security_group_name` when the name is unknown.
func (e *Evaluator) azureOwnerOf(rule, nsg *plan.Resource, report *side) tri {
	name, hasName := attrString(rule, "network_security_group_name")
	group, hasGroup := attrString(rule, "resource_group_name")

	if hasName && hasGroup {
		return e.azureOwnerByName(nsg, name, group, report)
	}

	if refs := rule.References["network_security_group_name"]; len(refs) > 0 {
		return e.azureOwnerByReference(rule, refs, nsg, report)
	}

	report.gap(evaluate.GapUnknownAfterApply, rule.Address+".network_security_group_name")

	return triMaybe
}

// azureOwnerByName is yes when the NSG is the one NSG of the plans with the name and resource group, maybe when
// several NSGs of the plans have them and the NSG is one of them.
func (e *Evaluator) azureOwnerByName(nsg *plan.Resource, name, group string, report *side) tri {
	switch owners := e.named(newNameKey(azureNSG, name, group)); {
	case len(owners) == 1:
		return azureIf(owners[0] == nsg)
	case len(owners) == 0:
		return azureNamedLike(nsg, name, group, report)
	case !slices.Contains(owners, nsg):
		return triNo
	default:
		report.gap(evaluate.GapUnresolvedReference,
			fmt.Sprintf("network security group %s/%s names %d resources of the plans", group, name, len(owners)))

		return triMaybe
	}
}

// azureOwnerByReference is yes when the one referenced NSG is the NSG and the rule is in its resource group, maybe
// when a reference does not resolve to an NSG, names several NSGs, or names an NSG of another or unknown resource
// group: Azure finds the NSG of a rule by its name in the rule's resource group. A reference list that resolves only
// in part may take its value from the part that does not resolve, so it names no owner for certain.
func (e *Evaluator) azureOwnerByReference(rule *plan.Resource, refs []string, nsg *plan.Resource, report *side) tri {
	found, resolved := e.resolveAll(rule, refs, report)
	owners, typed := keepTypes(attrRef{res: rule, path: "network_security_group_name"}, found, report, []string{azureNSG})

	if !resolved || !typed {
		return triMaybe
	}

	result := azureIf(slices.Contains(owners, nsg))

	if doubt := azureReferenceDoubt(rule, owners); doubt != "" {
		report.gap(evaluate.GapUnresolvedReference, rule.Address+".network_security_group_name: "+doubt)

		return triMaybe
	}

	return result
}

// azureReferenceDoubt says why the referenced NSGs do not establish the owner of the rule, or is "".
func azureReferenceDoubt(rule *plan.Resource, owners []*plan.Resource) string {
	if len(owners) > 1 {
		return fmt.Sprintf("the configuration names %d network security groups", len(owners))
	}

	if len(owners) == 0 {
		return ""
	}

	ruleGroup, ruleKnown := attrString(rule, "resource_group_name")
	nsgGroup, nsgKnown := attrString(owners[0], "resource_group_name")

	switch {
	case !ruleKnown || !nsgKnown:
		return "the resource group of the rule or of " + owners[0].Address + " is not known"
	case !strings.EqualFold(ruleGroup, nsgGroup):
		return fmt.Sprintf("the rule is in resource group %s and %s is in %s", ruleGroup, owners[0].Address, nsgGroup)
	}

	return ""
}

// azureNamedLike decides for a rule whose NSG name no NSG of the plan has. When the name of the NSG is known, the
// lookup would have found it, so the rule is not one of its rules. Otherwise it may be.
func azureNamedLike(nsg *plan.Resource, name, group string, report *side) tri {
	if azureHasName(nsg) {
		return triNo
	}

	report.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("network security group %s/%s is not a resource of the plan", group, name))

	return triMaybe
}

// azureHasName reports whether the name and the resource group of the resource are known.
func azureHasName(r *plan.Resource) bool {
	_, hasName := attrString(r, "name")
	_, hasGroup := attrString(r, "resource_group_name")

	return hasName && hasGroup
}

func azureIf(b bool) tri {
	if b {
		return triYes
	}

	return triNo
}

func (r *azureRule) text(field string) string {
	value, _ := attrString(r.res, r.base+field)

	return value
}

// priority is the priority of the rule; an unknown priority sorts first, where it makes the NSG result unknown.
func (r *azureRule) priority() int {
	n, _ := attrInt(r.res, r.base+"priority")

	return n
}

// inDirection reports whether the rule applies to the direction. A rule with an unknown direction applies to both.
func (r *azureRule) inDirection(outbound bool) bool {
	if unknownAt(r.res, r.base+"direction") {
		return true
	}

	return strings.EqualFold(r.text("direction"), azureOutbound) == outbound
}

// access is yes for an Allow rule and no for a Deny rule.
func (r *azureRule) access() tri {
	return azureIf(strings.EqualFold(r.text("access"), azureAllow))
}

func (r *azureRule) label() string {
	return fmt.Sprintf("%s (priority %d)", r.text("name"), r.priority())
}
