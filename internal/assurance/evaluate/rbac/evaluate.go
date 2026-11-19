package rbac

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Verdict is the result of one RBAC query.
type Verdict string

// Verdicts.
const (
	Granted    Verdict = "granted"
	NotGranted Verdict = "not-granted"
	Unknown    Verdict = "unknown"
)

// Groups that Kubernetes adds to every ServiceAccount user.
const (
	groupAuthenticated   = "system:authenticated"
	groupServiceAccounts = "system:serviceaccounts"
	serviceAccountUser   = "system:serviceaccount:"
)

// Request is the permission that a query asks for: one verb on one resource (or non-resource URL) in a namespace.
type Request struct {
	Verb string
	// APIGroup is the API group of the resource; the empty string is the core group.
	APIGroup string
	// Resource is the plural resource name. A value of the form "pods/log" is split into resource and subresource.
	Resource    string
	Subresource string
	// Name is the object name. Empty means any object: only rules without `resourceNames` grant it, as for a list
	// or a create request.
	Name string
	// NonResourceURL, when set, asks about a non-resource path such as "/healthz" instead of a resource.
	NonResourceURL string
	// Namespace is the namespace of the request; empty for a cluster-scoped resource or a request across all
	// namespaces, which only a ClusterRoleBinding can grant.
	Namespace string
}

// Query asks whether one subject is granted a Request.
type Query struct {
	Request Request
	Subject Subject
	// Groups are the groups of a User subject. ServiceAccounts get their groups from the namespace.
	Groups []string
}

// Ref names an object of the input.
type Ref struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Grant is one reason a permission is granted: a subject of a binding, the binding, the role, and the rule.
type Grant struct {
	Subject Subject `json:"subject"`
	Binding Ref     `json:"binding"`
	Role    Ref     `json:"role"`
	// Rule is the index of the rule in the (effective) rules of the role.
	Rule int `json:"rule"`
}

// Result is the answer to one Query.
type Result struct {
	Evaluator string  `json:"evaluator"`
	Version   string  `json:"version"`
	Verdict   Verdict `json:"verdict"`
	// Reason is set when the verdict is `unknown`: one of the `evaluate.Reason*` values.
	Reason string `json:"reason,omitempty"`
	// Grants are the bindings and rules that grant the permission.
	Grants []Grant `json:"grants"`
	// Missing are the roles that applicable bindings refer to and that are not in the input.
	Missing     []Ref           `json:"missing"`
	Assumptions []string        `json:"assumptions"`
	Limits      []string        `json:"limits"`
	Gaps        []assurance.Gap `json:"gaps"`
}

// WhoResult answers which subjects are granted a Request.
type WhoResult struct {
	Evaluator string `json:"evaluator"`
	Version   string `json:"version"`
	// Verdict is `granted` when at least one subject is granted, `not-granted` when none is, and `unknown` when none is
	// known to be granted but a binding could not be resolved.
	Verdict     Verdict         `json:"verdict"`
	Reason      string          `json:"reason,omitempty"`
	Grants      []Grant         `json:"grants"`
	Missing     []Ref           `json:"missing"`
	Assumptions []string        `json:"assumptions"`
	Limits      []string        `json:"limits"`
	Gaps        []assurance.Gap `json:"gaps"`
}

// Evaluator answers queries over one Input. It is safe for concurrent use after New.
type Evaluator struct {
	in           *Input
	namespaces   map[string]assurance.Coverage
	roles        map[string]*Role
	clusterRoles map[string]*ClusterRole
	gate         []assurance.Gap
}

// New indexes the input. It does not fail: whatever the evaluator cannot use becomes a gap of the results that
// depend on it.
// The input must not be changed while the evaluator is in use.
func New(input *Input) *Evaluator {
	evaluator := &Evaluator{
		in:           input,
		namespaces:   make(map[string]assurance.Coverage, len(input.Namespaces)),
		roles:        make(map[string]*Role, len(input.Roles)),
		clusterRoles: make(map[string]*ClusterRole, len(input.ClusterRoles)),
	}

	for _, space := range input.Namespaces {
		evaluator.namespaces[space.Name] = space.Coverage
	}

	for i := range input.Roles {
		role := &input.Roles[i]
		evaluator.roles[role.Metadata.Namespace+"/"+role.Metadata.Name] = role
	}

	for i := range input.ClusterRoles {
		role := &input.ClusterRoles[i]
		evaluator.clusterRoles[role.Metadata.Name] = role
	}

	if input.Evidence != evaluate.EvidenceRendered {
		evaluator.gate = []assurance.Gap{{
			Kind:   evaluate.GapDeclaredOnly,
			Detail: fmt.Sprintf("the input has evidence level %q; the evaluator needs %q", input.Evidence, evaluate.EvidenceRendered),
		}}
	}

	return evaluator
}

// InputDigest is the digest of the evaluated input.
func (e *Evaluator) InputDigest() string { return e.in.Digest() }

// Evaluate answers whether the subject is granted the request.
func (e *Evaluator) Evaluate(q *Query) Result {
	res := Result{
		Evaluator: Identifier, Version: Version,
		Grants: []Grant{}, Missing: []Ref{}, Gaps: []assurance.Gap{},
		Assumptions: []string{assumptionAuthorizer},
		Limits:      slices.Clone(Limits),
	}

	scan := e.scan(normalize(&q.Request), subjectMatcher(q.Subject, q.Groups))
	res.Verdict, res.Reason = scan.verdict()
	res.Grants, res.Missing, res.Gaps = scan.grants, scan.missing, scan.gaps

	if res.Verdict != Unknown {
		res.Gaps = []assurance.Gap{}
		res.Missing = []Ref{}
	}

	return res
}

