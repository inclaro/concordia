package render

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
)

// Gap kinds that the typed reader and the identity rules record.
const (
	GapUnparseableDocument = "unparseable-document"
	GapInvalidResource     = "invalid-resource"
	GapUnsupportedSelector = "unsupported-selector"
	GapUnparseableImage    = "unparseable-image"
	GapDuplicateIdentity   = "duplicate-identity"
)

const (
	defaultNamespace = "default"
	protocolTCP      = "TCP"
	hookAnnotation   = "helm.sh/hook"
)

// API groups and kinds that the reader and the scope table name.
const (
	groupCore       = ""
	groupApps       = "apps"
	groupBatch      = "batch"
	groupExtensions = "extensions"
	groupNetworking = "networking.k8s.io"
	groupRBAC       = "rbac.authorization.k8s.io"
	groupGateway    = "gateway.networking.k8s.io"
	groupStorage    = "storage.k8s.io"
	groupAdmission  = "admissionregistration.k8s.io"

	kindPod            = "Pod"
	kindService        = "Service"
	kindIngress        = "Ingress"
	kindNetworkPolicy  = "NetworkPolicy"
	kindGateway        = "Gateway"
	kindRole           = "Role"
	kindClusterRole    = "ClusterRole"
	kindRoleBinding    = "RoleBinding"
	kindClusterBinding = "ClusterRoleBinding"
	kindSecret         = "Secret"
	kindConfigMap      = "ConfigMap"
	kindServiceAccount = "ServiceAccount"
	kindCronJob        = "CronJob"
)

// scope says whether a kind lives in a namespace.
type scope int

const (
	scopeUnknown scope = iota
	scopeNamespaced
	scopeCluster
)

type groupKind struct{ group, kind string }

var clusterScoped = map[groupKind]bool{
	{groupCore, "Namespace"}: true, {groupCore, "Node"}: true, {groupCore, "PersistentVolume"}: true,
	{"apiextensions.k8s.io", "CustomResourceDefinition"}: true,
	{groupRBAC, kindClusterRole}:                         true,
	{groupRBAC, kindClusterBinding}:                      true,
	{groupStorage, "StorageClass"}:                       true,
	{groupStorage, "CSIDriver"}:                          true,
	{groupStorage, "CSINode"}:                            true,
	{groupStorage, "VolumeAttachment"}:                   true,
	{groupNetworking, "IngressClass"}:                    true,
	{"scheduling.k8s.io", "PriorityClass"}:               true,
	{"node.k8s.io", "RuntimeClass"}:                      true,
	{groupAdmission, "MutatingWebhookConfiguration"}:     true,
	{groupAdmission, "ValidatingWebhookConfiguration"}:   true,
	{groupAdmission, "ValidatingAdmissionPolicy"}:        true,
	{groupAdmission, "ValidatingAdmissionPolicyBinding"}: true,
	{"apiregistration.k8s.io", "APIService"}:             true,
	{groupGateway, "GatewayClass"}:                       true,
	{"certificates.k8s.io", "CertificateSigningRequest"}: true,
	{"policy", "PodSecurityPolicy"}:                      true,
}

var namespacedKinds = map[groupKind]bool{
	{groupCore, kindPod}: true, {groupCore, kindService}: true, {groupCore, kindServiceAccount}: true,
	{groupCore, kindConfigMap}: true, {groupCore, kindSecret}: true, {groupCore, "PersistentVolumeClaim"}: true,
	{groupCore, "Endpoints"}: true, {groupCore, "ResourceQuota"}: true, {groupCore, "LimitRange"}: true,
	{groupCore, "ReplicationController"}: true,
	{groupApps, "Deployment"}:            true, {groupApps, "StatefulSet"}: true, {groupApps, "DaemonSet"}: true, {groupApps, "ReplicaSet"}: true,
	{groupBatch, "Job"}: true, {groupBatch, kindCronJob}: true,
	{groupNetworking, kindIngress}: true, {groupNetworking, kindNetworkPolicy}: true,
	{groupExtensions, kindIngress}: true,
	{groupRBAC, kindRole}:          true, {groupRBAC, kindRoleBinding}: true,
	{"autoscaling", "HorizontalPodAutoscaler"}: true,
	{"policy", "PodDisruptionBudget"}:          true,
	{"coordination.k8s.io", "Lease"}:           true,
	{"discovery.k8s.io", "EndpointSlice"}:      true,
	{groupGateway, kindGateway}:                true, {groupGateway, "HTTPRoute"}: true,
	{groupGateway, "GRPCRoute"}: true, {groupGateway, "TCPRoute"}: true,
	{groupGateway, "TLSRoute"}: true,
}

