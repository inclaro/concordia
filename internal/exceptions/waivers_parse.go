package exceptions

import (
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
)

// waiverCursor is the position in the waiver file that a parse step validates: the URI and the JSON Pointer for the
// diagnostics, and the parse options.
type waiverCursor struct {
	cfg     *parseConfig
	uri     string
	pointer string
}

// child gives the cursor of a key or an index below this position.
func (c waiverCursor) child(key string) waiverCursor {
	return waiverCursor{cfg: c.cfg, uri: c.uri, pointer: c.pointer + "/" + key}
}

// fail makes a CO-EXC-002 error at this position.
func (c waiverCursor) fail(detail string, line int) *diag.Error {
	return waiverProblem(c.uri, detail, c.pointer, line)
}

// waiverProblem makes a CO-EXC-002 error for the waiver file.
func waiverProblem(uri, detail, pointer string, line int) *diag.Error {
	return diag.Fail(diag.EXC002, diag.Detail(detail), diag.WithSource(diag.Source{URI: uri, Pointer: pointer, Line: line}))
}

// invalidWaiverYAML makes the CO-EXC-002 error for text that is not valid YAML.
func invalidWaiverYAML(uri string, err error) *diag.Error {
	return waiverProblem(uri, "invalid YAML: "+strings.TrimPrefix(err.Error(), "yaml: "), "", 0)
}

// resolveAlias follows YAML aliases to the node that they name.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}

	return n
}

// documentRoot gives the content node of a parsed document, or nil when the document is empty.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}

		return resolveAlias(doc.Content[0])
	}

	if doc.Kind == 0 {
		return nil
	}

	return resolveAlias(doc)
}

// isNull reports whether the node is a YAML null (for example, "~" or an empty value).
func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// mapValue gives the value node of a key in a mapping node.
func mapValue(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := resolveAlias(m.Content[i])
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return resolveAlias(m.Content[i+1]), true
		}
	}

	return nil, false
}

// textValue gives the text of a node that the YAML core schema reads as text.
// A bare date (2026-12-31) is text here, as the TypeScript parser reads it.
func textValue(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}

	switch n.ShortTag() {
	case tagStr, "!!timestamp":
		return n.Value, true
	default:
		return "", false
	}
}

// lineOf gives the line of v, or of the fallback node when v is missing.
func lineOf(v, fallback *yaml.Node) int {
	if v != nil {
		return v.Line
	}

	return fallback.Line
}

// unmarshalWaiverText decodes the text of a waiver file to a YAML node tree.
func unmarshalWaiverText(text, uri string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, invalidWaiverYAML(uri, err)
	}

	return &doc, nil
}

// ParseWaivers parses and validates the text of a waiver file. The uri is for
// the diagnostics. An empty document, or a document without "waivers", gives no waivers.
//
// A finding waiver has `finding` and ignores unknown keys. A scoped waiver has `scope`, `approver`, and optional
// `compensatingEvidence`, and rejects unknown keys. Exactly one of `finding` and `scope` is required.
//
// Errors are *diag.Error: CO-EXC-002 (not valid) and CO-EXC-004 (duplicate ID); exit code 2.
func ParseWaivers(text, uri string, opts ...ParseOption) ([]Waiver, error) {
	var cfg parseConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	list, err := waiverList(text, uri)
	if err != nil {
		return nil, err
	}

	return parseWaiverList(list, waiverCursor{cfg: &cfg, uri: uri, pointer: "/" + waiversKey})
}

// noWaiverList is the waiver sequence of a document without waivers.
func noWaiverList() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq}
}

// waiverList gives the "waivers" sequence node of the text, or an empty sequence when the document has no waivers.
func waiverList(text, uri string) (*yaml.Node, error) {
	// The generic decode finds the syntax errors and the duplicate keys.
	var generic any
	if err := yaml.Unmarshal([]byte(text), &generic); err != nil {
		return nil, invalidWaiverYAML(uri, err)
	}

	doc, err := unmarshalWaiverText(text, uri)
	if err != nil {
		return nil, err
	}

	root := documentRoot(doc)
	if root == nil || isNull(root) {
		return noWaiverList(), nil
	}

	if root.Kind != yaml.MappingNode {
		return nil, waiverProblem(uri, "not an object", "", root.Line)
	}

	list, ok := mapValue(root, waiversKey)
	if !ok || isNull(list) {
		return noWaiverList(), nil
	}

	if list.Kind != yaml.SequenceNode {
		return nil, waiverProblem(uri, "waivers must be an array", "/"+waiversKey, list.Line)
	}

	return list, nil
}