// Who answers which subjects the bindings grant the request to. Every subject of every applicable binding whose
// role grants the request is listed in Grants.
func (e *Evaluator) Who(r *Request) WhoResult {
	scan := e.scan(normalize(r), func(Subject) bool { return true })
	verdict, reason := scan.verdict()

	res := WhoResult{
		Evaluator: Identifier, Version: Version, Verdict: verdict, Reason: reason,
		Grants: scan.grants, Missing: scan.missing, Gaps: scan.gaps,
		Assumptions: []string{assumptionAuthorizer},
		Limits:      slices.Clone(Limits),
	}

	if verdict == NotGranted {
		res.Gaps, res.Missing = []assurance.Gap{}, []Ref{}
	}

	return res
}

// normalize splits "pods/log" into resource and subresource.
func normalize(r *Request) *Request {
	out := *r
	if out.Subresource == "" {
		if res, sub, ok := strings.Cut(out.Resource, "/"); ok {
			out.Resource, out.Subresource = res, sub
		}
	}

	return &out
}

// scanResult is what the walk over the bindings found.
type scanResult struct {
	reason  string
	grants  []Grant
	missing []Ref
	gaps    []assurance.Gap
	// blocked is set when the input does not allow a result at all (declared evidence, coverage).
	blocked bool
}

func (s *scanResult) verdict() (verdict Verdict, reason string) {
	switch {
	case s.blocked:
		return Unknown, s.reason
	case len(s.grants) > 0:
		return Granted, ""
	case len(s.missing) > 0:
		return Unknown, evaluate.ReasonRoleTargetMissing
	case len(s.gaps) > 0:
		return Unknown, evaluate.ReasonUnsupportedConstruct
	}

	return NotGranted, ""
}

// scan walks the bindings that apply to the request's namespace and collects the grants of the subjects that match.
func (e *Evaluator) scan(req *Request, match func(Subject) bool) *scanResult {
	found := &scanResult{grants: []Grant{}, missing: []Ref{}, gaps: []assurance.Gap{}}

	if len(e.gate) > 0 {
		found.blocked, found.reason, found.gaps = true, evaluate.ReasonRenderedEvidenceMissing, slices.Clone(e.gate)

		return found
	}

	if gaps := e.coverageGaps(req.Namespace); len(gaps) > 0 {
		found.blocked, found.reason, found.gaps = true, evaluate.ReasonCoverageIncomplete, gaps

		return found
	}

	for i := range e.in.ClusterRoleBindings {
		b := &e.in.ClusterRoleBindings[i]
		e.bind(found, req, match, &bindingSite{
			ref: Ref{Kind: "ClusterRoleBinding", Name: b.Metadata.Name}, subjects: b.Subjects, roleRef: b.RoleRef,
		})
	}

	// Non-resource URLs are cluster-wide: Kubernetes grants them only through a ClusterRoleBinding, so a RoleBinding to
	// a ClusterRole that lists one grants nothing for them.
	if req.Namespace != "" && req.NonResourceURL == "" {
		for i := range e.in.RoleBindings {
			b := &e.in.RoleBindings[i]
			if b.Metadata.Namespace != req.Namespace {
				continue
			}

			ref := Ref{Kind: "RoleBinding", Namespace: b.Metadata.Namespace, Name: b.Metadata.Name}
			e.bind(found, req, match, &bindingSite{
				ref: ref, namespace: b.Metadata.Namespace, subjects: b.Subjects, roleRef: b.RoleRef,
			})
		}
	}

	slices.SortFunc(found.grants, func(a, b Grant) int { return compareGrants(&a, &b) })
	found.grants = slices.Compact(found.grants)
	slices.SortFunc(found.missing, compareRefs)
	found.missing = slices.Compact(found.missing)
	found.gaps = sortGaps(found.gaps)

	return found
}

func (e *Evaluator) coverageGaps(namespace string) []assurance.Gap {
	var gaps []assurance.Gap

	if e.in.ClusterCoverage != assurance.CoverageComplete {
		gaps = append(gaps, assurance.Gap{
			Kind:   evaluate.GapClusterCoverage,
			Detail: fmt.Sprintf("the cluster-scoped resources have render coverage %q", coverageText(e.in.ClusterCoverage)),
		})
	}

	if namespace == "" {
		return gaps
	}

	cov, ok := e.namespaces[namespace]

	switch {
	case !ok:
		gaps = append(gaps, assurance.Gap{
			Kind: evaluate.GapNamespaceMissing, Detail: fmt.Sprintf("namespace %s is not in the rendered input", namespace),
		})
	case cov != assurance.CoverageComplete:
		gaps = append(gaps, assurance.Gap{
			Kind: evaluate.GapNamespaceCoverage, Detail: fmt.Sprintf("namespace %s has render coverage %q", namespace, coverageText(cov)),
		})
	}

	return gaps
}

func coverageText(c assurance.Coverage) string {
	if c == "" {
		return "not-stated"
	}

	return string(c)
}

func compareRefs(a, b Ref) int {
	return cmpChain(
		canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Namespace, b.Namespace), canonjson.CompareUTF16(a.Name, b.Name),
	)
}

func compareGrants(a, b *Grant) int {
	return cmpChain(
		compareRefs(a.Binding, b.Binding), compareRefs(a.Role, b.Role), a.Rule,
		canonjson.CompareUTF16(a.Subject.Kind, b.Subject.Kind),
		canonjson.CompareUTF16(a.Subject.Namespace, b.Subject.Namespace),
		canonjson.CompareUTF16(a.Subject.Name, b.Subject.Name),
	)
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
