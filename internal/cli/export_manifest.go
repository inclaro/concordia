package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

func bundleManifest(app *Context, run *conformance.Run, opts *exportBundleOptions, entries []bundle.Entry, records []redact.Record) (bundle.Manifest, error) {
	toolchain, err := json.Marshal(run.Toolchain)
	if err != nil {
		return bundle.Manifest{}, fmt.Errorf("encode toolchain: %w", err)
	}

	runInfo, err := json.Marshal(map[string]any{
		"id": run.Run.ID, keySourceRevision: run.Run.SourceRevision,
		"dirtyTreeDigest": run.Run.DirtyTreeDigest, keyGraphGeneration: run.Run.GraphGeneration,
	})
	if err != nil {
		return bundle.Manifest{}, fmt.Errorf("encode run: %w", err)
	}

	return bundle.Manifest{
		SchemaVersion: bundle.SchemaVersion, Kind: bundle.Kind, CreatedAt: run.Run.FinishedAt,
		Concordia: run.Toolchain.Concordia, Toolchain: toolchain, Run: runInfo,
		Options:    bundle.BundleOptions{IncludeGraph: opts.includeGraph, IncludeSourceSnippets: opts.snippets},
		Provenance: bundleProvenance(app, run),
		References: bundleReferences(run, opts, entries),
		Redactions: manifestRedactions(records), RedactionRules: redact.Default().Version,
	}, nil
}

// bundleProvenance gives the OUT-04 R0 items of the run.
func bundleProvenance(app *Context, run *conformance.Run) bundle.Provenance {
	sources, snapshots := provenanceSources(run)
	envs := run.Inputs.Environments

	return bundle.Provenance{
		Calm:      bundle.CalmRef{Release: run.Toolchain.CalmSchema, Digest: run.Inputs.Architecture.Digest},
		Workspace: bundle.WorkspaceRef{Profile: app.Opts.Profile, Path: run.Inputs.Workspace.Path, Digest: run.Inputs.Workspace.Digest},
		Graph: bundle.GraphRef{
			CBMVersion: run.Toolchain.Cbm.ExecutableVersion, CBMSHA256: run.Toolchain.Cbm.SHA256, AdapterSchema: run.Toolchain.Cbm.AdapterSchema,
			Profile: bundle.ProfileRef(run.Toolchain.Cbm.Profile), Snapshots: snapshots,
		},
		Sources:   sources,
		Policies:  provenancePolicies(run),
		Decisions: provenanceDigestRefs(run.Inputs.Decisions),
		Review:    reviewProvenance(run),
		Environments: bundle.EnvironmentsRef{
			Selected: append([]string{}, envs.Selected...), Excluded: append([]string{}, envs.Excluded...), Digest: envs.Digest,
		},
		Evaluators: map[string]string{
			"concordia": run.Toolchain.Concordia, "calmSchema": run.Toolchain.CalmSchema,
			"cbm": run.Toolchain.Cbm.ExecutableVersion, "cbmAdapterSchema": run.Toolchain.Cbm.AdapterSchema,
		},
		Contracts:   map[string]string{"bundleSchema": bundle.SchemaVersion, "resultSchema": conformance.ResultSchemaVersion},
		Exceptions:  provenanceExceptions(run),
		BuildDigest: provenanceBuildDigest(),
		StartedAt:   run.Run.StartedAt,
		FinishedAt:  run.Run.FinishedAt,
	}
}

// provenanceSources gives the source revision of each project of the run, with the sorted graph snapshots of
// the run. A run without projects has the revision and the snapshot of the run itself.
func provenanceSources(run *conformance.Run) (sources []bundle.SourceRevision, snapshots []string) {
	clean, _ := workspace.DirtyTreeDigest("", nil)

	if len(run.Run.Projects) == 0 {
		return []bundle.SourceRevision{{
			Project: run.Run.Project, Revision: run.Run.SourceRevision,
			Dirty: run.Run.DirtyTreeDigest != clean, DirtyTreeDigest: run.Run.DirtyTreeDigest,
		}}, []string{run.Run.GraphGeneration}
	}

	sources = make([]bundle.SourceRevision, 0, len(run.Run.Projects))
	snapshots = make([]string, 0, len(run.Run.Projects))

	for i := range run.Run.Projects {
		proj := &run.Run.Projects[i]

		sources = append(sources, bundle.SourceRevision{
			Project: proj.Name, Revision: proj.SourceRevision, Dirty: proj.DirtyTreeDigest != clean, DirtyTreeDigest: proj.DirtyTreeDigest,
		})
		snapshots = append(snapshots, proj.GraphGeneration)
	}

	slices.Sort(snapshots)

	return sources, snapshots
}

// provenanceDigestRefs gives the path and digest of each recorded input.
func provenanceDigestRefs(refs []conformance.InputRef) []bundle.DigestRef {
	out := make([]bundle.DigestRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, bundle.DigestRef{Path: ref.Path, Digest: ref.Digest})
	}

	return out
}

// provenancePolicies gives the policy files of the run, then the ID and version of each policy that its rules use.
func provenancePolicies(run *conformance.Run) []bundle.DigestRef {
	versions := map[string]string{}

	for i := range run.Rules {
		if policy := &run.Rules[i].Policy; policy.ID != "" {
			versions[policy.ID] = policy.Version
		}
	}

	policies := provenanceDigestRefs(run.Inputs.Policies)

	for _, id := range slices.Sorted(maps.Keys(versions)) {
		policies = append(policies, bundle.DigestRef{ID: id, Version: versions[id]})
	}

	return policies
}