// parseWaiverList validates each item of the waiver sequence and rejects a duplicate ID (CO-EXC-004).
func parseWaiverList(list *yaml.Node, cur waiverCursor) ([]Waiver, error) {
	out := make([]Waiver, 0, len(list.Content))
	seen := make(map[string]struct{}, len(list.Content))

	for i, item := range list.Content {
		itemCur := cur.child(strconv.Itoa(i))

		parsed, err := parseWaiver(resolveAlias(item), itemCur)
		if err != nil {
			return nil, err
		}

		if _, dup := seen[parsed.ID]; dup {
			source := diag.Source{URI: cur.uri, Pointer: itemCur.child(idKey).pointer, Line: item.Line}

			return nil, diag.Fail(diag.EXC004, diag.Params{idKey: parsed.ID}, diag.WithSource(source))
		}

		seen[parsed.ID] = struct{}{}

		out = append(out, parsed)
	}

	return out, nil
}

// parseWaiver validates one waiver node at the cursor.
func parseWaiver(node *yaml.Node, cur waiverCursor) (Waiver, error) {
	if node.Kind != yaml.MappingNode {
		return Waiver{}, cur.fail("not an object", node.Line)
	}

	values := make(map[string]string, len(requiredWaiverKeys))

	for _, key := range requiredWaiverKeys {
		text, err := requiredText(node, key, cur)
		if err != nil {
			return Waiver{}, err
		}

		values[key] = text
	}

	finding, hasFinding := mapValue(node, findingKey)
	scope, hasScope := mapValue(node, scopeKey)

	if hasFinding == hasScope {
		return Waiver{}, cur.fail("exactly one of 'finding' and 'scope' is required", node.Line)
	}

	if err := checkExpiresDay(node, values[expiresKey], cur); err != nil {
		return Waiver{}, err
	}

	entry := Waiver{
		ID:      values[idKey],
		Rule:    values[ruleKey],
		Owner:   values[ownerKey],
		Reason:  values[reasonKey],
		Expires: values[expiresKey],
	}

	var err error
	if hasFinding {
		entry.Finding, err = parseFinding(node, finding, cur)
	} else {
		err = parseScoped(node, scope, &entry, cur)
	}

	if err != nil {
		return Waiver{}, err
	}

	if entry.Issue, err = parseIssue(node, cur); err != nil {
		return Waiver{}, err
	}

	return entry, nil
}

// checkExpiresDay fails when the expiry text is not a YYYY-MM-DD date.
func checkExpiresDay(node *yaml.Node, expires string, cur waiverCursor) error {
	if _, ok := ParseDay(expires); ok {
		return nil
	}

	v, _ := mapValue(node, expiresKey)

	return cur.child(expiresKey).fail("expires must be YYYY-MM-DD", lineOf(v, node))
}

// parseFinding validates the fingerprint of a finding waiver.
func parseFinding(node, finding *yaml.Node, cur waiverCursor) (string, error) {
	text, ok := textValue(finding)
	if !ok || !fingerprintPattern.MatchString(text) {
		return "", cur.child(findingKey).fail("finding must be a sha256: fingerprint", lineOf(finding, node))
	}

	return text, nil
}

// parseIssue gives the optional issue text of a waiver node. A missing issue is the empty text.
func parseIssue(node *yaml.Node, cur waiverCursor) (string, error) {
	v, present := mapValue(node, issueKey)
	if !present {
		return "", nil
	}

	issue, ok := textValue(v)
	if !ok {
		return "", cur.child(issueKey).fail("issue must be a string", v.Line)
	}

	return issue, nil
}

