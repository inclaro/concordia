package projection

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
)

type rel struct {
	id            string
	from          string
	to            string
	description   string
	protocol      string
	status        relStatus
	findings      []string
	observedKinds []string
}

// dslEnds are the two elements that a relationship joins.
type dslEnds struct{ from, to string }

// dslClassStatus maps a reconciliation classification to the status of a relationship in the DSL. A
// classification that is not here is declared-only.
var dslClassStatus = map[conformance.Classification]relStatus{
	conformance.ClassConforming:    statusConforming,
	conformance.ClassContradiction: statusViolating,
	conformance.ClassUnknown:       statusUnknown,
}

func findingFingerprints(run *RunView, from, to string) ([]string, bool) {
	var (
		fps    []string
		active bool
	)

	for i := range run.Findings {
		f := &run.Findings[i]
		if f.Source.ArchitectureID != from || f.Target.ArchitectureID != to {
			continue
		}

		fps = append(fps, f.Fingerprint)
		active = active || dslDispositionActive(f.Disposition)
	}

	slices.SortFunc(fps, canonjson.CompareUTF16)

	return fps, active
}

// dslDispositionActive is true for a disposition that makes a relationship violating.
func dslDispositionActive(disposition exceptions.Disposition) bool {
	return disposition == exceptions.DispositionActive || disposition == exceptions.DispositionExpiredWaiver
}

// reconciledStatus gives the status of the reconciliation entry of a relationship; no entry is declared-only.
func reconciledStatus(run *RunView, from, target string) relStatus {
	idx := slices.IndexFunc(run.Reconciliation, func(r conformance.ReconciliationEntry) bool { return r.From == from && r.To == target })
	if idx < 0 {
		return statusDeclaredOnly
	}

	if status, ok := dslClassStatus[run.Reconciliation[idx].Classification]; ok {
		return status
	}

	return statusDeclaredOnly
}

// relationshipStatus gives the status of a relationship. In committed mode it reads the reconciliation
// classification only: no finding, so no disposition, waiver, or expiry (design D6).
func relationshipStatus(from, target string, run *RunView, committed bool) (status relStatus, fingerprints []string) {
	if run == nil {
		return statusDeclaredOnly, nil
	}

	var fps []string

	if !committed {
		var active bool

		fps, active = findingFingerprints(run, from, target)
		if active {
			return statusViolating, fps
		}
	}

	return reconciledStatus(run, from, target), fps
}

func (m *dslModel) isAncestor(a, b string) bool {
	child := m.byID[b]
	if child == nil {
		return false
	}

	for p := child.Parent; p != ""; {
		if p == a {
			return true
		}

		next := m.byID[p]
		if next == nil {
			return false
		}

		p = next.Parent
	}

	return false
}

// hasBoth is true when both IDs are elements of the DSL.
func (m *dslModel) hasBoth(a, b string) bool {
	return m.byID[a] != nil && m.byID[b] != nil
}

// isNested is true when one of the elements contains the other.
func (m *dslModel) isNested(a, b string) bool {
	return m.isAncestor(a, b) || m.isAncestor(b, a)
}

// declaredEnds gives the pairs of elements that a relationship declaration joins.
func declaredEnds(decl *intent.RelationshipDeclaration) []dslEnds {
	if decl.Source == "" {
		return nil
	}

	var pairs []dslEnds

	if decl.Kind == intent.RelConnects && len(decl.Targets) > 0 {
		pairs = append(pairs, dslEnds{decl.Source, decl.Targets[0]})
	}

	if decl.Kind == intent.RelInteracts {
		for _, t := range decl.Targets {
			pairs = append(pairs, dslEnds{decl.Source, t})
		}
	}

	return pairs
}

func (m *dslModel) declaredRelationships(model *intent.ArchitectureModel) []rel {
	sorted := slices.Clone(model.Relationships)
	slices.SortFunc(sorted, func(a, b intent.RelationshipDeclaration) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	var rels []rel

	for i := range sorted {
		decl := &sorted[i]

		for _, pair := range declaredEnds(decl) {
			if !m.hasBoth(pair.from, pair.to) {
				continue
			}

			status, fps := relationshipStatus(pair.from, pair.to, m.run, m.committed)
			rels = append(rels, rel{
				id: decl.ID, from: pair.from, to: pair.to, description: decl.Description, protocol: decl.Protocol,
				status: status, findings: fps,
			})
		}
	}

	return rels
}

// isObservedOnly is true for an undeclared reconciliation entry between two elements that do not contain each other.
func (m *dslModel) isObservedOnly(rec *conformance.ReconciliationEntry) bool {
	if rec.Classification != conformance.ClassUndeclared || rec.From == rec.To {
		return false
	}

	return m.hasBoth(rec.From, rec.To) && !m.isNested(rec.From, rec.To)
}

// observedRel makes the relationship of an observed-only reconciliation entry.
func (m *dslModel) observedRel(rec *conformance.ReconciliationEntry) rel {
	var fps []string
	if !m.committed {
		fps, _ = findingFingerprints(m.run, rec.From, rec.To)
	}

	kinds := make([]string, len(rec.ObservedKinds))

	for j, k := range rec.ObservedKinds {
		kinds[j] = string(k)
	}

	return rel{
		from: rec.From, to: rec.To, description: "observed only", status: statusObservedOnly,
		findings: fps, observedKinds: kinds,
	}
}

func (m *dslModel) observedOnly() []rel {
	if m.run == nil {
		return nil
	}

	var out []rel

	for i := range m.run.Reconciliation {
		if rec := &m.run.Reconciliation[i]; m.isObservedOnly(rec) {
			out = append(out, m.observedRel(rec))
		}
	}

	slices.SortStableFunc(out, func(a, b rel) int {
		return canonjson.CompareUTF16(a.from+"\x00"+a.to, b.from+"\x00"+b.to)
	})

	return out
}

// relationshipProps gives the properties of a relationship in the DSL.
func (m *dslModel) relationshipProps(relation *rel) []property {
	var entries []property

	if relation.id != "" {
		entries = append(entries, property{propUniqueID, relation.id})
	}

	if !m.committed {
		entries = append(entries, property{"concordia.findings", strings.Join(relation.findings, ",")})
	}

	if len(relation.observedKinds) > 0 {
		entries = append(entries, property{"concordia.observed-kinds", strings.Join(relation.observedKinds, ",")})
	}

	return entries
}

func (m *dslModel) writeRelationship(out *dslWriter, relation *rel) {
	status := string(relation.status)
	out.line(2, m.ident[relation.from]+" -> "+m.ident[relation.to]+" "+DSLString("["+status+"] "+relation.description)+" "+
		DSLString(relation.protocol)+" "+DSLString(statusTag(relation.status))+" {")
	out.props(bodyDepth, m.relationshipProps(relation)...)
	out.line(2, "}")
}
