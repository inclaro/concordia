package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
)

// The evaluation bundle holds the assurance evidence as digests, typed facts, and redacted statements, and never a
// rendered value (design D16, D17):
//
//	assurance/render/<environment>.json   the rendered sources of one environment: renderer, digests, coverage, and the
//	                                      inventory of the resources (identity, digest, repository location)
//	assurance/plans/<environment>.json    the plans of one environment: digest, binding, and the planned addresses
//	assurance/statements/<sha256>.json    one redacted statement (statement.OutputPath)
//	assurance/contracts.json              the claim contracts, the evaluators, the toolchain, the trust contract
//	assurance/quality/scenarios.json      the loaded scenario files and scenarios
//	assurance/quality/results.json        the verdict of each scenario
const (
	assuranceDir          = "assurance/"
	assuranceRenderDir    = assuranceDir + "render/"
	assurancePlansDir     = assuranceDir + "plans/"
	assuranceContractsDoc = assuranceDir + "contracts.json"
	assuranceQualityDir   = assuranceDir + "quality/"
)

// AssuranceFile is one file that the assurance evidence adds to the evaluation bundle.
type AssuranceFile struct {
	Path string
	Data []byte
}

// renderFile is `assurance/render/<environment>.json`.
type renderFile struct {
	Environment string               `json:"environment"`
	Sources     []renderedFileSource `json:"sources"`
}

type renderedFileSource struct {
	Resources []renderedFileResource `json:"resources"`
	RenderedEntry
	Redactions int `json:"redactions"`
}

type renderedFileResource struct {
	Group     string   `json:"group,omitempty"`
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace,omitempty"`
	Name      string   `json:"name"`
	Digest    string   `json:"digest"`
	Location  string   `json:"location"`
	DataKeys  []string `json:"dataKeys,omitempty"`
	Conflict  bool     `json:"conflict,omitempty"`
}

// planFile is `assurance/plans/<environment>.json`.
type planFile struct {
	Environment string          `json:"environment"`
	Plans       []planFileEntry `json:"plans"`
}

type planFileEntry struct {
	Resources []planFileResource `json:"resources"`
	PlanEntry
}

type planFileResource struct {
	Address string   `json:"address"`
	Type    string   `json:"type"`
	Mode    string   `json:"mode"`
	Actions []string `json:"actions"`
	Unknown []string `json:"unknown,omitempty"`
}

// contractsFile is `assurance/contracts.json`.
type contractsFile struct {
	Cosign               string              `json:"cosign,omitempty"`
	ClaimsRegistryDigest string              `json:"claimsRegistryDigest"`
	TrustContractDigest  string              `json:"trustContractDigest,omitempty"`
	Contracts            []contractRef       `json:"contracts"`
	Evaluators           []EvaluatorIdentity `json:"evaluators"`
	Renderers            []render.Identity   `json:"renderers"`
}

type contractRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// qualityScenariosFile is `assurance/quality/scenarios.json`.
type qualityScenariosFile struct {
	Files     any `json:"files"`
	Scenarios any `json:"scenarios"`
	Findings  any `json:"findings"`
}

