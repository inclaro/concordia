// Package statement reads externally produced evidence: in-toto Statement v1 files, bare or inside a DSSE envelope or
// a Sigstore bundle (design D5). It decodes the typed predicates (SLSA provenance and the Concordia evidence types),
// verifies the signature of a declared producer through internal/cosign, and prepares the redacted form that is
// persisted. It decides nothing about admissibility: internal/assurance/admit does, from the facts that this package
// reads.
package statement

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/workspace"
)

// StatementType is the only in-toto statement type that Concordia reads.
const StatementType = "https://in-toto.io/Statement/v1"

// PayloadType is the DSSE payload type of an in-toto statement.
const PayloadType = "application/vnd.in-toto+json"

// bundleMediaPrefix starts the media type of a Sigstore bundle.
const bundleMediaPrefix = "application/vnd.dev.sigstore.bundle"

// Predicate types.
const (
	PredicateSLSAProvenance      = "https://slsa.dev/provenance/v1"
	PredicateDeploymentRecord    = "https://concordia.io/evidence/deployment-record/v1"
	PredicateWorkloadObservation = "https://concordia.io/evidence/workload-observation/v1"
	PredicateCheckResult         = "https://concordia.io/evidence/check-result/v1"
	PredicateMeasurement         = "https://concordia.io/evidence/measurement/v1"
	PredicatePlan                = "https://concordia.io/evidence/plan/v1"
)

// claimKinds maps a predicate type to the producer claim kind that a producer needs to attest it.
var claimKinds = map[string]string{
	PredicateSLSAProvenance:      workspace.ProducerClaimBuildProvenance,
	PredicateDeploymentRecord:    workspace.ProducerClaimDeploymentRecord,
	PredicateWorkloadObservation: workspace.ProducerClaimWorkloadObservation,
	PredicateCheckResult:         workspace.ProducerClaimCheckResult,
	PredicateMeasurement:         workspace.ProducerClaimMeasurement,
	PredicatePlan:                workspace.ProducerClaimPlan,
}

// ClaimKind gives the producer claim kind of a predicate type, or "" for a type that Concordia does not read.
func ClaimKind(predicateType string) string { return claimKinds[predicateType] }

// Subject is one artifact or revision that a statement is about. The digest is keyed by algorithm: `sha256` for an
// artifact or a file, `gitCommit` for a revision.
type Subject struct {
	Digest map[string]string `json:"digest"`
	Name   string            `json:"name,omitempty"`
}

// Statement is an in-toto Statement v1. The predicate stays raw: the typed readers decode it.
type Statement struct {
	Type          string          `json:"_type"` //nolint:tagliatelle // in-toto names the key `_type`.
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate,omitempty"`
}

// Form is how a statement file is packaged.
type Form string

// Statement file forms.
const (
	// FormStatement is a bare in-toto statement. It carries no signature.
	FormStatement Form = "statement"
	// FormEnvelope is a DSSE envelope. cosign cannot verify it without a Sigstore bundle, so it counts as unsigned.
	FormEnvelope Form = "dsse-envelope"
	// FormBundle is a Sigstore bundle that wraps a DSSE envelope and holds the verification material.
	FormBundle Form = "sigstore-bundle"
)

// Loaded is one statement file that was read.
type Loaded struct {
	// Path is the workspace-relative path with "/" separators.
	Path string
	// Digest is the digest of the file bytes, `sha256:<hex>`.
	Digest    string
	Form      Form
	Statement Statement
}

// MalformedError is a file that is not a usable statement.
type MalformedError struct {
	Path   string
	Reason string
}

// Error gives the path and the reason.
func (m *MalformedError) Error() string { return m.Path + ": " + m.Reason }

// Diagnostic gives the CO-PRV-009 warning of the file.
func (m *MalformedError) Diagnostic() diag.Diagnostic {
	return diag.New(diag.PRV009, diag.Detail(m.Error()), diag.WithSource(diag.Source{URI: m.Path}))
}

