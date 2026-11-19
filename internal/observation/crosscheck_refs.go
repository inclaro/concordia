package observation

import "go.yaml.in/yaml/v3"

const (
	refEnv        = "env"
	refEnvFrom    = "envFrom"
	refVolume     = "volume"
	refSA         = "serviceAccountName"
	refTLS        = "tls"
	refParentRef  = "parentRef"
	refRoleRef    = "roleRef"
	refSubject    = "subject"
	refScaleTgt   = "scaleTarget"
	defaultSAName = "default"
)

// reference is one use of another resource by its kind and name.
type reference struct {
	mode      string
	kind      string
	name      string
	namespace string
	line      int
}

// refTarget is the kind of the resource that a reference uses, and how the reference uses it.
type refTarget struct {
	mode string
	kind string
}

// references gives the uses of other resources that a document makes.
func references(doc *manifestDoc) []reference {
	switch doc.kind {
	case "Ingress":
		return ingressRefs(doc)
	case "HTTPRoute":
		return routeRefs(doc)
	case "RoleBinding", "ClusterRoleBinding":
		return bindingRefs(doc)
	case "HorizontalPodAutoscaler":
		return scaleRefs(doc)
	}

	if IsWorkloadKind(doc.kind) {
		return podRefs(doc)
	}

	return nil
}

// ref builds a reference from the name node of a field. It gives false for a missing or empty name.
func (d *manifestDoc) ref(target refTarget, keyNode, v *yaml.Node) (reference, bool) {
	nm := name(v)
	if nm == "" || keyNode == nil {
		return reference{}, false
	}

	ns := d.namespace
	if target.kind == kindClusterRole {
		ns = ""
	}

	return reference{mode: target.mode, kind: target.kind, name: nm, namespace: ns, line: d.line(keyNode)}, true
}

// kindedRef builds a reference from a node that names the kind and the name of its target. It gives false when
// the kind is missing or the name is missing or empty.
func (d *manifestDoc) kindedRef(mode string, node *yaml.Node) (reference, bool) {
	kind := name(fieldVal(node, "kind"))
	if kind == "" {
		return reference{}, false
	}

	k, v := field(node, "name")

	return d.ref(refTarget{mode: mode, kind: kind}, k, v)
}

// namespacedRef builds a reference from a node that names its target, and takes the namespace of the target from the node when it has one.
func (d *manifestDoc) namespacedRef(target refTarget, node *yaml.Node) (reference, bool) {
	k, v := field(node, "name")

	r, ok := d.ref(target, k, v)
	if !ok {
		return reference{}, false
	}

	if ns := name(fieldVal(node, "namespace")); ns != "" {
		r.namespace = ns
	}

	return r, true
}

// refCollector gathers the references of one document.
type refCollector struct {
	doc  *manifestDoc
	refs []reference
}

// add keeps the reference that the field `key` of `parent` gives, when there is one.
func (rc *refCollector) add(target refTarget, parent *yaml.Node, key string) {
	k, v := field(parent, key)
	if r, ok := rc.doc.ref(target, k, v); ok {
		rc.refs = append(rc.refs, r)
	}
}

func podRefs(doc *manifestDoc) []reference {
	pod := podSpec(doc)
	if pod == nil {
		return nil
	}

	collected := &refCollector{doc: doc}

	if k, v := field(pod, "serviceAccountName"); name(v) != defaultSAName {
		if r, ok := doc.ref(refTarget{mode: refSA, kind: kindServiceAcct}, k, v); ok {
			collected.refs = append(collected.refs, r)
		}
	}

	for _, ct := range containers(pod) {
		collected.envRefs(ct)
	}

	for _, vol := range items(fieldVal(pod, "volumes")) {
		collected.add(refTarget{refVolume, kindPVC}, fieldVal(vol, "persistentVolumeClaim"), "claimName")
		collected.add(refTarget{refVolume, kindSecret}, fieldVal(vol, "secret"), "secretName")
		collected.add(refTarget{refVolume, kindConfigMap}, fieldVal(vol, "configMap"), "name")
	}

	return collected.refs
}

func (rc *refCollector) envRefs(ct *yaml.Node) {
	for _, e := range items(fieldVal(ct, "env")) {
		from := fieldVal(e, "valueFrom")
		rc.add(refTarget{refEnv, kindSecret}, fieldVal(from, "secretKeyRef"), "name")
		rc.add(refTarget{refEnv, kindConfigMap}, fieldVal(from, "configMapKeyRef"), "name")
	}

	for _, e := range items(fieldVal(ct, "envFrom")) {
		rc.add(refTarget{refEnvFrom, kindSecret}, fieldVal(e, "secretRef"), "name")
		rc.add(refTarget{refEnvFrom, kindConfigMap}, fieldVal(e, "configMapRef"), "name")
	}
}

func ingressRefs(doc *manifestDoc) []reference {
	rc := &refCollector{doc: doc}

	for _, t := range items(fieldVal(fieldVal(doc.root, "spec"), "tls")) {
		rc.add(refTarget{refTLS, kindSecret}, t, "secretName")
	}

	return rc.refs
}

func routeRefs(doc *manifestDoc) []reference {
	var refs []reference

	for _, parent := range items(fieldVal(fieldVal(doc.root, "spec"), "parentRefs")) {
		kind := kindGateway
		if k := name(fieldVal(parent, "kind")); k != "" {
			kind = k
		}

		if r, ok := doc.namespacedRef(refTarget{refParentRef, kind}, parent); ok {
			refs = append(refs, r)
		}
	}

	return refs
}

func bindingRefs(doc *manifestDoc) []reference {
	var refs []reference

	if r, ok := doc.kindedRef(refRoleRef, fieldVal(doc.root, "roleRef")); ok {
		refs = append(refs, r)
	}

	for _, subject := range items(fieldVal(doc.root, "subjects")) {
		if name(fieldVal(subject, "kind")) != kindServiceAcct {
			continue
		}

		if r, ok := doc.namespacedRef(refTarget{refSubject, kindServiceAcct}, subject); ok {
			refs = append(refs, r)
		}
	}

	return refs
}

func scaleRefs(d *manifestDoc) []reference {
	r, ok := d.kindedRef(refScaleTgt, fieldVal(fieldVal(d.root, "spec"), "scaleTargetRef"))
	if !ok {
		return nil
	}

	return []reference{r}
}
