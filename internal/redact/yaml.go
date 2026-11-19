package redact

import (
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/yamlparse"
)

const (
	keyData         = "data"
	keyStringData   = "stringData"
	lastAppliedConf = "kubectl.kubernetes.io/last-applied-configuration"
	nullTag         = "!!null"
	boolTag         = "!!bool"
)

// envKeys are the keys whose children are environment variables or build arguments.
var envKeys = map[string]struct{}{
	"environment": {}, "env": {}, "args": {}, "build_args": {}, "variables": {},
}

var documentSeparator = regexp.MustCompile(`^(---|\.{3})(\s|$)`)

// yamlPass redacts the values of Kubernetes, Compose, Helm, and JSON documents by structure:
// Secret `data` and `stringData`, environment values with a sensitive name, and any scalar
// under a sensitive key. A document that does not parse gets the line pass of key-value text.
func yamlPass(buf *buffer) {
	start := 0

	for i := 0; i <= len(buf.lines); i++ {
		if i < len(buf.lines) && !documentSeparator.MatchString(buf.lines[i].text) {
			continue
		}

		yamlDocument(buf, start, i)

		start = i + 1
	}
}

// yamlDocument redacts the lines from..to (to exclusive), which are one YAML document.
func yamlDocument(buf *buffer, from, stop int) {
	if from >= stop {
		return
	}

	texts := make([]string, 0, stop-from)
	for _, l := range buf.lines[from:stop] {
		texts = append(texts, l.text)
	}

	root, problems := yamlparse.ParseNode([]byte(strings.Join(texts, "\n")))
	if len(problems) > 0 {
		keyValuePass(buf, from, stop, false)

		return
	}

	if root == nil {
		return
	}

	p := &yamlWalk{buf: buf, offset: from, done: map[*yaml.Node]struct{}{}}
	p.walk(root, false)
	buf.apply(p.edits)

	for _, d := range p.drops {
		buf.drop(d[0], d[1])
	}
}

type yamlWalk struct {
	buf    *buffer
	done   map[*yaml.Node]struct{}
	edits  []edit
	drops  [][2]int
	offset int
}

func isScalar(n *yaml.Node) bool { return n != nil && n.Kind == yaml.ScalarNode }

// scalarPair says whether both nodes are scalars.
func scalarPair(a, b *yaml.Node) bool { return isScalar(a) && isScalar(b) }

// isScalarValue says whether n is a scalar with exactly this text.
func isScalarValue(n *yaml.Node, value string) bool { return isScalar(n) && n.Value == value }

// isCollection says whether n is a mapping or a list.
func isCollection(n *yaml.Node) bool {
	return n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode
}

// inFlow says whether n is in flow style, given that its parent is (flow) or is not.
func inFlow(n *yaml.Node, flow bool) bool { return flow || n.Style&yaml.FlowStyle != 0 }

func (p *yamlWalk) walk(n *yaml.Node, flow bool) {
	switch n.Kind {
	case yaml.MappingNode:
		p.mapping(n, inFlow(n, flow))
	case yaml.SequenceNode:
		p.sequence(n, inFlow(n, flow), false)
	case yaml.DocumentNode, yaml.AliasNode, yaml.ScalarNode:
		for _, c := range n.Content {
			p.walk(c, flow)
		}
	}
}

func isSecretMapping(n *yaml.Node) bool {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "kind" && isScalarValue(n.Content[i+1], "Secret") {
			return true
		}
	}

	return false
}

func (p *yamlWalk) mapping(n *yaml.Node, flow bool) {
	secret := isSecretMapping(n)

	for i := 0; i+1 < len(n.Content); i += 2 {
		p.entry(n.Content[i], n.Content[i+1], secret, flow)
	}
}

// entry redacts one `key: value` pair of a mapping. secret says that the mapping is a Secret.
func (p *yamlWalk) entry(key, val *yaml.Node, secret, flow bool) {
	name := key.Value

	switch {
	case !isScalar(key):
		p.walk(val, flow)
	case isSecretData(secret, name, val):
		p.secretData(val, inFlow(val, flow))
	case isScalar(val) && isSecretScalarName(name):
		p.redactScalar(val, key, ClassSecretData, flow)
	case isEnvContainer(name, val):
		p.envContainer(val, inFlow(val, flow))
	case holdsSecretValues(name, val):
		p.redactAll(val, key, inFlow(val, flow))
	default:
		p.walk(val, flow)
	}
}

// isSecretData says whether the entry is the `data` or `stringData` mapping of a Secret.
func isSecretData(secret bool, name string, val *yaml.Node) bool {
	if !secret || val.Kind != yaml.MappingNode {
		return false
	}

	return name == keyData || name == keyStringData
}

// isEnvContainer says whether the entry is a mapping or list of environment variables.
func isEnvContainer(name string, val *yaml.Node) bool { return isEnvKey(name) && isCollection(val) }