func scopeOf(group, kind string) scope {
	key := groupKind{group, kind}

	switch {
	case clusterScoped[key]:
		return scopeCluster
	case namespacedKinds[key]:
		return scopeNamespaced
	default:
		return scopeUnknown
	}
}

// errUnsupported marks a construct that the adapter contract does not support: a coverage gap, not an empty result.
var errUnsupported = errors.New("unsupported")

// docContext is what the reader needs from the source of a document.
type docContext struct {
	environment string
	cluster     string
	// namespace is the namespace of a namespaced resource that names none: the Helm release namespace, else
	// `default` (what `kubectl apply` does).
	namespace string
	location  string
}

// readDocument reads one rendered document. A document that is not a Kubernetes object (no kind or no name) is a
// gap; a `List` is expanded by the caller. The typed parts of the kind are filled in; a part that cannot be read
// is a gap and leaves the identity in place.
func readDocument(doc map[string]any, ctx *docContext) (Resource, []ContainerOccurrence, []assurance.Gap) {
	var gaps []assurance.Gap

	apiVersion := str(doc, "apiVersion")
	kind := str(doc, "kind")
	meta := asMap(doc["metadata"])

	group := groupCore
	if g, _, ok := strings.Cut(apiVersion, "/"); ok {
		group = g
	}

	res := Resource{
		APIVersion: apiVersion, Location: ctx.location, Document: doc,
		Labels: strMap(meta["labels"]), Annotations: strMap(meta["annotations"]),
	}
	res.Hook = res.Annotations[hookAnnotation]

	namespace := str(meta, "namespace")

	switch scopeOf(group, kind) {
	case scopeCluster:
		namespace = ""
	case scopeNamespaced:
		if namespace == "" {
			namespace = ctx.namespace
		}
	case scopeUnknown:
		// A custom kind may be either: the namespace stays as written.
	}

	res.ID = ResourceID{
		Environment: ctx.environment, Cluster: ctx.cluster, Group: group, Kind: kind, Namespace: namespace, Name: str(meta, "name"),
	}

	gap := func(gapKind, detail string) {
		gaps = append(gaps, assurance.Gap{Kind: gapKind, Detail: detail, Path: ctx.location})
	}

	occurrences := readTyped(&res, doc, ctx.namespace, gap)

	for i := range occurrences {
		occurrences[i].Workload = res.ID
	}

	return res, occurrences, gaps
}

// readTyped fills the typed part of the kind of res and gives the container occurrences of a Pod-bearing kind.
func readTyped(res *Resource, doc map[string]any, defaultNS string, gap func(kind, detail string)) []ContainerOccurrence {
	gkey := groupKind{res.ID.Group, res.ID.Kind}

	// The legacy `extensions` group served Ingress and NetworkPolicy before they moved to networking.k8s.io.
	if gkey.group == groupExtensions {
		gkey.group = groupNetworking
	}

	if src, ok := podSourceOf(gkey, doc); ok {
		return readPod(res, src.spec, src.labels, gap)
	}

	spec := asMap(doc["spec"])

	switch gkey.group {
	case groupCore:
		readCoreKind(res, doc, gkey.kind)
	case groupNetworking:
		readNetworkingKind(res, spec, gkey.kind, gap)
	case groupGateway:
		readGatewayKind(res, spec, gkey.kind)
	case groupRBAC:
		readRBACKind(res, doc, defaultNS, gap)
	}

	return nil
}

// podSource is the pod spec of a kind and the labels of its pods.
type podSource struct {
	spec   map[string]any
	labels map[string]string
}