// AssuranceBundleFiles gives the files that the assurance evidence of a run adds to the evaluation bundle, and the
// redaction records of the values that were removed from them. A run without assurance evidence adds nothing. The
// order of the files and of the records is fixed, so two exports of one run are equal.
func AssuranceBundleFiles(run *Run) (files []AssuranceFile, records []redact.Record, err error) {
	if run.Assurance == nil || run.AssuranceEvidence == nil {
		return nil, nil, nil
	}

	set := &assuranceFileSet{}

	steps := []func(*Run) error{set.addRenders, set.addPlans, set.addStatements, set.addContracts, set.addQuality}
	for _, step := range steps {
		if err := step(run); err != nil {
			return nil, nil, err
		}
	}

	slices.SortFunc(set.files, func(a, b AssuranceFile) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	return set.files, assuranceRedactions(run.AssuranceEvidence.Evidence), nil
}

// assuranceFileSet collects the files that the assurance evidence adds to the bundle.
type assuranceFileSet struct {
	files []AssuranceFile
}

func (s *assuranceFileSet) add(path string, value any) error {
	data, err := canonjson.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	s.files = append(s.files, AssuranceFile{Path: path, Data: data})

	return nil
}

func (s *assuranceFileSet) addRenders(run *Run) error {
	for _, env := range renderEnvironments(run.Assurance.Rendered) {
		file := renderFileOf(env, run.AssuranceEvidence.Evidence, run.Assurance)
		if err := s.add(assuranceRenderDir+bundleName(env)+".json", file); err != nil {
			return err
		}
	}

	return nil
}

func (s *assuranceFileSet) addPlans(run *Run) error {
	for _, env := range planEnvironments(run.Assurance.Plans) {
		file := planFileOf(env, run.AssuranceEvidence.Evidence, run.Assurance)
		if err := s.add(assurancePlansDir+bundleName(env)+".json", file); err != nil {
			return err
		}
	}

	return nil
}

func (s *assuranceFileSet) addStatements(run *Run) error {
	persisted := run.AssuranceEvidence.Evidence.Persisted
	for i := range persisted {
		if err := s.add(statement.OutputPath(persisted[i].Digest), &persisted[i]); err != nil {
			return err
		}
	}

	return nil
}

func (s *assuranceFileSet) addContracts(run *Run) error {
	contracts, err := contractsOf(run)
	if err != nil {
		return err
	}

	return s.add(assuranceContractsDoc, contracts)
}

func (s *assuranceFileSet) addQuality(run *Run) error {
	found := run.AssuranceEvidence.Evidence
	if found.Scenarios == nil {
		return nil
	}

	scenarios := qualityScenariosFile{Files: found.Scenarios.Files, Scenarios: found.Scenarios.Scenarios, Findings: found.Scenarios.Findings}
	if err := s.add(assuranceQualityDir+"scenarios.json", scenarios); err != nil {
		return err
	}

	return s.add(assuranceQualityDir+"results.json", qualityResultsOf(run.Assurance))
}

// bundleName makes an environment id safe as a file name: the id never leaves its directory.
func bundleName(id string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(id)
}

func renderEnvironments(entries []RenderedEntry) []string {
	out := make([]string, 0, len(entries))

	for i := range entries {
		out = append(out, entries[i].Environment)
	}

	return sortedUniqueEnvironments(out)
}

func planEnvironments(entries []PlanEntry) []string {
	out := make([]string, 0, len(entries))

	for i := range entries {
		out = append(out, entries[i].Environment)
	}

	return sortedUniqueEnvironments(out)
}

func sortedUniqueEnvironments(ids []string) []string {
	slices.SortFunc(ids, canonjson.CompareUTF16)

	return slices.Compact(ids)
}

func renderFileOf(env string, found *evidence.Evidence, report *AssuranceReport) renderFile {
	out := renderFile{Environment: env, Sources: []renderedFileSource{}}

	for i := range report.Rendered {
		if report.Rendered[i].Environment == env {
			out.Sources = append(out.Sources, renderedSourceOf(env, &report.Rendered[i], found))
		}
	}

	return out
}

// renderedSourceOf is one rendered source with the resources of the render results that produced it.
func renderedSourceOf(env string, entry *RenderedEntry, found *evidence.Evidence) renderedFileSource {
	source := renderedFileSource{RenderedEntry: *entry, Resources: []renderedFileResource{}}

	for _, res := range found.Renders {
		if res.Environment != env || res.Path != entry.Source || res.Tool != entry.Tool {
			continue
		}

		source.Redactions = len(res.Redactions)

		for j := range res.Resources {
			source.Resources = append(source.Resources, renderedResourceOf(&res.Resources[j]))
		}
	}

	return source
}

func renderedResourceOf(r *render.Resource) renderedFileResource {
	return renderedFileResource{
		Group: r.ID.Group, Kind: r.ID.Kind, Namespace: r.ID.Namespace, Name: r.ID.Name,
		Digest: r.Digest, Location: r.Location, Conflict: r.Conflict, DataKeys: r.DataKeys,
	}
}

func planFileOf(env string, found *evidence.Evidence, report *AssuranceReport) planFile {
	out := planFile{Environment: env, Plans: []planFileEntry{}}

	for i := range report.Plans {
		if report.Plans[i].Environment == env {
			out.Plans = append(out.Plans, planEntryOf(env, &report.Plans[i], found))
		}
	}

	return out
}

// planEntryOf is one declared plan with the resources of the plans that were read for it.
func planEntryOf(env string, entry *PlanEntry, found *evidence.Evidence) planFileEntry {
	item := planFileEntry{PlanEntry: *entry, Resources: []planFileResource{}}

	for j := range found.Plans {
		declared := &found.Plans[j]
		if !declaresPlan(declared, env, entry.File) {
			continue
		}

		item.Resources = append(item.Resources, planResources(declared.Plan)...)
	}

	return item
}

// declaresPlan reports whether a declared plan of one environment and file was read.
func declaresPlan(declared *evidence.Plan, env, file string) bool {
	return declared.Plan != nil && declared.Declared.Environment == env && declared.Declared.File == file
}

func planResources(p *plan.Plan) []planFileResource {
	out := make([]planFileResource, 0, len(p.Resources))

	for i := range p.Resources {
		r := &p.Resources[i]
		out = append(out, planFileResource{Address: r.Address, Type: r.Type, Mode: r.Mode, Actions: r.Actions, Unknown: r.Unknown})
	}

	return out
}

// contractsOf lists the claim contracts that the run loaded with their digests, the evaluators, and the toolchain.
func contractsOf(run *Run) (contractsFile, error) {
	out := contractsFile{Contracts: []contractRef{}, Evaluators: []EvaluatorIdentity{}, Renderers: []render.Identity{}}

	if tc := run.Toolchain.Assurance; tc != nil {
		out.Evaluators, out.Renderers, out.Cosign = tc.Evaluators, tc.Renderers, tc.Cosign
		out.ClaimsRegistryDigest, out.TrustContractDigest = tc.ClaimsRegistryDigest, tc.TrustContractDigest
	}

	for i := range run.AssuranceEvidence.Contracts {
		contract := &run.AssuranceEvidence.Contracts[i]

		text, err := canonjson.Marshal(contract)
		if err != nil {
			return contractsFile{}, fmt.Errorf("encode contract %s: %w", contract.ID, err)
		}

		out.Contracts = append(out.Contracts, contractRef{ID: contract.ID, Version: contract.Version, Digest: digest.OfBytes(text)})
	}

	return out, nil
}

// assuranceRedactions lists every value that was removed from the assurance evidence, never the value. They are
// the render, plan, and statement redactions with the bundle file that would have held the value.
func assuranceRedactions(found *evidence.Evidence) []redact.Record {
	out := renderRedactionsOf(found)
	out = append(out, planRedactionsOf(found)...)

	for i := range found.Persisted {
		out = append(out, found.Persisted[i].Redactions...)
	}

	slices.SortStableFunc(out, func(a, b redact.Record) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Output, b.Output), canonjson.CompareUTF16(a.File, b.File), cmp.Compare(a.Line, b.Line),
			canonjson.CompareUTF16(a.Property, b.Property), canonjson.CompareUTF16(a.Class, b.Class),
		)
	})

	return slices.Compact(out)
}

