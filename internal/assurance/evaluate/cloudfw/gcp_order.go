package cloudfw

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// GCP firewall rule values.
const (
	gcpFirewallType     = "google_compute_firewall"
	gcpIngress          = "INGRESS"
	gcpEgress           = "EGRESS"
	gcpDefaultPriority  = 1000
	gcpImpliedPriority  = 65535
	gcpAllow            = "allow"
	gcpDeny             = "deny"
	gcpAllAddresses     = "0.0.0.0/0"
	gcpImpliedRuleOwner = "implied"
	gcpUnknownText      = "unknown"
	gcpPriorityAttr     = "priority"
	gcpDirectionAttr    = "direction"
	gcpDisabledAttr     = "disabled"
)

// gcpScalarOrderFields are the attributes that place a rule in the order of its direction.
var gcpScalarOrderFields = []string{gcpPriorityAttr, gcpDirectionAttr, gcpDisabledAttr}

// gcpRule is one firewall rule of a direction. res is nil for an implied rule, which matches all traffic. gaps is
// not empty when the plan does not give the place of the rule in the order (priority, direction, enabled) or its
// action: such a rule comes first, and a match that is not a sure no makes the side unknown.
type gcpRule struct {
	res           *plan.Resource
	name          string
	gaps          []assurance.Gap
	priority      int
	deny          bool
	actionUnknown bool
}

func (r *gcpRule) uncertain() bool { return len(r.gaps) > 0 }

func (r *gcpRule) action() string {
	if r.deny {
		return gcpDeny
	}

	return gcpAllow
}

func (r *gcpRule) verdict() tri {
	if r.deny {
		return triNo
	}

	return triYes
}

func (r *gcpRule) record(point *side) {
	owner := gcpImpliedRuleOwner
	if r.res != nil {
		owner = r.res.Address
	}

	point.rule(owner, r.name)
}

// gcpRules are the firewall rules that may be enabled in the direction and the implied rule: the uncertain rules
// first, then by ascending priority, deny before allow at equal priority.
func (e *Evaluator) gcpRules(ingress bool) []gcpRule {
	direction, impliedDeny := gcpEgress, false
	if ingress {
		direction, impliedDeny = gcpIngress, true
	}

	implied := gcpRule{priority: gcpImpliedPriority, deny: impliedDeny}
	implied.name = fmt.Sprintf("priority %d %s", implied.priority, implied.action())
	rules := []gcpRule{implied}

	for _, firewall := range e.ofType(gcpFirewallType) {
		if !gcpSkipped(firewall, direction) {
			rules = append(rules, gcpRuleOf(firewall))
		}
	}

	slices.SortStableFunc(rules, func(a, b gcpRule) int {
		return cmpChain(gcpTrueFirst(a.uncertain(), b.uncertain()), cmp.Compare(a.priority, b.priority),
			gcpTrueFirst(a.deny, b.deny))
	})

	return rules
}

// gcpSkipped reports whether the plan shows that the rule is disabled or belongs to the other direction.
func gcpSkipped(firewall *plan.Resource, direction string) bool {
	if attrBool(firewall, gcpDisabledAttr) {
		return true
	}

	return !unknownAt(firewall, gcpDirectionAttr) && gcpDirection(firewall) != direction
}

// gcpRuleOf reads the place and the action of a firewall rule.
func gcpRuleOf(firewall *plan.Resource) gcpRule {
	rule := gcpRule{res: firewall, priority: gcpDefaultPriority, deny: blockCount(firewall, gcpDeny) > 0}
	if priority, ok := attrInt(firewall, gcpPriorityAttr); ok {
		rule.priority = priority
	}

	for _, field := range gcpScalarOrderFields {
		if unknownAt(firewall, field) {
			rule.gaps = append(rule.gaps, gcpUnknownGap(firewall, field))
		}
	}

	for _, list := range []string{gcpAllow, gcpDeny} {
		if gcpListUnknown(firewall, list) {
			rule.gaps = append(rule.gaps, gcpUnknownGap(firewall, list))
			rule.actionUnknown = true
		}
	}

	rule.name = gcpRuleName(&rule)

	return rule
}

// gcpListUnknown reports whether the list, or an element of it, is unknown until apply.
func gcpListUnknown(firewall *plan.Resource, list string) bool {
	return slices.Contains(firewall.Unknown, planPath(list)) || listHasUnknownElements(firewall, list)
}

func gcpUnknownGap(res *plan.Resource, path string) assurance.Gap {
	return assurance.Gap{Kind: evaluate.GapUnknownAfterApply, Detail: res.Address + "." + planPath(path)}
}

// gcpRuleName is `priority <n> <allow|deny>`, with `unknown` for a part that the plan does not give.
func gcpRuleName(rule *gcpRule) string {
	priority, action := strconv.Itoa(rule.priority), rule.action()
	if unknownAt(rule.res, gcpPriorityAttr) {
		priority = gcpUnknownText
	}

	if rule.actionUnknown {
		action = gcpUnknownText
	}

	return "priority " + priority + " " + action
}

func gcpDirection(firewall *plan.Resource) string {
	if direction, ok := attrString(firewall, gcpDirectionAttr); ok && direction != "" {
		return direction
	}

	return gcpIngress
}

func gcpTrueFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}

	return 1
}