var podTemplateKinds = map[groupKind]bool{
	{groupApps, "Deployment"}: true, {groupApps, "StatefulSet"}: true, {groupApps, "DaemonSet"}: true,
	{groupApps, "ReplicaSet"}: true, {groupBatch, "Job"}: true,
}

// podSourceOf finds the pod spec of a Pod-bearing kind: the Pod itself, the template of a workload, or the job
// template of a CronJob.
func podSourceOf(gk groupKind, doc map[string]any) (podSource, bool) {
	spec := asMap(doc["spec"])

	switch {
	case gk == groupKind{groupCore, kindPod}:
		return podSource{spec: spec, labels: strMap(asMap(doc["metadata"])["labels"])}, true
	case podTemplateKinds[gk]:
		return podTemplateSource(asMap(spec["template"])), true
	case gk == groupKind{groupBatch, kindCronJob}:
		return podTemplateSource(asMap(asMap(asMap(spec["jobTemplate"])["spec"])["template"])), true
	}

	return podSource{}, false
}

func podTemplateSource(tpl map[string]any) podSource {
	return podSource{spec: asMap(tpl["spec"]), labels: strMap(asMap(tpl["metadata"])["labels"])}
}

func readCoreKind(res *Resource, doc map[string]any, kind string) {
	switch kind {
	case kindService:
		res.Service = readService(asMap(doc["spec"]))
	case kindServiceAccount:
		res.ServiceAccount = readServiceAccount(doc)
	case kindSecret, kindConfigMap:
		res.DataKeys = dataKeys(doc)
	}
}

func readNetworkingKind(res *Resource, spec map[string]any, kind string, gap func(kind, detail string)) {
	switch kind {
	case kindNetworkPolicy:
		res.NetworkPolicy = readNetworkPolicy(spec)

		for _, u := range res.NetworkPolicy.Unsupported {
			gap(GapUnsupportedSelector, "NetworkPolicy "+res.ID.Name+": "+u)
		}
	case kindIngress:
		res.Route = readIngress(spec)
	}
}

func readGatewayKind(res *Resource, spec map[string]any, kind string) {
	switch kind {
	case kindGateway:
		res.Gateway = readGateway(spec)
	case "HTTPRoute", "GRPCRoute", "TCPRoute", "TLSRoute":
		res.Route = readGatewayRoute(spec)
	}
}

func readRBACKind(res *Resource, doc map[string]any, defaultNS string, gap func(kind, detail string)) {
	kind := res.ID.Kind

	switch kind {
	case kindRole, kindClusterRole:
		var err error

		if res.Role, err = readRole(doc); err != nil {
			gap(GapUnsupportedSelector, kind+" "+res.ID.Name+": "+err.Error())
		}
	case kindRoleBinding, kindClusterBinding:
		res.Binding = readBinding(doc, defaultNS)
	}
}

// readPod reads the pod facts and the container occurrences of a pod spec.
func readPod(res *Resource, spec map[string]any, labels map[string]string, gap func(kind, detail string)) []ContainerOccurrence {
	account := str(spec, "serviceAccountName")
	if account == "" {
		account = str(spec, "serviceAccount")
	}

	if account == "" {
		account = "default"
	}

	res.Workload = &Workload{PodLabels: labels, ServiceAccountName: account}

	var out []ContainerOccurrence

	for _, group := range []struct{ field, kind string }{
		{"containers", KindContainer}, {"initContainers", KindInit}, {"ephemeralContainers", KindEphemeral},
	} {
		for _, item := range asList(spec[group.field]) {
			c := asMap(item)
			occ := ContainerOccurrence{Container: str(c, "name"), Kind: group.kind, Ports: readContainerPorts(c)}

			ref, err := ParseImageRef(str(c, "image"))
			occ.ImageRef = ref
			occ.Immutable = err == nil && ref.Immutable()

			if err != nil {
				gap(GapUnparseableImage, fmt.Sprintf("%s %s of %s: %v", group.kind, occ.Container, res.ID.Name, err))
			}

			out = append(out, occ)
		}
	}

	return out
}

func readContainerPorts(c map[string]any) []ContainerPort {
	items := asList(c["ports"])
	out := make([]ContainerPort, 0, len(items))

	for _, item := range items {
		p := asMap(item)

		n, _ := intOf(p["containerPort"])
		out = append(out, ContainerPort{Name: str(p, "name"), ContainerPort: n, Protocol: protocolOf(p)})
	}

	return out
}