// envelope is a DSSE envelope.
type envelope struct {
	PayloadType string `json:"payloadType"`
	Payload     string `json:"payload"`
	Signatures  []struct {
		Sig string `json:"sig"`
	} `json:"signatures"`
}

// container holds the keys that tell the forms apart.
type container struct {
	Type                 string          `json:"_type"` //nolint:tagliatelle // in-toto names the key `_type`.
	MediaType            string          `json:"mediaType"`
	DSSEEnvelope         *envelope       `json:"dsseEnvelope"`
	VerificationMaterial json.RawMessage `json:"verificationMaterial"`
	PayloadType          string          `json:"payloadType"`
	Payload              string          `json:"payload"`
	Signatures           json.RawMessage `json:"signatures"`
}

// Parse reads one statement file. path is the workspace-relative path; it is only used in messages and in Loaded.
// A file that is not a valid statement gives a *MalformedError error.
func Parse(path string, data []byte) (Loaded, error) {
	loaded := Loaded{Path: path, Digest: digest.OfBytes(data)}

	fail := func(format string, args ...any) (Loaded, error) {
		return Loaded{}, &MalformedError{Path: path, Reason: fmt.Sprintf(format, args...)}
	}

	var top container
	if err := json.Unmarshal(data, &top); err != nil {
		return fail("not valid JSON: %v", jsonProblem(err))
	}

	payload := data

	switch {
	case top.DSSEEnvelope != nil || strings.HasPrefix(top.MediaType, bundleMediaPrefix):
		if top.DSSEEnvelope == nil {
			return fail("the Sigstore bundle holds no DSSE envelope, so it is not an attestation")
		}

		loaded.Form = FormBundle

		var err error
		if payload, err = envelopePayload(top.DSSEEnvelope); err != nil {
			return fail("%v", err)
		}
	case top.PayloadType != "" || top.Payload != "":
		loaded.Form = FormEnvelope

		var err error
		if payload, err = envelopePayload(&envelope{PayloadType: top.PayloadType, Payload: top.Payload}); err != nil {
			return fail("%v", err)
		}
	case top.Type != "":
		loaded.Form = FormStatement
	default:
		return fail("not an in-toto statement, a DSSE envelope, or a Sigstore bundle")
	}

	if err := json.Unmarshal(payload, &loaded.Statement); err != nil {
		return fail("the statement is not valid: %v", jsonProblem(err))
	}

	if err := loaded.Statement.validate(); err != nil {
		return fail("%v", err)
	}

	return loaded, nil
}

// envelopePayload checks the payload type of an envelope and decodes its payload.
func envelopePayload(env *envelope) ([]byte, error) {
	if env.PayloadType != PayloadType {
		return nil, fmt.Errorf("the DSSE payload type is %q, want %q", env.PayloadType, PayloadType)
	}

	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if payload, err := enc.DecodeString(env.Payload); err == nil {
			return payload, nil
		}
	}

	return nil, errors.New("the DSSE payload is not base64")
}

// validate checks the parts of a statement that every predicate needs.
func (s *Statement) validate() error {
	switch {
	case s.Type != StatementType:
		return fmt.Errorf("the statement type is %q, want %q", s.Type, StatementType)
	case s.PredicateType == "":
		return errors.New("the statement has no predicateType")
	case len(s.Subject) == 0:
		return errors.New("the statement has no subject")
	}

	for i, subject := range s.Subject {
		if len(subject.Digest) == 0 {
			return fmt.Errorf("subject %d has no digest", i)
		}

		for alg, value := range subject.Digest {
			if value == "" {
				return fmt.Errorf("subject %d has an empty %s digest", i, alg)
			}
		}
	}

	return nil
}

// jsonProblem shortens a decoding error to its useful part.
func jsonProblem(err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("syntax error at byte %d", syntax.Offset)
	}

	return err
}