// parseScoped validates the parts that only a scoped waiver has and fills them in entry: the closed key list, the
// scope, the approver, the compensating evidence, and the expiry limit.
func parseScoped(node, scope *yaml.Node, entry *Waiver, cur waiverCursor) error {
	if err := rejectUnknownKeys(node, scopedWaiverKeys, cur); err != nil {
		return err
	}

	if strings.ContainsAny(entry.Rule, wildcardChars) {
		return cur.child(ruleKey).fail("rule must not be a wildcard", node.Line)
	}

	parsed, err := parseScope(scope, cur.child(scopeKey))
	if err != nil {
		return err
	}

	entry.Scope = parsed

	if entry.Approver, err = parseScopedApprover(node, cur); err != nil {
		return err
	}

	if entry.CompensatingEvidence, err = parseScopedEvidence(node, cur); err != nil {
		return err
	}

	return checkExpiryLimit(node, entry, cur)
}

// parseScopedApprover validates the approver of a scoped waiver, which is required.
func parseScopedApprover(node *yaml.Node, cur waiverCursor) (*Approver, error) {
	approver, present := mapValue(node, approverKey)
	if !present {
		return nil, cur.child(approverKey).fail("missing required property 'approver'", node.Line)
	}

	return parseApprover(approver, cur.child(approverKey))
}

// parseScopedEvidence validates the optional compensating evidence of a scoped waiver.
func parseScopedEvidence(node *yaml.Node, cur waiverCursor) ([]Evidence, error) {
	list, ok := mapValue(node, evidenceKey)
	if !ok {
		return nil, nil
	}

	return parseEvidence(list, cur.child(evidenceKey))
}

// checkExpiryLimit applies `exceptions.maxExpiryDays` to a scoped waiver.
func checkExpiryLimit(node *yaml.Node, entry *Waiver, cur waiverCursor) error {
	if cur.cfg.maxExpiryDays <= 0 {
		return nil
	}

	day, _ := ParseDay(entry.Expires)
	today := cur.cfg.now.UTC().Truncate(dayLength)
	days := int(day.Sub(today) / dayLength)

	if days <= cur.cfg.maxExpiryDays {
		return nil
	}

	expires, _ := mapValue(node, expiresKey)
	detail := fmt.Sprintf("expires %s is %d days away, above exceptions.maxExpiryDays %d", entry.Expires, days, cur.cfg.maxExpiryDays)

	return cur.child(expiresKey).fail(detail, lineOf(expires, node))
}

// rejectUnknownKeys fails for the first key of the mapping that is not in allowed.
func rejectUnknownKeys(m *yaml.Node, allowed []string, cur waiverCursor) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := resolveAlias(m.Content[i])
		if key.Kind == yaml.ScalarNode && slices.Contains(allowed, key.Value) {
			continue
		}

		return cur.child(key.Value).fail("unknown property '"+key.Value+"'", key.Line)
	}

	return nil
}

// requiredText gives the text of a key of a mapping, or a CO-EXC-002 error when it is missing, empty, or not text.
func requiredText(m *yaml.Node, key string, cur waiverCursor) (string, error) {
	v, _ := mapValue(m, key)

	text, ok := textValue(v)
	if !ok || text == "" {
		return "", cur.child(key).fail("missing required property '"+key+"'", lineOf(v, m))
	}

	return text, nil
}

// scopeField gives the field of a scope that a scope key names.
func scopeField(scope *Scope, key string) *string {
	switch key {
	case subjectKey:
		return &scope.Subject
	case environmentKey:
		return &scope.Environment
	default:
		return &scope.Obligation
	}
}

// parseScope validates the scope object: only the keys subject, environment, and obligation, at least one of
// them, each a text without a wildcard.
func parseScope(node *yaml.Node, cur waiverCursor) (*Scope, error) {
	if node.Kind != yaml.MappingNode {
		return nil, cur.fail("scope must be an object", node.Line)
	}

	if err := rejectUnknownKeys(node, scopeKeys, cur); err != nil {
		return nil, err
	}

	if len(node.Content) == 0 {
		return nil, cur.fail("scope needs at least one of subject, environment, and obligation", node.Line)
	}

	var out Scope

	for _, key := range scopeKeys {
		value, present := mapValue(node, key)
		if !present {
			continue
		}

		text, err := scopeText(value, key, cur)
		if err != nil {
			return nil, err
		}

		*scopeField(&out, key) = text
	}

	return &out, nil
}

