package assessment

import (
	"encoding/json"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
)

// ParseApproval parses and validates one approval record: the closed schema (every bound field is required) and
// the checks that a schema cannot make (expiry after the decision, unique dependency items, delta shape). The
// record digest and canonical JSON are set. Whether the record refers to a known assessment version is checked
// by Load, and who made it by the authority verification.
func ParseApproval(file string, text []byte) (*Approval, error) {
	src, err := read(file, text)
	if err != nil {
		return nil, err
	}

	return buildApproval(src)
}

func buildApproval(src *source) (*Approval, error) {
	if err := src.validate(KindApproval); err != nil {
		return nil, err
	}

	var rec Approval
	if err := src.decode(&rec); err != nil {
		return nil, err
	}

	if err := checkApproval(src, &rec); err != nil {
		return nil, err
	}

	rec.Location = src.location()

	var err error

	if rec.Digest, rec.canonical, err = sealRecord(src); err != nil {
		return nil, err
	}

	return &rec, nil
}

// checkApproval makes the checks that go beyond the schema.
func checkApproval(src *source, rec *Approval) error {
	if err := checkExpiry(src, rec); err != nil {
		return err
	}

	if err := uniqueApprovalLists(src, rec); err != nil {
		return err
	}

	if rec.Previous == nil {
		return nil
	}

	return checkPrevious(src, rec)
}

// checkExpiry fails when the expiry time is not after the decision.
func checkExpiry(src *source, rec *Approval) error {
	if rec.Expiry == nil || rec.Expiry.ExpiresAt.IsZero() || rec.Expiry.ExpiresAt.After(rec.DecidedAt) {
		return nil
	}

	return invalid(src.file, fmt.Sprintf("expiry.expiresAt %s is not after decidedAt %s",
		rec.Expiry.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), rec.DecidedAt.Format("2006-01-02T15:04:05Z07:00")),
		"/expiry/expiresAt", src.at("/expiry/expiresAt"))
}

// uniqueApprovalLists fails when the dependency items, the assumptions, or the evidence of the approval repeat.
func uniqueApprovalLists(src *source, rec *Approval) error {
	if err := uniqueItems(src, rec.Subject.Dependencies, "/subject/dependencies"); err != nil {
		return err
	}

	if err := uniqueRefs(src, rec.Assumptions, "/assumptions"); err != nil {
		return err
	}

	return uniqueRefs(src, rec.Evidence, "/evidence")
}

// checkPrevious makes the checks of an incremental approval.
func checkPrevious(src *source, rec *Approval) error {
	if rec.Previous.Approval == rec.ID {
		return invalid(src.file, "previous.approval names the record itself", "/previous/approval", src.at("/previous/approval"))
	}

	if rec.Previous.SubjectDigest == rec.Subject.Digest && !deltaEmpty(rec.Delta) {
		return invalid(src.file, "the previous and the new subject digest are equal but the delta is not empty",
			"/delta", src.at("/delta"))
	}

	if err := uniqueItems(src, rec.Delta.Added, "/delta/added"); err != nil {
		return err
	}

	return uniqueItems(src, rec.Delta.Removed, "/delta/removed")
}

func deltaEmpty(d *Delta) bool {
	return d == nil || len(d.Added)+len(d.Removed)+len(d.Modified) == 0
}

// uniqueItems fails when two items have the same kind and identity.
func uniqueItems(src *source, items []Item, pointer string) error {
	type key struct {
		kind     DependencyKind
		identity string
	}

	seen := map[key]bool{}

	for i, it := range items {
		k := key{it.Kind, it.Identity}
		if seen[k] {
			at := pointer + "/" + strconv.Itoa(i)

			return invalid(src.file, fmt.Sprintf("item %s %q is listed twice", it.Kind, it.Identity), at, src.at(at))
		}

		seen[k] = true
	}

	return nil
}

// uniqueRefs fails when two refs have the same id.
func uniqueRefs(src *source, refs []Ref, pointer string) error {
	seen := map[string]bool{}

	for i, r := range refs {
		if seen[r.ID] {
			at := pointer + "/" + strconv.Itoa(i) + "/id"

			return invalid(src.file, fmt.Sprintf("%q is bound twice", r.ID), at, src.at(at))
		}

		seen[r.ID] = true
	}

	return nil
}