// provenanceExceptions gives the waivers that the run used, with the baseline and waiver files.
func provenanceExceptions(run *conformance.Run) bundle.ExceptionsRef {
	used := make([]string, 0, len(run.Exceptions.Waivers))
	for i := range run.Exceptions.Waivers {
		used = append(used, run.Exceptions.Waivers[i].ID)
	}

	exceptions := bundle.ExceptionsRef{WaiversUsed: used}
	if b := run.Inputs.Baseline; b != nil {
		exceptions.Baseline = &bundle.DigestRef{Path: b.Path, Digest: b.Digest}
	}

	if w := run.Inputs.Waivers; w != nil {
		exceptions.Waivers = &bundle.DigestRef{Path: w.Path, Digest: w.Digest}
	}

	return exceptions
}

// provenanceBuildDigest gives the digest of the running executable, or the empty string when it cannot be read.
func provenanceBuildDigest() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}

	sum, _ := digest.OfFile(exe)

	return sum
}

// exportRefs lists the references of a bundle: what the result depends on by digest, and whether the bundle holds it.
type exportRefs struct {
	have map[string]bool
	refs []bundle.Reference
}

// bundleReferences lists what the result depends on by digest and whether the bundle holds it.
func bundleReferences(run *conformance.Run, opts *exportBundleOptions, entries []bundle.Entry) []bundle.Reference {
	have := make(map[string]bool, len(entries))
	for _, e := range entries {
		have[e.Path] = true
	}

	list := &exportRefs{have: have}
	list.addReference("graph-snapshot", run.Run.GraphGeneration, opts.includeGraph)
	list.add("workspace", run.Inputs.Workspace)
	list.add("architecture", run.Inputs.Architecture)
	list.addSingles(&run.Inputs)
	list.addLists(&run.Inputs)
	list.addDecisions(&run.Inputs)

	return append(list.refs, reviewReferences(run, have)...)
}

// addReference appends a reference that is included or else unavailable because the bundle does not hold it.
func (l *exportRefs) addReference(kind, sum string, included bool) {
	ref := bundle.Reference{Kind: kind, Digest: sum, Availability: bundle.Included}
	if !included {
		ref.Availability, ref.Reason = bundle.Unavailable, bundle.ReasonNotIncluded
	}

	l.refs = append(l.refs, ref)
}

// add appends the reference of an input that the bundle may hold as `inputs/<path>`.
func (l *exportRefs) add(kind string, in conformance.InputRef) {
	if strings.HasPrefix(in.Path, "builtin:") {
		return // built into the executable; the build digest identifies it.
	}

	l.addReference(kind, in.Digest, l.have["inputs/"+in.Path])
}

func (l *exportRefs) addSingles(inputs *conformance.RunInputs) {
	for _, single := range []struct {
		in   *conformance.InputRef
		kind string
	}{{inputs.Lock, "lock"}, {inputs.Baseline, "baseline"}, {inputs.Waivers, "waivers"}} {
		if single.in != nil {
			l.add(single.kind, *single.in)
		}
	}
}

func (l *exportRefs) addLists(inputs *conformance.RunInputs) {
	for _, list := range []struct {
		kind string
		in   []conformance.InputRef
	}{{"pattern", inputs.Patterns}, {"decorator", inputs.Decorators}, {"policy", inputs.Policies}, {"binding", inputs.Bindings}, {"trace", inputs.Traces}} {
		for _, in := range list.in {
			l.add(list.kind, in)
		}
	}
}

func (l *exportRefs) addDecisions(inputs *conformance.RunInputs) {
	for _, ref := range inputs.Decisions {
		if !isWorkspacePart(inputs, ref) {
			l.add("decision", ref)

			continue
		}

		// The lifecycle profile is part of the workspace file: the bundle holds it when it holds that file.
		l.addReference("decision-lifecycle", ref.Digest, l.have["inputs/"+inputs.Workspace.Path])
	}
}

// isWorkspacePart says whether a recorded input is a part of the workspace file and not a file of its own: the ADR
// lifecycle profile has the path `<workspace file>#adr.lifecycle`. The bundle holds no file under that path.
func isWorkspacePart(inputs *conformance.RunInputs, ref conformance.InputRef) bool {
	return strings.HasPrefix(ref.Path, inputs.Workspace.Path+"#")
}

func inputRefsOf(inputs *conformance.RunInputs) []conformance.InputRef {
	refs := []conformance.InputRef{inputs.Workspace, inputs.Architecture}

	for _, single := range []*conformance.InputRef{inputs.Lock, inputs.Baseline, inputs.Waivers} {
		if single != nil {
			refs = append(refs, *single)
		}
	}

	for _, list := range [][]conformance.InputRef{inputs.Patterns, inputs.Decorators, inputs.Policies, inputs.Bindings, inputs.Traces} {
		refs = append(refs, list...)
	}

	for _, ref := range inputs.Decisions {
		if !isWorkspacePart(inputs, ref) {
			refs = append(refs, ref)
		}
	}

	return refs
}

// manifestRedactions turns the records into the manifest list: one entry for each place and class,
// with the count, sorted. No entry has a value.
func manifestRedactions(records []redact.Record) []bundle.Redaction {
	counts := map[bundle.Redaction]int{}

	for _, r := range records {
		counts[bundle.Redaction{Path: r.Output, File: r.File, Property: r.Property, Class: r.Class, Line: r.Line}]++
	}

	out := make([]bundle.Redaction, 0, len(counts))

	for key, n := range counts {
		key.Count = n
		out = append(out, key)
	}

	slices.SortFunc(out, func(a, b bundle.Redaction) int {
		return cmp.Or(
			strings.Compare(a.Path, b.Path), strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line),
			strings.Compare(a.Property, b.Property), strings.Compare(a.Class, b.Class),
		)
	})

	return out
}
