package adr

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
)

// supersedesFamily is the capability family that gives decision supersession evidence. The `ADR` nodes and
// the `supersedes` edges come from the same provider extraction, so the family of the edge stands for both (the
// profile has no separate family for the nodes).
var supersedesFamily = string(observation.KindSupersedes)

// markdownLanguage is the coverage key of Markdown files, the files that decisions live in.
const markdownLanguage = ".md"

// observedIDPrefix and observedIDSeparator frame the project name in an observed ID: `cbm:<project>:<id>`.
const (
	observedIDPrefix    = "cbm:"
	observedIDSeparator = ":"
)

// BuildEvidence builds the decision evidence of a run from the merged normalized evidence. Decisions are the
// `ADR` nodes (the nodes of the evidence and the synthesized decision entities, so a decision that no edge
// touches is kept too); the path with the project prefix is the identity, and the canonical `adr_id` is a label
// only. A `supersedes` edge runs from the new decision to the old one. `mentions` and `references-file` edges
// that start in a decision file are candidate evidence only.
//
// Supported follows the capability profile: the decision evidence is relied on only when the `supersedes`
// family is supplied. UnresolvedSupersedesObservable is false: CBM 0.12.1 emits no node or edge for a "supersedes
// X" statement whose X is not a committed decision (O12). ExtractionComplete starts as Supported, and
// ApplyCoverage narrows it and adds the stale files from the file coverage of each project.
//
//nolint:gocritic // hugeParam: the signature takes the capabilities by value, as Provider.Capabilities gives them.
func BuildEvidence(evidence *observation.EvidenceQueryResult, caps observation.Capabilities, projects []conformance.DecisionProject) DecisionEvidence {
	family, present := caps.Families[supersedesFamily]
	supported := present && family.Level != observation.SupportUnsupported

	out := DecisionEvidence{
		Supported:          supported,
		ExtractionComplete: supported,
		Decisions:          []EvidenceDecision{},
		Supersedes:         []SupersedesEdge{},
		Mentions:           []Mention{},
		Stale:              []string{},
	}

	if evidence == nil {
		return out
	}

	b := evidenceBuilder{ev: evidence, projects: projects, byPath: map[string]struct{}{}, byID: map[string]string{}}
	b.decisions(&out)
	b.edges(&out)

	slices.SortFunc(out.Decisions, func(x, y EvidenceDecision) int { return canonjson.CompareUTF16(x.Path, y.Path) })
	slices.SortFunc(out.Supersedes, func(x, y SupersedesEdge) int {
		if c := canonjson.CompareUTF16(x.From, y.From); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(x.To, y.To)
	})
	slices.SortFunc(out.Mentions, func(x, y Mention) int {
		if c := canonjson.CompareUTF16(x.From, y.From); c != 0 {
			return c
		}

		if c := canonjson.CompareUTF16(x.Subject, y.Subject); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(x.Kind, y.Kind)
	})

	return out
}

// ApplyCoverage adds the file coverage of one project: its stale files join Stale, and ExtractionComplete becomes
// false when a Markdown file of the project is in scope, not excluded, not dirty, and not indexed, because the
// absence of a decision cannot be shown then. An excluded Markdown file is outside the scope, and a dirty one
// reaches presence through the dirty and stale files, so neither makes extraction incomplete. The coverage paths
// already carry the project prefix (DecisionEvalInput.Coverage), so they are used as they are.
func (e *DecisionEvidence) ApplyCoverage(cov *observation.FileCoverage) {
	e.Stale = append(e.Stale, cov.Stale...)

	slices.SortFunc(e.Stale, canonjson.CompareUTF16)
	e.Stale = slices.Compact(e.Stale)

	if slices.ContainsFunc(cov.Unindexed, isMarkdown) {
		e.ExtractionComplete = false
	}
}

// isMarkdown says whether a file is a Markdown file, the kind of file that decisions live in.
func isMarkdown(file string) bool {
	return strings.EqualFold(path.Ext(file), markdownLanguage)
}

type evidenceBuilder struct {
	ev       *observation.EvidenceQueryResult
	byPath   map[string]struct{}
	byID     map[string]string
	projects []conformance.DecisionProject
}