// ParseRevocation parses and validates one revocation record. The record digest and canonical JSON are set.
func ParseRevocation(file string, text []byte) (*Revocation, error) {
	src, err := read(file, text)
	if err != nil {
		return nil, err
	}

	return buildRevocation(src)
}

func buildRevocation(src *source) (*Revocation, error) {
	if err := src.validate(KindRevocation); err != nil {
		return nil, err
	}

	var rev Revocation
	if err := src.decode(&rev); err != nil {
		return nil, err
	}

	rev.Location = src.location()

	var err error

	if rev.Digest, rev.canonical, err = sealRecord(src); err != nil {
		return nil, err
	}

	return &rev, nil
}

// sealRecord gives the digest and the canonical JSON of the parsed document of a record.
func sealRecord(src *source) (digest string, canonical []byte, err error) {
	digest, err = RecordDigest(src.doc)
	if err != nil {
		return "", nil, invalid(src.file, err.Error(), "", src.node)
	}

	canonical, err = canonicalOf(src.doc)
	if err != nil {
		return "", nil, invalid(src.file, err.Error(), "", src.node)
	}

	return digest, canonical, nil
}

// Doc gives the parsed-document form of the record (the mapping that ParseApproval digests), with empty lists
// instead of null.
func (a *Approval) Doc() (map[string]any, error) {
	clone := *a
	if clone.Assumptions == nil {
		clone.Assumptions = []Ref{}
	}

	if clone.Evidence == nil {
		clone.Evidence = []Ref{}
	}

	if clone.ProducedUnder.EvaluatorVersions == nil {
		clone.ProducedUnder.EvaluatorVersions = map[string]string{}
	}

	if clone.Subject.Dependencies == nil {
		clone.Subject.Dependencies = []Item{}
	}

	if clone.Delta != nil {
		clone.Delta = deltaWithLists(clone.Delta)
	}

	return docOf(KindApproval, &clone)
}

// deltaWithLists copies the delta with empty lists instead of null.
func deltaWithLists(d *Delta) *Delta {
	delta := *d
	if delta.Added == nil {
		delta.Added = []Item{}
	}

	if delta.Removed == nil {
		delta.Removed = []Item{}
	}

	if delta.Modified == nil {
		delta.Modified = []ModifiedItem{}
	}

	return &delta
}

// Doc gives the parsed-document form of the record.
func (r *Revocation) Doc() (map[string]any, error) { return docOf(KindRevocation, r) }

// docOf gives the mapping of a record struct with apiVersion and kind.
func docOf(kind string, v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("assessment: encode %s: %w", kind, err)
	}

	doc := map[string]any{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("assessment: encode %s: %w", kind, err)
	}

	doc["apiVersion"] = APIVersion
	doc["kind"] = kind

	return doc, nil
}

// RenderApproval gives the YAML text of a new approval record. The record is validated as ParseApproval would:
// a record that omits a bound field is an error (CO-ASM-001) and nothing is rendered.
func RenderApproval(rec *Approval) ([]byte, error) {
	return renderChecked(rec.Doc, func(text []byte) error {
		_, err := ParseApproval(rec.File, text)

		return err
	})
}

// RenderRevocation gives the YAML text of a new revocation record, validated as ParseRevocation would.
func RenderRevocation(rev *Revocation) ([]byte, error) {
	return renderChecked(rev.Doc, func(text []byte) error {
		_, err := ParseRevocation(rev.File, text)

		return err
	})
}

// renderChecked renders the YAML text of the document of a record and validates it with parse.
func renderChecked(doc func() (map[string]any, error), parse func(text []byte) error) ([]byte, error) {
	mapping, err := doc()
	if err != nil {
		return nil, err
	}

	text, err := renderYAML(mapping)
	if err != nil {
		return nil, err
	}

	if err := parse(text); err != nil {
		return nil, err
	}

	return text, nil
}

func renderYAML(doc map[string]any) ([]byte, error) {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, diag.Fail(diag.ASM001, diag.Detail("cannot render: "+err.Error()))
	}

	return data, nil
}