// isSecretScalarName says whether a scalar under the key is a secret value.
func isSecretScalarName(name string) bool { return name == lastAppliedConf || sensitiveName(name) }

// holdsSecretValues says whether a sensitive key holds a list or mapping of secret values. A key
// that lists the names of secrets holds references, and a marker list is already redacted.
func holdsSecretValues(name string, val *yaml.Node) bool {
	if name == keySecrets || !sensitiveName(name) {
		return false
	}

	return isCollection(val) && !isMarkerList(val)
}

// secretData redacts every value of the `data` or `stringData` mapping of a Secret.
func (p *yamlWalk) secretData(val *yaml.Node, flow bool) {
	for j := 0; j+1 < len(val.Content); j += 2 {
		p.redactScalar(val.Content[j+1], val.Content[j], ClassSecretData, flow)
	}
}

// envContainer walks the mapping or list that an environment key holds.
func (p *yamlWalk) envContainer(val *yaml.Node, flow bool) {
	if val.Kind == yaml.MappingNode {
		p.envMapping(val, flow)

		return
	}

	p.sequence(val, flow, true)
}

// redactAll redacts every scalar below n with the property of key, the sensitive key that holds n. A
// nested key that names a reference to a secret (secretName, tokenFile) keeps its value.
func (p *yamlWalk) redactAll(node, key *yaml.Node, flow bool) {
	if node.Kind == yaml.MappingNode {
		p.redactAllMapping(node, key, flow)

		return
	}

	for _, child := range node.Content {
		p.redactChild(child, child, key, flow)
	}
}

// redactAllMapping redacts the values of a mapping. A mapping key is not a value, and a pointer stays.
func (p *yamlWalk) redactAllMapping(node, key *yaml.Node, flow bool) {
	for i := 1; i < len(node.Content); i += 2 {
		name := node.Content[i-1]
		if isScalar(name) && isPointerName(name.Value) {
			continue
		}

		p.redactChild(node.Content[i], name, key, flow)
	}
}

// redactChild redacts a scalar child, and looks below any other child that is not a marker. A value
// that spans lines continues at a deeper indentation than its owner, the key or item that holds it.
func (p *yamlWalk) redactChild(child, owner, key *yaml.Node, flow bool) {
	if isScalar(child) {
		p.redactScalarAs(child, owner, redaction{property: key.Value, class: ClassSecretData, flow: flow})

		return
	}

	if !isMarkerList(child) {
		p.redactAll(child, key, inFlow(child, flow))
	}
}

// keySecrets is the Compose and Kubernetes key that lists the names of secrets: references, not values.
const keySecrets = "secrets"

// markerBody is a marker without its opening bracket.
const markerBody = "REDACTED:"

// isMarkerList says whether n is a marker that a previous pass wrote and that reads as a flow list:
// `[REDACTED:secret-data]`.
func isMarkerList(n *yaml.Node) bool {
	if n.Kind != yaml.SequenceNode || len(n.Content) != 1 {
		return false
	}

	if n.Style&yaml.FlowStyle == 0 {
		return false
	}

	item := n.Content[0]

	return isScalar(item) && strings.HasPrefix(item.Value, markerBody)
}

func isEnvKey(name string) bool {
	_, ok := envKeys[name]

	return ok
}

// envMapping: `KEY: value` pairs. A sensitive name gives env-value.
func (p *yamlWalk) envMapping(n *yaml.Node, flow bool) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]

		if scalarPair(key, val) && sensitiveName(key.Value) {
			p.redactScalar(val, key, ClassEnvValue, flow)

			continue
		}

		p.walk(val, flow)
	}
}

// sequence walks the items of a list. In an environment list (`env`, `args`, Compose
// `environment`) a text item `KEY=value` is an assignment.
func (p *yamlWalk) sequence(n *yaml.Node, flow, env bool) {
	for _, item := range n.Content {
		switch {
		case item.Kind == yaml.MappingNode:
			p.namedValue(item, inFlow(item, flow))
			p.walk(item, flow)
		case env && isScalar(item):
			p.redactAfterEquals(item, flow)
		default:
			p.walk(item, flow)
		}
	}
}

// namedValue handles a mapping with `name: DB_PASSWORD` and `value: x`: the Kubernetes env item.
func (p *yamlWalk) namedValue(n *yaml.Node, flow bool) {
	var name, value, valueKey *yaml.Node

	for i := 0; i+1 < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "name":
			name = n.Content[i+1]
		case "value":
			value, valueKey = n.Content[i+1], n.Content[i]
		}
	}

	if scalarPair(name, value) && sensitiveName(name.Value) {
		p.redactScalarAs(value, valueKey, redaction{property: name.Value, class: ClassEnvValue, flow: flow})
	}
}
