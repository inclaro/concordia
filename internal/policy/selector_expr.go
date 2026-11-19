package policy

import (
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// Tri is a three-valued truth value. The zero value is TriUnknown, so a
// result that nobody set never reads as "the rule does not apply".
type Tri uint8

// Tri values.
const (
	TriUnknown Tri = iota
	TriFalse
	TriTrue
)

// triNames and triNegations are indexed by Tri.
var (
	triNames     = [...]string{TriUnknown: "unknown", TriFalse: "false", TriTrue: "true"}
	triNegations = [...]Tri{TriUnknown: TriUnknown, TriFalse: TriTrue, TriTrue: TriFalse}
)

// String gives "unknown", "false" or "true".
func (t Tri) String() string {
	if int(t) < len(triNames) {
		return triNames[t]
	}

	return "unknown"
}

// MarshalText encodes the value as its String form.
func (t Tri) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// Not negates. The negation of unknown is unknown.
func (t Tri) Not() Tri {
	if int(t) < len(triNegations) {
		return triNegations[t]
	}

	return TriUnknown
}

// And is the conjunction: false if one operand is false, else unknown if one
// operand is unknown, else true. And of no operand is true.
func And(values ...Tri) Tri { return foldTri(values, TriFalse, TriTrue) }

// Or is the disjunction: true if one operand is true, else unknown if one
// operand is unknown, else false. Or of no operand is false.
func Or(values ...Tri) Tri { return foldTri(values, TriTrue, TriFalse) }

// foldTri gives absorbing if a value is absorbing, else unknown if a value is
// unknown, else neutral (also for no value).
func foldTri(values []Tri, absorbing, neutral Tri) Tri {
	result := neutral

	for _, v := range values {
		if v == absorbing {
			return absorbing
		}

		if v == TriUnknown {
			result = TriUnknown
		}
	}

	return result
}

// Dimension is a selector dimension over the evidence: a property of a fact
// or of an entity that a selector can test.
type Dimension string

// Selector dimensions. The keys `calmNodes`, `nodeTypes`, `tags` and `ref`
// are not dimensions: they resolve to CALM IDs at compile time and are tested
// as the `id` property of DimensionArchitecture.
const (
	// DimensionArchitecture is the CALM identity and kind. Properties: id, nodeType.
	DimensionArchitecture Dimension = "architecture"
	// DimensionObservedKind is the kind of the observed entity or fact.
	DimensionObservedKind Dimension = "observedKind"
	// DimensionEnvironment is the environment of the fact.
	DimensionEnvironment Dimension = "environment"
	// DimensionRepository is the repository that the fact comes from.
	DimensionRepository Dimension = "repository"
	// DimensionWorkload is a workload. Properties: cluster, namespace, kind, name.
	DimensionWorkload Dimension = "workload"
	// DimensionContainer is a container of a workload. Properties: name, image.
	DimensionContainer Dimension = "container"
	// DimensionProtocol is the protocol of a connection.
	DimensionProtocol Dimension = "protocol"
	// DimensionOperation is the operation of a connection.
	DimensionOperation Dimension = "operation"
	// DimensionInterface is the interface of a relationship.
	DimensionInterface Dimension = "interface"
	// DimensionTrustContext is the trust context. It has no data source in this release.
	DimensionTrustContext Dimension = "trustContext"
	// DimensionDataClassification is the data classification. It has no data source in this release.
	DimensionDataClassification Dimension = "dataClassification"
	// DimensionGovernanceScope is the governance scope. It has no data source in this release.
	DimensionGovernanceScope Dimension = "governanceScope"
	// DimensionEvidenceRole is the evidence role of the fact (an observation.EvidenceRole).
	DimensionEvidenceRole Dimension = "evidenceRole"
)

// PropertyValue is the property name of the dimensions that have one value.
const PropertyValue = "value"

// Properties of the structured dimensions.
const (
	PropertyID        = "id"
	PropertyNodeType  = "nodeType"
	PropertyCluster   = "cluster"
	PropertyKind      = "kind"
	PropertyName      = "name"
	PropertyNamespace = "namespace"
	PropertyImage     = "image"
)

// dimensionProperties lists the properties of each dimension. A dimension
// with the only property PropertyValue has one value.
var dimensionProperties = map[Dimension][]string{
	DimensionArchitecture:       {PropertyID, PropertyNodeType},
	DimensionObservedKind:       {PropertyValue},
	DimensionEnvironment:        {PropertyValue},
	DimensionRepository:         {PropertyValue},
	DimensionWorkload:           {PropertyCluster, PropertyKind, PropertyName, PropertyNamespace},
	DimensionContainer:          {PropertyImage, PropertyName},
	DimensionProtocol:           {PropertyValue},
	DimensionOperation:          {PropertyValue},
	DimensionInterface:          {PropertyValue},
	DimensionTrustContext:       {PropertyValue},
	DimensionDataClassification: {PropertyValue},
	DimensionGovernanceScope:    {PropertyValue},
	DimensionEvidenceRole:       {PropertyValue},
}

// Dimensions gives all selector dimensions, sorted.
func Dimensions() []Dimension {
	out := make([]Dimension, 0, len(dimensionProperties))
	for d := range dimensionProperties {
		out = append(out, d)
	}

	slices.Sort(out)

	return out
}

// Properties gives the property names of the dimension, sorted. It is nil for
// a value that is not a dimension.
func (d Dimension) Properties() []string { return slices.Clone(dimensionProperties[d]) }

// Property names one property of one dimension. It is how an unknown result
// says which property it needed.
type Property struct {
	Dimension Dimension `json:"dimension"`
	Name      string    `json:"name"`
}

// String gives `dimension` for a dimension with one value, else `dimension.name`.
func (p Property) String() string {
	if p.Name == PropertyValue {
		return string(p.Dimension)
	}

	return string(p.Dimension) + "." + p.Name
}

// EvalContext gives the property values of the fact or entity under evaluation.
//
// Lookup returns known=false when the property is missing for this fact or
// when no data source supplies it; the selector then evaluates to unknown, never
// to false. A dimension that has no data source in this release (trustContext,
// dataClassification, governanceScope) is unknown until the owning change adds
// a resolver for it.
type EvalContext interface {
	Lookup(dimension Dimension, property string) (value string, known bool)
}

// Resolver gives the property values of one dimension.
type Resolver func(property string) (value string, known bool)

// ResolverSet is an EvalContext made of one resolver for each dimension that
// has a data source. A dimension without a resolver is unknown.
type ResolverSet map[Dimension]Resolver

// Lookup implements EvalContext.
func (s ResolverSet) Lookup(dimension Dimension, property string) (string, bool) {
	resolve, ok := s[dimension]
	if !ok || resolve == nil {
		return "", false
	}

	return resolve(property)
}

// StaticValues is an EvalContext of fixed values: dimension, then property.
// Anything that is not listed is unknown.
type StaticValues map[Dimension]map[string]string

// Lookup implements EvalContext.
func (s StaticValues) Lookup(dimension Dimension, property string) (string, bool) {
	value, ok := s[dimension][property]

	return value, ok
}

// ExprOp is the operator of a SelectorExpr node.
type ExprOp string

// Operators of a SelectorExpr.
const (
	// OpMatch is a leaf: a dimension with accepted values.
	OpMatch ExprOp = "match"
	OpAllOf ExprOp = "allOf"
	OpAnyOf ExprOp = "anyOf"
	OpNot   ExprOp = "not"
)

// SelectorExpr is a selector over the dimensions with explicit Boolean
// composition. A leaf (OpMatch) tests properties of one dimension; every
// property of the leaf must match (`workload: {namespace, kind}`), and each
// property accepts any of its listed values. allOf, anyOf and not combine
// operands and nest. The expression is a value: do not change it after
// Compile returns it.
type SelectorExpr struct {
	Op ExprOp `json:"op"`
	// Dimension and Match are set on a leaf. Match maps a property name to its accepted values.
	Dimension Dimension           `json:"dimension,omitempty"`
	Match     map[string][]string `json:"match,omitempty"`
	// Operands are set on allOf, anyOf (one or more) and not (exactly one).
	Operands []*SelectorExpr `json:"operands,omitempty"`
}

// Match makes a leaf for a dimension with one value: it matches when the
// value is one of values.
func Match(dimension Dimension, values ...string) *SelectorExpr {
	return MatchProperties(dimension, map[string][]string{PropertyValue: values})
}

// MatchProperties makes a leaf that needs every listed property to match one
// of its accepted values.
func MatchProperties(dimension Dimension, accepted map[string][]string) *SelectorExpr {
	match := make(map[string][]string, len(accepted))
	for name, values := range accepted {
		match[name] = nonNil(slices.Clone(values))
	}

	return &SelectorExpr{Op: OpMatch, Dimension: dimension, Match: match}
}

// AllOf makes a conjunction.
func AllOf(operands ...*SelectorExpr) *SelectorExpr {
	return &SelectorExpr{Op: OpAllOf, Operands: operands}
}

// AnyOf makes a disjunction.
func AnyOf(operands ...*SelectorExpr) *SelectorExpr {
	return &SelectorExpr{Op: OpAnyOf, Operands: operands}
}

// Not makes a negation.
func Not(operand *SelectorExpr) *SelectorExpr {
	return &SelectorExpr{Op: OpNot, Operands: []*SelectorExpr{operand}}
}

// Evaluation is the result of evaluating a selector.
type Evaluation struct {
	// Unknown lists, sorted and without duplicates, the properties whose
	// missing value made the result unknown. It is empty unless Value is TriUnknown.
	Unknown []Property `json:"unknown,omitempty"`
	Value   Tri        `json:"value"`
}

// Eval evaluates the expression for the fact or entity that ctx describes. A
// nil ctx has no property, so every leaf is unknown. A nil expression is true.
//
//   - not of unknown is unknown.
//   - allOf is false if one operand is false, else unknown if one is unknown, else true.
//   - anyOf is true if one operand is true, else unknown if one is unknown, else false.
//
// A rule does not apply because a selector is false. It never counts as
// not-applicable because a selector is unknown.
func (e *SelectorExpr) Eval(ctx EvalContext) Evaluation {
	if e == nil {
		return Evaluation{Value: TriTrue}
	}

	value, missing := e.eval(ctx)
	if value != TriUnknown {
		return Evaluation{Value: value}
	}

	slices.SortFunc(missing, func(a, b Property) int { return canonjson.CompareUTF16(a.String(), b.String()) })

	return Evaluation{Value: TriUnknown, Unknown: slices.Compact(missing)}
}

func (e *SelectorExpr) eval(ctx EvalContext) (Tri, []Property) {
	switch e.Op {
	case OpMatch:
		return e.evalLeaf(ctx)
	case OpNot:
		if len(e.Operands) != 1 {
			return TriUnknown, nil
		}

		value, missing := e.Operands[0].eval(ctx)

		return value.Not(), missing
	case OpAllOf, OpAnyOf:
		return e.evalOperands(ctx)
	}

	return TriUnknown, nil
}

// evalOperands evaluates allOf and anyOf. The decisive value is false for
// allOf and true for anyOf: one operand with it decides the result.
func (e *SelectorExpr) evalOperands(ctx EvalContext) (Tri, []Property) {
	decisive, neutral := TriFalse, TriTrue
	if e.Op == OpAnyOf {
		decisive, neutral = TriTrue, TriFalse
	}

	result := neutral

	var missing []Property

	for _, operand := range e.Operands {
		value, props := operand.eval(ctx)

		if value == decisive {
			return decisive, nil
		}

		if value == TriUnknown {
			result = TriUnknown

			missing = append(missing, props...)
		}
	}

	return result, missing
}

func (e *SelectorExpr) evalLeaf(ctx EvalContext) (Tri, []Property) {
	names := make([]string, 0, len(e.Match))
	for name := range e.Match {
		names = append(names, name)
	}

	slices.Sort(names)

	result := TriTrue

	var missing []Property

	for _, name := range names {
		var (
			value string
			known bool
		)

		if ctx != nil {
			value, known = ctx.Lookup(e.Dimension, name)
		}

		switch {
		case !known:
			result = TriUnknown

			missing = append(missing, Property{Dimension: e.Dimension, Name: name})
		case !slices.Contains(e.Match[name], value):
			return TriFalse, nil
		}
	}

	return result, missing
}

// String shows the expression, for example `allOf(environment=production,not(protocol=https))`.
func (e *SelectorExpr) String() string {
	if e == nil {
		return ""
	}

	switch e.Op {
	case OpMatch:
		return e.leafString()
	case OpAllOf, OpAnyOf, OpNot:
		parts := make([]string, len(e.Operands))
		for i, operand := range e.Operands {
			parts[i] = operand.String()
		}

		return string(e.Op) + "(" + strings.Join(parts, ",") + ")"
	}

	return string(e.Op)
}

func (e *SelectorExpr) leafString() string {
	names := make([]string, 0, len(e.Match))
	for name := range e.Match {
		names = append(names, name)
	}

	slices.Sort(names)

	parts := make([]string, len(names))
	for i, name := range names {
		values := strings.Join(e.Match[name], "|")
		if name == PropertyValue {
			parts[i] = string(e.Dimension) + "=" + values

			continue
		}

		parts[i] = string(e.Dimension) + "." + name + "=" + values
	}

	if len(parts) == 1 {
		return parts[0]
	}

	return "(" + strings.Join(parts, ",") + ")"
}

// ---------------------------------------------------------------------------------------------
// Compile: from the raw selector to an expression

// Selector key classes.
type selectorClass int

const (
	// classLegacy: only calmNodes, nodeTypes, tags and ref (union, resolved to CALM IDs).
	classLegacy selectorClass = iota
	// classDimension: exactly one dimension key.
	classDimension
	// classOperator: exactly one of allOf, anyOf and not.
	classOperator
)

var operatorKeys = []string{string(OpAllOf), string(OpAnyOf), string(OpNot)}

// classifySelector says which kind of selector the keys make. The problem is
// a detail for CO-POL-002, or "" when there is none. A selector that mixes the
// classes, or has two dimensions or operators at one level, composes
// implicitly and is a problem.
func classifySelector(sel rawSelector) (class selectorClass, problem string) {
	keys := make([]string, 0, len(sel))
	for key := range sel {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	var legacy, composing []string

	for _, key := range keys {
		switch {
		case slices.Contains(selectorKeys, key):
			legacy = append(legacy, key)
		case slices.Contains(operatorKeys, key), isDimensionKey(key):
			composing = append(composing, key)
		default:
			return classLegacy, unsupportedSelectorKeyProblem(key)
		}
	}

	return classOfSelectorKeys(legacy, composing)
}

// unsupportedSelectorKeyProblem is the detail for a key that is none of the legacy keys, the dimensions and the operators.
func unsupportedSelectorKeyProblem(key string) string {
	if key == deployedInKey {
		return "selector key '" + deployedInKey + "' is only valid as the whole subject or the whole relation.target " +
			"of an obligation block, not in obligation.applies, an operator, a named selector, or another rule"
	}

	return "selector key '" + key + "' is not supported (use " + strings.Join(selectorKeys, ", ") +
		", a dimension: " + dimensionList() + ", or an operator: " + strings.Join(operatorKeys, ", ") + ")"
}

// classOfSelectorKeys gives the class of a selector whose supported keys are the legacy and the composing ones.
func classOfSelectorKeys(legacy, composing []string) (class selectorClass, problem string) {
	switch {
	case len(legacy) > 0 && len(composing) > 0:
		return classLegacy, "selector combines " + strings.Join(append(legacy, composing...), ", ") +
			" without an operator; wrap them in allOf, anyOf or not (no implicit composition)"
	case len(composing) > 1:
		return classLegacy, "selector has the keys " + strings.Join(composing, ", ") +
			" at one level without an operator; combine them with allOf, anyOf or not (no implicit composition)"
	case len(composing) == 0:
		return classLegacy, ""
	case slices.Contains(operatorKeys, composing[0]):
		return classOperator, ""
	}

	return classDimension, ""
}

func isDimensionKey(key string) bool {
	_, ok := dimensionProperties[Dimension(key)]

	return ok
}

func dimensionList() string {
	dims := Dimensions()
	names := make([]string, len(dims))

	for i, d := range dims {
		names[i] = string(d)
	}

	return strings.Join(names, ", ")
}

func (c *compiler) selectorProblem(detail, pointer string) *diag.Diagnostic {
	d := diag.New(diag.POL002, diag.Detail(detail), c.at(pointer))

	return &d
}

// selectorExpr compiles a raw selector into an expression. A selector of the
// keys calmNodes, nodeTypes, tags and ref becomes a leaf on the CALM ID of
// the nodes that it resolves to; `{ref: name}` of a named selector that is an
// expression becomes that expression.
func (c *compiler) selectorExpr(sel rawSelector, pointer string, stack []string) (*SelectorExpr, *diag.Diagnostic) {
	class, problem := classifySelector(sel)
	if problem != "" {
		return nil, c.selectorProblem(problem, pointer)
	}

	switch class {
	case classDimension:
		return c.dimensionLeaf(sel, pointer)
	case classOperator:
		return c.operatorExpr(sel, pointer, stack)
	case classLegacy:
	}

	if expr, d := c.namedExpressionRef(sel, pointer, stack); expr != nil || d != nil {
		return expr, d
	}

	ids, d := c.resolveSelector(sel, pointer, stack)
	if d != nil {
		return nil, d
	}

	return MatchProperties(DimensionArchitecture, map[string][]string{PropertyID: ids}), nil
}

// namedExpressionRef compiles `{ref: name}` of a named selector that is an
// expression. It gives neither an expression nor a problem for any other selector.
func (c *compiler) namedExpressionRef(sel rawSelector, pointer string, stack []string) (*SelectorExpr, *diag.Diagnostic) {
	ref, ok := sel["ref"].(string)
	if !ok || len(sel) != 1 {
		return nil, nil
	}

	target, d := c.refTarget(ref, pointer, stack)
	if d != nil {
		return nil, d
	}

	if targetClass, targetProblem := classifySelector(target); targetProblem != "" || targetClass == classLegacy {
		return nil, nil
	}

	return c.selectorExpr(target, "/selectors/"+ref, append(slices.Clone(stack), ref))
}

func (c *compiler) operatorExpr(sel rawSelector, pointer string, stack []string) (*SelectorExpr, *diag.Diagnostic) {
	key, raw, ok := presentOperatorKey(sel)
	if !ok {
		return nil, c.selectorProblem("selector has no operator", pointer)
	}

	operands, places, problem := c.operandsOf(key, raw, pointer)
	if problem != nil {
		return nil, problem
	}

	expr := &SelectorExpr{Op: ExprOp(key), Operands: make([]*SelectorExpr, 0, len(operands))}

	for i, operand := range operands {
		child, d := c.selectorExpr(operand, places[i], stack)
		if d != nil {
			return nil, d
		}

		expr.Operands = append(expr.Operands, child)
	}

	return expr, nil
}

// presentOperatorKey gives the first operator key that the selector has, in the order of operatorKeys, and its value.
func presentOperatorKey(sel rawSelector) (key string, raw any, ok bool) {
	for _, candidate := range operatorKeys {
		if value, present := sel[candidate]; present {
			return candidate, value, true
		}
	}

	return "", nil, false
}

// operandsOf reads the operands of an operator key and the pointer of each: `not` takes
// one selector; `allOf` and `anyOf` take a non-empty list.
func (c *compiler) operandsOf(key string, raw any, pointer string) (operands []rawSelector, places []string, problem *diag.Diagnostic) {
	if ExprOp(key) == OpNot {
		return c.notOperand(raw, pointer)
	}

	return c.listOperands(key, raw, pointer)
}

func (c *compiler) notOperand(raw any, pointer string) (operands []rawSelector, places []string, problem *diag.Diagnostic) {
	operand, isMap := raw.(map[string]any)
	if !isMap {
		return nil, nil, c.selectorProblem("'not' takes one selector", pointer+"/not")
	}

	return []rawSelector{operand}, []string{pointer + "/not"}, nil
}

func (c *compiler) listOperands(key string, raw any, pointer string) (operands []rawSelector, places []string, problem *diag.Diagnostic) {
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil, nil, c.selectorProblem("'"+key+"' takes a list of one or more selectors", pointer+"/"+key)
	}

	for i, item := range list {
		place := pointer + "/" + key + "/" + strconv.Itoa(i)

		operand, isMap := item.(map[string]any)
		if !isMap {
			return nil, nil, c.selectorProblem("'"+key+"' item is not a selector", place)
		}

		operands = append(operands, operand)
		places = append(places, place)
	}

	return operands, places, nil
}

// dimensionLeaf compiles `{dimension: value}`. A dimension with one value takes
// a string or a list of strings; a structured dimension takes a map of its
// properties, each a string or a list of strings.
func (c *compiler) dimensionLeaf(sel rawSelector, pointer string) (*SelectorExpr, *diag.Diagnostic) {
	for key, raw := range sel {
		dimension := Dimension(key)
		place := pointer + "/" + key
		properties := dimensionProperties[dimension]

		if len(properties) == 1 && properties[0] == PropertyValue {
			values, ok := acceptedValues(raw)
			if !ok {
				return nil, c.selectorProblem("'"+key+"' takes a string or a list of strings", place)
			}

			if d := c.checkDimensionValues(dimension, values, place); d != nil {
				return nil, d
			}

			return Match(dimension, values...), nil
		}

		return c.structuredLeaf(dimension, raw, place)
	}

	return nil, c.selectorProblem("selector has no dimension", pointer)
}

// structuredLeaf compiles the map of properties of a structured dimension.
func (c *compiler) structuredLeaf(dimension Dimension, raw any, place string) (*SelectorExpr, *diag.Diagnostic) {
	properties := dimensionProperties[dimension]

	fields, isMap := raw.(map[string]any)
	if !isMap || len(fields) == 0 {
		return nil, c.selectorProblem("'"+string(dimension)+"' takes a map of "+strings.Join(properties, ", "), place)
	}

	accepted := make(map[string][]string, len(fields))

	for name, value := range fields {
		if !slices.Contains(properties, name) {
			return nil, c.selectorProblem("'"+string(dimension)+"' has no property '"+name+"' (use "+strings.Join(properties, ", ")+")", place+"/"+name)
		}

		values, ok := acceptedValues(value)
		if !ok {
			return nil, c.selectorProblem("'"+string(dimension)+"."+name+"' takes a string or a list of strings", place+"/"+name)
		}

		accepted[name] = values
	}

	return MatchProperties(dimension, accepted), nil
}

func (c *compiler) checkDimensionValues(dimension Dimension, values []string, pointer string) *diag.Diagnostic {
	if dimension != DimensionEvidenceRole {
		return nil
	}

	for _, v := range values {
		if !observation.IsEvidenceRole(v) {
			return c.selectorProblem("'evidenceRole' value '"+v+"' is not an evidence role", pointer)
		}
	}

	return nil
}

// acceptedValues reads a non-empty string or a non-empty list of non-empty strings.
func acceptedValues(raw any) (values []string, ok bool) {
	switch v := raw.(type) {
	case string:
		return []string{v}, v != ""
	case []any:
		out := make([]string, 0, len(v))

		for _, item := range v {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, false
			}

			out = append(out, s)
		}

		return out, len(out) > 0
	}

	return nil, false
}