func protocolOf(p map[string]any) string {
	if s := str(p, "protocol"); s != "" {
		return s
	}

	return protocolTCP
}

func readService(spec map[string]any) *Service {
	svc := &Service{Selector: strMap(spec["selector"]), Type: str(spec, "type")}

	for _, item := range asList(spec["ports"]) {
		p := asMap(item)
		port, _ := intOf(p["port"])
		target := portRef(p["targetPort"])

		if target == (PortRef{}) {
			target = PortRef{Number: port}
		}

		svc.Ports = append(svc.Ports, ServicePort{Name: str(p, "name"), Port: port, Protocol: protocolOf(p), TargetPort: target})
	}

	return svc
}

func readServiceAccount(doc map[string]any) *ServiceAccount {
	account := &ServiceAccount{}

	if v, ok := doc["automountServiceAccountToken"].(bool); ok {
		account.AutomountServiceAccountToken = &v
	}

	for _, item := range asList(doc["imagePullSecrets"]) {
		if n := str(asMap(item), "name"); n != "" {
			account.ImagePullSecrets = append(account.ImagePullSecrets, n)
		}
	}

	return account
}

func dataKeys(doc map[string]any) []string {
	var keys []string

	for _, field := range []string{"data", "stringData", "binaryData"} {
		for k := range asMap(doc[field]) {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}

	slices.Sort(keys)

	return keys
}

// readNetworkPolicy keeps the whole spec. An unsupported construct goes to Unsupported and the rule that holds it is
// kept without it: the caller turns the list into a gap, and the evaluator treats the policy as incomplete.
func readNetworkPolicy(spec map[string]any) *NetworkPolicy {
	policy := &NetworkPolicy{}

	sel, err := readSelector(spec["podSelector"])
	if err != nil {
		policy.Unsupported = append(policy.Unsupported, "podSelector: "+err.Error())
	}

	policy.PodSelector = sel

	for _, t := range asList(spec["policyTypes"]) {
		policy.PolicyTypes = append(policy.PolicyTypes, fmt.Sprint(t))
	}

	policy.Ingress = readRules(spec["ingress"], "from", "ingress", policy)
	policy.Egress = readRules(spec["egress"], "to", "egress", policy)

	return policy
}

func readRules(v any, peerKey, direction string, policy *NetworkPolicy) []NetworkPolicyRule {
	items := asList(v)
	if len(items) == 0 {
		return nil
	}

	out := make([]NetworkPolicyRule, 0, len(items))

	for i, item := range items {
		out = append(out, readRule(asMap(item), fmt.Sprintf("%s[%d].%s", direction, i, peerKey), peerKey, policy))
	}

	return out
}

// readRule reads one ingress or egress rule. where names the rule for the unsupported peers, as `ingress[0].from`.
func readRule(entry map[string]any, where, peerKey string, policy *NetworkPolicy) NetworkPolicyRule {
	rule := NetworkPolicyRule{}

	for j, p := range asList(entry[peerKey]) {
		peer, err := readPeer(asMap(p))
		if err != nil {
			policy.Unsupported = append(policy.Unsupported, fmt.Sprintf("%s[%d]: %v", where, j, err))
		}

		rule.Peers = append(rule.Peers, peer)
	}

	for _, p := range asList(entry["ports"]) {
		rule.Ports = append(rule.Ports, readRulePort(asMap(p)))
	}

	return rule
}

func readRulePort(pm map[string]any) NetworkPolicyPort {
	port := NetworkPolicyPort{Protocol: protocolOf(pm)}

	if ref := portRef(pm["port"]); ref != (PortRef{}) {
		port.Port = &ref
	}

	if n, ok := intOf(pm["endPort"]); ok {
		port.EndPort = &n
	}

	return port
}

func readPeer(raw map[string]any) (NetworkPolicyPeer, error) {
	var (
		peer  NetworkPolicyPeer
		first error
	)

	if raw["podSelector"] != nil {
		sel, err := readSelector(raw["podSelector"])
		peer.PodSelector = &sel
		first = err
	}

	if raw["namespaceSelector"] != nil {
		sel, err := readSelector(raw["namespaceSelector"])
		peer.NamespaceSelector = &sel

		if first == nil {
			first = err
		}
	}

	if ip := asMap(raw["ipBlock"]); ip != nil {
		peer.IPBlock = readIPBlock(ip)
	}

	return peer, first
}

func readIPBlock(ip map[string]any) *IPBlock {
	block := &IPBlock{CIDR: str(ip, "cidr")}
	for _, e := range asList(ip["except"]) {
		block.Except = append(block.Except, fmt.Sprint(e))
	}

	return block
}

// readSelector reads a label selector. A missing or null selector is the empty selector. An operator other than
// In, NotIn, Exists, DoesNotExist, or a malformed expression is errUnsupported.
func readSelector(v any) (LabelSelector, error) {
	obj := asMap(v)

	var sel LabelSelector

	if v != nil && obj == nil {
		return sel, fmt.Errorf("%w: selector is not a mapping", errUnsupported)
	}

	if labels := strMap(obj["matchLabels"]); len(labels) > 0 {
		sel.MatchLabels = labels
	}

	for _, item := range asList(obj["matchExpressions"]) {
		req := readSelectorRequirement(asMap(item))
		if err := checkSelectorRequirement(req); err != nil {
			return sel, err
		}

		sel.MatchExpressions = append(sel.MatchExpressions, req)
	}

	return sel, nil
}

func readSelectorRequirement(e map[string]any) SelectorRequirement {
	req := SelectorRequirement{Key: str(e, "key"), Operator: str(e, "operator")}

	for _, val := range asList(e["values"]) {
		req.Values = append(req.Values, fmt.Sprint(val))
	}

	return req
}

func checkSelectorRequirement(req SelectorRequirement) error {
	switch req.Operator {
	case "In", "NotIn":
		if len(req.Values) == 0 || req.Key == "" {
			return fmt.Errorf("%w: expression %q needs a key and values", errUnsupported, req.Operator)
		}
	case "Exists", "DoesNotExist":
		if req.Key == "" || len(req.Values) > 0 {
			return fmt.Errorf("%w: expression %q needs a key and no values", errUnsupported, req.Operator)
		}
	default:
		return fmt.Errorf("%w: expression operator %q", errUnsupported, req.Operator)
	}

	return nil
}

func readIngress(spec map[string]any) *Route {
	route := &Route{}

	addIngressBackend(route, asMap(spec["defaultBackend"]))
	addIngressBackend(route, asMap(spec["backend"]))

	for _, r := range asList(spec["rules"]) {
		rm := asMap(r)

		if h := str(rm, "host"); h != "" && !slices.Contains(route.Hosts, h) {
			route.Hosts = append(route.Hosts, h)
		}

		for _, p := range asList(asMap(rm["http"])["paths"]) {
			addIngressBackend(route, asMap(asMap(p)["backend"]))
		}
	}

	return route
}

// addIngressBackend adds the backend of the current (`service`) or the legacy (`serviceName`) Ingress form.
func addIngressBackend(route *Route, m map[string]any) {
	if m == nil {
		return
	}

	if svc := asMap(m["service"]); svc != nil {
		route.addBackend(Backend{Service: str(svc, "name"), Port: portRef(asMap(svc["port"])["number"], asMap(svc["port"])["name"])})
	} else if name := str(m, "serviceName"); name != "" {
		route.addBackend(Backend{Service: name, Port: portRef(m["servicePort"])})
	}
}

func (r *Route) addBackend(b Backend) {
	if b.Service != "" && !slices.Contains(r.Backends, b) {
		r.Backends = append(r.Backends, b)
	}
}

func readGatewayRoute(spec map[string]any) *Route {
	route := &Route{}

	for _, h := range asList(spec["hostnames"]) {
		route.Hosts = append(route.Hosts, fmt.Sprint(h))
	}

	for _, p := range asList(spec["parentRefs"]) {
		route.ParentRefs = append(route.ParentRefs, readParentRef(asMap(p)))
	}

	for _, rule := range asList(spec["rules"]) {
		for _, b := range asList(asMap(rule)["backendRefs"]) {
			if backend, ok := readBackendRef(asMap(b)); ok {
				route.addBackend(backend)
			}
		}
	}

	return route
}

func readParentRef(pm map[string]any) ParentRef {
	kind := str(pm, "kind")
	if kind == "" {
		kind = "Gateway"
	}

	return ParentRef{Kind: kind, Namespace: str(pm, "namespace"), Name: str(pm, "name")}
}

// readBackendRef reads a backend of a gateway route. A reference to anything but a Service is not a backend.
func readBackendRef(bm map[string]any) (Backend, bool) {
	if k := str(bm, "kind"); k != "" && k != "Service" {
		return Backend{}, false
	}

	return Backend{Service: str(bm, "name"), Namespace: str(bm, "namespace"), Port: portRef(bm["port"])}, true
}

func readGateway(spec map[string]any) *Gateway {
	gw := &Gateway{ClassName: str(spec, "gatewayClassName")}

	for _, l := range asList(spec["listeners"]) {
		lm := asMap(l)
		port, _ := intOf(lm["port"])
		gw.Listeners = append(gw.Listeners, GatewayListener{
			Name: str(lm, "name"), Port: port, Protocol: str(lm, "protocol"), Hostname: str(lm, "hostname"),
		})
	}

	return gw
}

func readRole(doc map[string]any) (*Role, error) {
	role := &Role{}

	for _, item := range asList(doc["rules"]) {
		rule := asMap(item)
		role.Rules = append(role.Rules, PolicyRule{
			APIGroups: strList(rule["apiGroups"]), Resources: strList(rule["resources"]), ResourceNames: strList(rule["resourceNames"]),
			Verbs: strList(rule["verbs"]), NonResourceURLs: strList(rule["nonResourceURLs"]),
		})
	}

	var first error

	if agg := asMap(doc["aggregationRule"]); agg != nil {
		role.AggregationRule = &AggregationRule{}

		for _, s := range asList(agg["clusterRoleSelectors"]) {
			sel, err := readSelector(s)
			if err != nil && first == nil {
				first = err
			}

			role.AggregationRule.ClusterRoleSelectors = append(role.AggregationRule.ClusterRoleSelectors, sel)
		}
	}

	return role, first
}

func readBinding(doc map[string]any, defaultNS string) *Binding {
	ref := asMap(doc["roleRef"])
	binding := &Binding{RoleRef: RoleRef{APIGroup: str(ref, "apiGroup"), Kind: str(ref, "kind"), Name: str(ref, "name")}}

	for _, item := range asList(doc["subjects"]) {
		entry := asMap(item)
		subject := Subject{Kind: str(entry, "kind"), APIGroup: str(entry, "apiGroup"), Name: str(entry, "name"), Namespace: str(entry, "namespace")}

		// A ServiceAccount subject without a namespace is not valid in a ClusterRoleBinding; in a RoleBinding the
		// namespace of the binding applies, which is its own namespace.
		if subject.Kind == kindServiceAccount && subject.Namespace == "" {
			subject.Namespace = str(asMap(doc["metadata"]), "namespace")
			if subject.Namespace == "" && str(doc, "kind") == kindRoleBinding {
				subject.Namespace = defaultNS
			}
		}

		binding.Subjects = append(binding.Subjects, subject)
	}

	return binding
}

// asMap gives the value as a mapping with string keys, or nil.
func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	default:
		return nil
	}
}

func asList(v any) []any {
	l, _ := v.([]any)

	return l
}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// strMap reads a mapping of scalar values as strings; non-mappings give nil.
func strMap(v any) map[string]string {
	m := asMap(v)
	if len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}

	return out
}

func strList(v any) []string {
	items := asList(v)
	if len(items) == 0 {
		return nil
	}

	out := make([]string, 0, len(items))

	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}

	return out
}

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}

	return 0, false
}

// portRef reads an int-or-string port from the first argument that holds one.
func portRef(values ...any) PortRef {
	for _, v := range values {
		if n, ok := intOf(v); ok {
			return PortRef{Number: n}
		}

		if s, ok := v.(string); ok && s != "" {
			return PortRef{Name: s}
		}
	}

	return PortRef{}
}
