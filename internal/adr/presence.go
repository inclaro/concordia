package adr

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// GitFacts are the Git facts that presence reads. Every path carries the project prefix (`@<project>/`) in a
// multi-project run; the primary project has no prefix. The facts are plain data: reading Git is the caller's job.
type GitFacts struct {
	// Revisions maps a project name ("" for the primary project) to its evaluated revision; "" or a missing
	// key means the project has no revision.
	Revisions map[string]string
	// Tracked is the set of paths tracked in the evaluated revision (`GitPort.ListFilesAt`).
	Tracked map[string]bool
	// Dirty is the set of paths with working-tree changes (`GitPort.DirtyFiles`).
	Dirty map[string]bool
}

// PresenceResult is the committed-presence result of one registered decision.
type PresenceResult struct {
	State PresenceState
	// Reason is the evidence reason: the finding reason for a violation (`index-only`, `absent`,
	// `path-mismatch`), the state name for `uncommitted-edit` and `not-recognized`, one of the Evidence*
	// reasons or `duplicate-id` for `unverifiable`, and "" for `committed`.
	Reason string
	// Path is the decision path that the result names: the matched entity path, else the path pin, else "".
	Path string
	// Decision is the matched evidence entity; nil when no entity matched.
	Decision *EvidenceDecision
	// OtherPaths are the other paths that the result names, sorted: the entity paths of a `path-mismatch`
	// (Path is then the pin), or the candidate paths of a `duplicate-id`.
	OtherPaths []string
}

// reasonUncommittedEdit and reasonNotRecognized are the evidence reasons of the two `unknown` presence states
// that are not finding reasons.
const (
	reasonUncommittedEdit = string(PresenceUncommittedEdit)
	reasonNotRecognized   = string(PresenceNotRecognized)
)

// EvaluatePresence gives the committed-presence result of every entry, keyed by EntryKey (design D3).
//
// An entry with a path pin selects the entity at that path (the pin is project-relative; in a multi-project run
// the entry's project prefix is added). An entry without a pin is matched by canonical label; an entry with
// `project` sees only the decisions of that project. An unpinned entry with more than one tracked candidate, or
// more than one candidate and none tracked, is a duplicate: the result is `unverifiable` with reason
// `duplicate-id` and no decision is picked. With exactly one tracked candidate among several, that one is used
// (the others are not committed and are not a duplicate in committed evidence).
//
// evidence nil or not Supported gives `unverifiable` `no-capability`; a project without revision gives `unverifiable`
// `no-revision`, checked first. An ExtractionComplete=false evidence with no entity gives `unverifiable`
// `extraction-incomplete`.
func EvaluatePresence(entries []Entry, evidence *DecisionEvidence, git *GitFacts) map[string]PresenceResult {
	out := make(map[string]PresenceResult, len(entries))
	facts := presenceFacts{evidence: evidence, git: git, stale: staleSet(evidence)}

	for i := range entries {
		out[EntryKey(&entries[i])] = facts.of(&entries[i])
	}

	return out
}

// presenceFacts are the inputs that every entry is judged against: the decision evidence, the Git facts, and the set
// of paths that the evidence reports as stale.
type presenceFacts struct {
	evidence *DecisionEvidence
	git      *GitFacts
	stale    map[string]bool
}

// staleSet is the set of the stale paths of the evidence; nil for no evidence.
func staleSet(evidence *DecisionEvidence) map[string]bool {
	if evidence == nil {
		return nil
	}

	stale := make(map[string]bool, len(evidence.Stale))
	for _, p := range evidence.Stale {
		stale[p] = true
	}

	return stale
}

// pinPath gives the evidence path of a path pin: project-relative, prefixed with `@<project>/` for a project other
// than the primary one.
func pinPath(entry *Entry) string {
	if entry.Path == "" || strings.HasPrefix(entry.Path, "@") {
		return entry.Path
	}

	project, _ := entry.EvidenceScope()
	if project == "" {
		return entry.Path
	}

	return "@" + project + "/" + entry.Path
}