// scopeText gives the text of one scope value: a non-empty text without a wildcard.
func scopeText(value *yaml.Node, key string, cur waiverCursor) (string, error) {
	text, ok := textValue(value)

	switch {
	case !ok || text == "":
		return "", cur.child(key).fail("scope "+key+" must be a non-empty string", value.Line)
	case strings.ContainsAny(text, wildcardChars):
		return "", cur.child(key).fail("scope "+key+" must not be a wildcard", value.Line)
	}

	return text, nil
}

// parseApprover validates the approver object: identity and authority, both text.
func parseApprover(node *yaml.Node, cur waiverCursor) (*Approver, error) {
	if node.Kind != yaml.MappingNode {
		return nil, cur.fail("approver must be an object with identity and authority", node.Line)
	}

	if err := rejectUnknownKeys(node, approverKeys, cur); err != nil {
		return nil, err
	}

	identity, err := requiredText(node, "identity", cur)
	if err != nil {
		return nil, err
	}

	authority, err := requiredText(node, "authority", cur)
	if err != nil {
		return nil, err
	}

	return &Approver{Identity: identity, Authority: authority}, nil
}

// parseEvidence validates the compensating evidence list: objects with a workspace-relative path and a digest.
func parseEvidence(list *yaml.Node, cur waiverCursor) ([]Evidence, error) {
	if list.Kind != yaml.SequenceNode {
		return nil, cur.fail("compensatingEvidence must be an array", list.Line)
	}

	out := make([]Evidence, 0, len(list.Content))

	for i, item := range list.Content {
		evidence, err := parseEvidenceItem(resolveAlias(item), cur.child(strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}

		out = append(out, evidence)
	}

	return out, nil
}

// parseEvidenceItem validates one evidence object.
func parseEvidenceItem(item *yaml.Node, cur waiverCursor) (Evidence, error) {
	if item.Kind != yaml.MappingNode {
		return Evidence{}, cur.fail("evidence must be an object with path and sha256", item.Line)
	}

	if err := rejectUnknownKeys(item, evidenceKeys, cur); err != nil {
		return Evidence{}, err
	}

	path, err := requiredText(item, "path", cur)
	if err != nil {
		return Evidence{}, err
	}

	if !fs.ValidPath(path) {
		return Evidence{}, cur.child("path").fail("evidence path must be relative to the workspace, with '/' separators and no '..'", item.Line)
	}

	digest, err := requiredText(item, "sha256", cur)
	if err != nil {
		return Evidence{}, err
	}

	if !fingerprintPattern.MatchString(digest) {
		return Evidence{}, cur.child("sha256").fail("evidence sha256 must be a sha256: digest", item.Line)
	}

	return Evidence{Path: path, SHA256: digest}, nil
}

// ParseDay parses a YYYY-MM-DD date to 00:00 UTC of that day. It accepts the
// dates that JavaScript Date.parse accepts: month 1 to 12, day 1 to 31; a day
// that the month does not have (2026-02-31) rolls into the next month.
func ParseDay(text string) (time.Time, bool) {
	if !datePattern.MatchString(text) {
		return time.Time{}, false
	}

	parts, ok := dayNumbers(strings.SplitN(text, "-", dateParts))
	if !ok || !isJSDateMonthDay(parts[1], parts[2]) {
		return time.Time{}, false
	}

	return time.Date(parts[0], time.Month(parts[1]), parts[2], 0, 0, 0, 0, time.UTC), true
}

// dayNumbers converts the year, month, and day texts of a date to numbers.
func dayNumbers(texts []string) ([dateParts]int, bool) {
	var numbers [dateParts]int

	for i, text := range texts {
		n, err := strconv.Atoi(text)
		if err != nil {
			return numbers, false
		}

		numbers[i] = n
	}

	return numbers, true
}

// isJSDateMonthDay reports whether the month and the day are in the limits of JavaScript Date.parse.
func isJSDateMonthDay(month, day int) bool {
	return month >= 1 && month <= maxMonth && day >= 1 && day <= maxDay
}