// renderRedactionsOf gives the redactions of the rendered sources with the render file that would have held the value.
func renderRedactionsOf(found *evidence.Evidence) []redact.Record {
	var out []redact.Record

	for _, res := range found.Renders {
		for _, rec := range res.Redactions {
			rec.Output = assuranceRenderDir + bundleName(res.Environment) + ".json"
			out = append(out, rec)
		}
	}

	return out
}

// planRedactionsOf gives the redactions of the plans with the plan file that would have held the value.
func planRedactionsOf(found *evidence.Evidence) []redact.Record {
	var out []redact.Record

	for i := range found.Plans {
		declared := &found.Plans[i]
		if declared.Plan == nil {
			continue
		}

		for _, rec := range declared.Plan.Redactions {
			rec.Output = assurancePlansDir + bundleName(declared.Declared.Environment) + ".json"
			out = append(out, rec)
		}
	}

	return out
}

// trustContractDigestOf is the digest of the producer trust contract, or "" for a workspace that declares no
// provenance or record.
func trustContractDigestOf(a *assuranceRun) string {
	if a.cfg.Provenance == nil && a.cfg.Records == nil {
		return ""
	}

	contract := provenance.NewTrustContract(a.cfg)

	return contract.Digest()
}

// qualityResultsFile is `assurance/quality/results.json`.
type qualityResultsFile struct {
	Quality      []QualityEntry     `json:"quality"`
	QualityAreas []QualityAreaEntry `json:"qualityAreas"`
}

func qualityResultsOf(report *AssuranceReport) qualityResultsFile {
	return qualityResultsFile{Quality: report.Quality, QualityAreas: report.QualityAreas}
}

// assuranceManifestRedactions gives the redaction records of the assurance evidence for the run manifest.
func assuranceManifestRedactions(run *Run) []redact.Record {
	if run.AssuranceEvidence == nil {
		return nil
	}

	return assuranceRedactions(run.AssuranceEvidence.Evidence)
}