// projectOf gives the workspace project of an observed ID; "" for the primary project, an unknown project, or a
// synthesized ID. The paths of the evidence already carry the project prefix (the pipeline prefixes the evidence
// of every project other than the primary one), so only the project name is read here.
func (b *evidenceBuilder) projectOf(observedID string) string {
	rest, ok := strings.CutPrefix(observedID, observedIDPrefix)
	if !ok {
		return ""
	}

	cut := strings.LastIndex(rest, observedIDSeparator)
	if cut < 0 {
		return ""
	}

	name := rest[:cut]

	for i := range b.projects {
		if b.projects[i].Prefix != "" && b.projects[i].Name == name {
			return name
		}
	}

	return ""
}

// decisions collects the `ADR` nodes of the evidence, then the decision entities of paths that no node gave.
func (b *evidenceBuilder) decisions(out *DecisionEvidence) {
	add := func(node *observation.ObservedNode) {
		decision, err := observation.DecisionOfNode(node)
		if err != nil {
			return
		}

		project := b.projectOf(node.ObservedID)
		path := decision.Path

		if _, seen := b.byPath[path]; seen {
			b.byID[node.ObservedID] = path

			return
		}

		b.byPath[path] = struct{}{}
		b.byID[node.ObservedID] = path

		out.Decisions = append(out.Decisions, EvidenceDecision{
			Path:         path,
			Project:      project,
			ObservedID:   node.ObservedID,
			AdrID:        decision.AdrID,
			Label:        CanonicalID(decision.AdrID),
			Title:        decision.Title,
			Status:       decision.Status,
			StatusText:   decision.StatusText,
			Date:         decision.Date,
			Deciders:     node.Properties["deciders"],
			SupersededBy: decision.SupersededBy,
			Line:         node.StartLine,
		})
	}

	for _, id := range sortedNodeIDs(b.ev.Nodes) {
		node := b.ev.Nodes[id]
		if node.Label == observation.LabelDecision {
			add(&node)
		}
	}

	for i := range b.ev.Entities {
		node := &b.ev.Entities[i]
		if node.Label != observation.LabelDecision {
			continue
		}

		if _, seen := b.byPath[node.FilePath]; seen {
			b.byID[node.ObservedID] = node.FilePath

			continue
		}

		add(node)
	}
}

func sortedNodeIDs(nodes map[string]observation.ObservedNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	return ids
}

// node finds an endpoint of an edge among the nodes and the entities.
func (b *evidenceBuilder) node(id string) (observation.ObservedNode, bool) {
	if n, ok := b.ev.Nodes[id]; ok {
		return n, true
	}

	for i := range b.ev.Entities {
		if b.ev.Entities[i].ObservedID == id {
			return b.ev.Entities[i], true
		}
	}

	return observation.ObservedNode{}, false
}

// edges collects the `supersedes` edges and the mentions that start in a decision file.
func (b *evidenceBuilder) edges(out *DecisionEvidence) {
	for _, edge := range b.ev.Edges {
		if edge.Kind == observation.KindSupersedes {
			from, fromOK := b.byID[edge.From]
			to, toOK := b.byID[edge.To]

			if fromOK && toOK {
				out.Supersedes = append(out.Supersedes, SupersedesEdge{From: from, To: to, Line: edgeLine(edge)})
			}

			continue
		}

		if edge.Kind == observation.KindMentions || edge.Kind == observation.KindReferencesFile {
			b.mention(out, edge)
		}
	}
}

// mention keeps a `mentions` or `references-file` edge whose source is in a decision file.
func (b *evidenceBuilder) mention(out *DecisionEvidence, edge *observation.EvidenceEdge) {
	source, found := b.node(edge.From)
	if !found {
		return
	}

	from := source.FilePath

	if _, isDecision := b.byPath[from]; !isDecision {
		return
	}

	target, found := b.node(edge.To)
	if !found {
		return
	}

	subject := target.FilePath
	if subject == "" {
		subject = target.QualifiedName
	}

	out.Mentions = append(out.Mentions, Mention{From: from, Subject: subject, Kind: string(edge.Kind)})
}

// edgeLine gives the source line of an edge: the span of its location, else its `line` property, else 0.
func edgeLine(edge *observation.EvidenceEdge) int {
	if edge.SourceSpan != nil && edge.SourceSpan.Line > 0 {
		return edge.SourceSpan.Line
	}

	line, err := strconv.Atoi(edge.Properties["line"])
	if err != nil || line < 0 {
		return 0
	}

	return line
}
