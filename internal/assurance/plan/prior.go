package plan

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/inclaro/concordia/internal/redact"
)

// priorOwnerTypes are the firewall rule resources whose owner the reader keeps from before a delete or a
// replacement. The inline rule list of a security group or NSG in a plan against existing state is a copy of all its
// rules, so it can still hold a rule that the plan deletes; the owner says which list that is.
var priorOwnerTypes = []string{
	"aws_security_group_rule", "aws_vpc_security_group_ingress_rule", "aws_vpc_security_group_egress_rule",
	"azurerm_network_security_rule",
}

// priorOwnerKeys are the attributes that name the owner of a firewall rule resource. No other attribute of the
// values before a change is ever read.
var priorOwnerKeys = []string{"security_group_id", "network_security_group_name", "resource_group_name"}

// priorPath is the first segment of the property of a redaction of a value before the change.
const priorPath = "before"

// priorOwner reads the owner attributes of a firewall rule resource that the plan deletes or replaces from the
// values before the change. Only the owner keys are decoded. A value that is not a non-empty string, or that the
// redaction rules change (it is marked sensitive or holds a credential), is left out; its redaction is recorded.
func (r *reader) priorOwner(item *resourceChange) (map[string]string, []redact.Record) {
	if !slices.Contains(priorOwnerTypes, item.Type) || !slices.Contains(item.Change.Actions, "delete") {
		return nil, nil
	}

	var before map[string]json.RawMessage
	if json.Unmarshal(item.Change.Before, &before) != nil {
		return nil, nil
	}

	walk := walker{address: item.Address, file: r.file}
	root := &node{sensitive: decodeMirror(item.Change.BeforeSensitive), path: priorPath}

	if isTrue(root.sensitive) {
		return nil, nil
	}

	owner := map[string]string{}

	for _, key := range priorOwnerKeys {
		if text, ok := walk.priorText(root, key, before[key]); ok {
			owner[key] = text
		}
	}

	if len(owner) == 0 {
		return nil, walk.redactions
	}

	return owner, walk.redactions
}

// priorText is the cleaned string of one owner attribute. ok is false when the attribute is absent, is not a
// non-empty string, or was redacted.
func (w *walker) priorText(root *node, key string, raw json.RawMessage) (string, bool) {
	var value any
	if decodeValue(raw, &value) != nil {
		return "", false
	}

	redacted := len(w.redactions)
	cleaned, _ := w.value(root.child(value, key))
	text, isText := cleaned.(string)

	return text, isText && text != "" && len(w.redactions) == redacted
}

// decodeMirror decodes a sensitivity mirror; a mirror that does not decode marks nothing.
func decodeMirror(raw json.RawMessage) any {
	var mirror any
	if decodeValue(raw, &mirror) != nil {
		return nil
	}

	return mirror
}

// decodeValue decodes JSON with numbers kept as json.Number, as the rest of the reader does.
func decodeValue(raw json.RawMessage, into *any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	return dec.Decode(into)
}
