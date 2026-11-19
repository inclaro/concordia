package policy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// Schema validation and the decoding of a policy file.

func policyDiag(uri, pointer, detail string) diag.Diagnostic {
	shown := pointer
	if shown == "" {
		shown = "/"
	}

	return diag.New(diag.POL001, diag.Detail(shown+" "+detail), diag.WithSource(diag.Source{URI: uri, Pointer: pointer}))
}

// structureProblems checks what the schema cannot say in a way that gives one
// clear message: exactly one predicate, `subject` with all families but
// layerOrder, and `allow` only with forbid.
func structureProblems(raw any, uri string) []diag.Diagnostic {
	doc, _ := raw.(map[string]any)
	rules, _ := doc["rules"].([]any)

	var out []diag.Diagnostic

	for i, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}

		pointer := "/rules/" + strconv.Itoa(i)
		out = append(out, ruleStructureProblems(rule, uri, pointer)...)
	}

	return out
}

func ruleStructureProblems(rule map[string]any, uri, pointer string) []diag.Diagnostic {
	out := predicateCountProblems(rule, uri, pointer)
	out = append(out, subjectProblems(rule, uri, pointer)...)
	out = append(out, allowProblems(rule, uri, pointer)...)

	return append(out, typedEvidenceProblems(rule, uri, pointer)...)
}

// predicateCountProblems checks that the rule has exactly one predicate.
func predicateCountProblems(rule map[string]any, uri, pointer string) []diag.Diagnostic {
	var found []string

	for _, key := range predicateKeys {
		if _, ok := rule[key]; ok {
			found = append(found, key)
		}
	}

	list := strings.Join(predicateKeys, ", ")

	switch len(found) {
	case 1:
		return nil
	case 0:
		return []diag.Diagnostic{policyDiag(uri, pointer, "rule must have exactly one predicate ("+list+"); found none")}
	default:
		return []diag.Diagnostic{policyDiag(uri, pointer,
			fmt.Sprintf("rule must have exactly one predicate (%s); found %d: %s", list, len(found), strings.Join(found, ", ")))}
	}
}

// allowProblems checks that `allow` comes with forbid.
func allowProblems(rule map[string]any, uri, pointer string) []diag.Diagnostic {
	_, hasAllow := rule["allow"]
	_, hasForbid := rule["forbid"]

	if hasAllow && !hasForbid {
		return []diag.Diagnostic{policyDiag(uri, pointer, "'allow' is only permitted with forbid")}
	}

	return nil
}

// typedEvidenceProblems checks that the evidence keys of a typed rule are in a rule with an obligation block.
func typedEvidenceProblems(rule map[string]any, uri, pointer string) []diag.Diagnostic {
	if _, hasObligation := rule["obligation"]; hasObligation {
		return nil
	}

	ev, ok := rule["evidence"].(map[string]any)
	if !ok {
		return nil
	}

	var out []diag.Diagnostic

	for _, key := range typedEvidenceKeys {
		if _, set := ev[key]; set {
			out = append(out, policyDiag(uri, pointer+"/evidence/"+key, "is only permitted in a rule with an obligation block"))
		}
	}

	return out
}

// subjectProblems checks the `subject` of a rule: required by all predicates except layerOrder, viewCurrency,
// decisions, assessmentCurrency, require.effectiveAccess, require.checkResult and require.claim, which do not permit it
// (the first two of the last three name their own selectors; a claim names its own subjects).
func subjectProblems(rule map[string]any, uri, pointer string) []diag.Diagnostic {
	_, hasSubject := rule["subject"]

	for _, key := range []string{
		"viewCurrency", "decisions", "assessmentCurrency", "layerOrder",
		"require.effectiveAccess", "require.checkResult", "require.claim",
	} {
		if !hasPredicateKey(rule, key) {
			continue
		}

		if !hasSubject {
			return nil
		}

		return []diag.Diagnostic{policyDiag(uri, pointer, "'subject' is not permitted with "+key)}
	}

	if !hasSubject {
		return []diag.Diagnostic{policyDiag(uri, pointer, "must have required property 'subject'")}
	}

	return nil
}

// hasPredicateKey says whether the rule has the key; `require.<name>` looks inside the `require` block.
func hasPredicateKey(rule map[string]any, key string) bool {
	if name, nested := strings.CutPrefix(key, "require."); nested {
		block, _ := rule["require"].(map[string]any)
		_, ok := block[name]

		return ok
	}

	_, ok := rule[key]

	return ok
}

func schemaDiagnostics(raw any, uri string) []diag.Diagnostic {
	var out []diag.Diagnostic

	seen := map[string]struct{}{}
	push := func(d diag.Diagnostic) {
		key := d.Source.Pointer + "\x00" + d.Message
		if _, dup := seen[key]; dup {
			return
		}

		seen[key] = struct{}{}

		out = append(out, d)
	}

	for _, e := range packSchema().Validate(raw) {
		push(policyDiag(uri, e.InstancePath, e.Message))
	}

	for _, d := range structureProblems(raw, uri) {
		push(d)
	}

	return out
}

func syntaxDiagnostics(file PolicyFile, problems []yamlparse.Problem) []diag.Diagnostic {
	out := make([]diag.Diagnostic, len(problems))

	for i, p := range problems {
		out[i] = diag.New(diag.POL001, diag.Detail("syntax error: "+p.Message),
			diag.WithSource(diag.Source{URI: file.Path, Line: p.Line}))
	}

	return out
}

func decodePack(raw any) (rawPack, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return rawPack{}, fmt.Errorf("encode policy: %w", err)
	}

	var pack rawPack
	if err := json.Unmarshal(data, &pack); err != nil {
		return rawPack{}, fmt.Errorf("decode policy: %w", err)
	}

	return pack, nil
}