// LoadAll reads each path with read and parses it. A file that cannot be read or is not a valid statement gives one
// CO-PRV-009 warning and does not stop the others. The statements come sorted by path.
func LoadAll(paths []string, read func(path string) ([]byte, error)) ([]Loaded, []diag.Diagnostic) {
	sorted := slices.Clone(paths)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	sorted = slices.Compact(sorted)

	var (
		loaded []Loaded
		diags  []diag.Diagnostic
	)

	for _, path := range sorted {
		data, err := read(path)
		if err != nil {
			bad := &MalformedError{Path: path, Reason: "cannot be read: " + err.Error()}
			diags = append(diags, bad.Diagnostic())

			continue
		}

		one, err := Parse(path, data)

		var bad *MalformedError
		if errors.As(err, &bad) {
			diags = append(diags, bad.Diagnostic())

			continue
		}

		loaded = append(loaded, one)
	}

	return loaded, diags
}

// Digest is a subject digest to compare: an algorithm and a value.
type Digest struct {
	Algorithm string
	Value     string
}

// ParseDigest reads `sha256:<hex>` (or any `<algorithm>:<value>`).
func ParseDigest(text string) (Digest, bool) {
	alg, value, ok := strings.Cut(text, ":")
	if !ok || alg == "" || value == "" {
		return Digest{}, false
	}

	return Digest{Algorithm: alg, Value: value}, true
}

// String gives `<algorithm>:<value>`.
func (d Digest) String() string { return d.Algorithm + ":" + d.Value }

// Names says whether one subject of the statement has the digest. Hex digests compare without case.
func (s *Statement) Names(d Digest) bool {
	for _, subject := range s.Subject {
		if value, ok := subject.Digest[d.Algorithm]; ok && strings.EqualFold(value, d.Value) {
			return true
		}
	}

	return false
}

// Contradicts says whether the statement names no subject with the digest that its predicate states: the predicate
// and the signed subject disagree, so the statement is an integrity failure. A missing or malformed stated digest is
// the reader's missing-field case and does not count here.
func (s *Statement) Contradicts(stated string) bool {
	want, ok := ParseDigest(stated)

	return ok && ValidDigest(stated) && !s.Names(want)
}

// Digests gives every subject digest as `<algorithm>:<value>`, sorted.
func (s *Statement) Digests() []string {
	var out []string

	for _, subject := range s.Subject {
		for alg, value := range subject.Digest {
			out = append(out, alg+":"+value)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}

// Fields is the predicate as a generic object, next to its typed form, so that a missing field and an empty field
// stay apart.
type Fields map[string]any

// Has says whether the dotted path holds a value. null and an empty string count as missing; an empty list or an
// empty object count as present, because "covers nothing" is a statement.
func (f Fields) Has(path string) bool {
	var current any = map[string]any(f)

	for _, key := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}

		if current, ok = object[key]; !ok {
			return false
		}
	}

	switch value := current.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	}

	return true
}

// Missing lists the required dotted paths that the predicate does not hold, in the order of the list.
func (f Fields) Missing(required []string) []string {
	var out []string

	for _, path := range required {
		if !f.Has(path) {
			out = append(out, path)
		}
	}

	return out
}

// DecodePredicate decodes the predicate of a statement of the wanted type into out and gives the generic form. A
// predicate of another type, a predicate that is not an object, and a field of the wrong JSON type are errors: the
// file is malformed. A field that is absent is not an error here; the caller lists it with Fields.Missing.
func DecodePredicate(st *Statement, want string, out any) (Fields, error) {
	if st.PredicateType != want {
		return nil, fmt.Errorf("the predicate type is %q, want %q", st.PredicateType, want)
	}

	var fields Fields
	if err := json.Unmarshal(st.Predicate, &fields); err != nil || fields == nil {
		return nil, errors.New("the predicate is not a JSON object")
	}

	if err := json.Unmarshal(st.Predicate, out); err != nil {
		return nil, fmt.Errorf("the predicate does not match %s: %w", want, jsonProblem(err))
	}

	return fields, nil
}