func (f presenceFacts) of(entry *Entry) PresenceResult {
	pin := pinPath(entry)
	candidates := labelCandidates(entry, f.evidence)

	if f.git == nil || !hasRevisions(entry, candidates, f.git) {
		return PresenceResult{State: PresenceUnverifiable, Reason: EvidenceNoRevision, Path: pin}
	}

	if f.evidence == nil || !f.evidence.Supported {
		return PresenceResult{State: PresenceUnverifiable, Reason: EvidenceNoCapability, Path: pin}
	}

	if pin != "" {
		return f.pinned(pin, candidates)
	}

	return f.unpinned(candidates)
}

// unpinned evaluates an entry without a path pin from its label candidates.
func (f presenceFacts) unpinned(candidates []EvidenceDecision) PresenceResult {
	switch matched := trackedCandidates(candidates, f.git); {
	case len(candidates) == 0:
		if !f.evidence.ExtractionComplete {
			return PresenceResult{State: PresenceUnverifiable, Reason: EvidenceExtractionIncomplete}
		}

		return PresenceResult{State: PresenceAbsent, Reason: string(ReasonAbsent)}
	case len(candidates) == 1:
		return f.entity(&candidates[0])
	case len(matched) == 1:
		return f.entity(&matched[0])
	default:
		return PresenceResult{
			State: PresenceUnverifiable, Reason: string(ReasonDuplicateID), OtherPaths: pathsOf(candidates),
		}
	}
}

// hasRevisions says whether every project that the entry can match has an evaluated revision: the project of the
// entry, else the projects of its label candidates, else the primary project.
func hasRevisions(entry *Entry, candidates []EvidenceDecision, git *GitFacts) bool {
	if project, scoped := entry.EvidenceScope(); scoped || len(candidates) == 0 {
		return git.Revisions[project] != ""
	}

	for i := range candidates {
		if git.Revisions[candidates[i].Project] == "" {
			return false
		}
	}

	return true
}

// pinned evaluates an entry with a path pin.
func (f presenceFacts) pinned(pin string, labelled []EvidenceDecision) PresenceResult {
	if i := slices.IndexFunc(f.evidence.Decisions, func(d EvidenceDecision) bool { return d.Path == pin }); i >= 0 {
		return f.entity(&f.evidence.Decisions[i])
	}

	if len(labelled) > 0 {
		return PresenceResult{
			State: PresencePathMismatch, Reason: string(ReasonPathMismatch), Path: pin, OtherPaths: pathsOf(labelled),
		}
	}

	switch {
	case !f.evidence.ExtractionComplete:
		return PresenceResult{State: PresenceUnverifiable, Reason: EvidenceExtractionIncomplete, Path: pin}
	case f.git.Tracked[pin]:
		return PresenceResult{State: PresenceNotRecognized, Reason: reasonNotRecognized, Path: pin}
	default:
		return PresenceResult{State: PresenceAbsent, Reason: string(ReasonAbsent), Path: pin}
	}
}

// entity gives the result for a matched entity: tracked, dirty, and stale decide the state.
func (f presenceFacts) entity(d *EvidenceDecision) PresenceResult {
	res := PresenceResult{Path: d.Path, Decision: d}

	switch {
	case !f.git.Tracked[d.Path]:
		res.State, res.Reason = PresenceIndexOnly, string(ReasonIndexOnly)
	case f.git.Dirty[d.Path] || f.stale[d.Path]:
		res.State, res.Reason = PresenceUncommittedEdit, reasonUncommittedEdit
	default:
		res.State = PresenceCommitted
	}

	return res
}

// labelCandidates are the evidence decisions whose label equals the canonical ID of the entry, restricted to the
// project of the entry when it names one. They are in evidence order (sorted by path).
func labelCandidates(entry *Entry, evidence *DecisionEvidence) []EvidenceDecision {
	if evidence == nil {
		return nil
	}

	project, scoped := entry.EvidenceScope()

	var out []EvidenceDecision

	for i := range evidence.Decisions {
		d := &evidence.Decisions[i]
		if d.Label != entry.ID {
			continue
		}

		if scoped && d.Project != project {
			continue
		}

		out = append(out, *d)
	}

	return out
}

func trackedCandidates(candidates []EvidenceDecision, git *GitFacts) []EvidenceDecision {
	var out []EvidenceDecision

	for i := range candidates {
		if git.Tracked[candidates[i].Path] {
			out = append(out, candidates[i])
		}
	}

	return out
}

func pathsOf(decisions []EvidenceDecision) []string {
	out := make([]string, 0, len(decisions))
	for i := range decisions {
		out = append(out, decisions[i].Path)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}
