package quality

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// raw types mirror the schema-valid document.
type rawSet struct {
	Scenarios []rawScenario `json:"scenarios"`
	Findings  []rawFinding  `json:"findings"`
}

type rawScenario struct {
	ExpectedResponse string    `json:"expectedResponse"`
	Area             string    `json:"area"`
	QualityAttribute string    `json:"qualityAttribute"`
	StimulusSource   string    `json:"stimulusSource"`
	Stimulus         string    `json:"stimulus"`
	Environment      string    `json:"environment"`
	ID               string    `json:"id"`
	Owner            string    `json:"owner"`
	Mode             Mode      `json:"mode"`
	Enforcement      string    `json:"enforcement"`
	Scope            []string  `json:"scope"`
	AddressedBy      []string  `json:"addressedBy"`
	Criterion        Criterion `json:"criterion"`
}

type rawFinding struct {
	ID          string        `json:"id"`
	Type        FindingType   `json:"type"`
	Statement   string        `json:"statement"`
	Owner       string        `json:"owner"`
	Status      FindingStatus `json:"status"`
	Scenarios   []string      `json:"scenarios"`
	Areas       []string      `json:"areas"`
	AddressedBy []string      `json:"addressedBy"`
	Assumptions []string      `json:"assumptions"`
}

// build converts the schema-valid document into the Go types with the line of every item.
func build(src sourceDoc, doc map[string]any) (*Set, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("cannot convert: %w", err)
	}

	var raw rawSet
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cannot convert: %w", err)
	}

	set := &Set{Scenarios: make([]*Scenario, 0, len(raw.Scenarios)), Findings: make([]*Finding, 0, len(raw.Findings))}

	for i := range raw.Scenarios {
		set.Scenarios = append(set.Scenarios, buildScenario(src, &raw.Scenarios[i], "/scenarios/"+strconv.Itoa(i)))
	}

	for i := range raw.Findings {
		set.Findings = append(set.Findings, buildFinding(src, &raw.Findings[i], "/findings/"+strconv.Itoa(i)))
	}

	return set, nil
}

func buildScenario(src sourceDoc, entry *rawScenario, base string) *Scenario {
	enforcement := EnforcementAdvisory
	if entry.Enforcement != "" {
		enforcement = Enforcement(entry.Enforcement)
	}

	return &Scenario{
		ID: entry.ID, Area: entry.Area, QualityAttribute: entry.QualityAttribute, StimulusSource: entry.StimulusSource,
		Stimulus: entry.Stimulus, Environment: entry.Environment, ExpectedResponse: entry.ExpectedResponse, Owner: entry.Owner,
		Mode: entry.Mode, Enforcement: enforcement, Criterion: entry.Criterion, Scope: entry.Scope,
		AddressedBy: src.adrRefs(entry.AddressedBy, base+"/addressedBy"),
		Location:    src.location(base),
		scopeLines:  src.lines(entry.Scope, base+"/scope"),
		ruleLines:   src.lines(entry.Criterion.Rules, base+"/criterion/rules"),
	}
}

func buildFinding(src sourceDoc, entry *rawFinding, base string) *Finding {
	return &Finding{
		ID: entry.ID, Type: entry.Type, Statement: entry.Statement, Owner: entry.Owner, Status: entry.Status,
		Scenarios: entry.Scenarios, Areas: entry.Areas, AddressedBy: src.adrRefs(entry.AddressedBy, base+"/addressedBy"),
		Assumptions: nonNil(entry.Assumptions), Location: src.location(base),
		scenarioLines: src.lines(entry.Scenarios, base+"/scenarios"),
	}
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}

	return list
}

func (s sourceDoc) adrRefs(ids []string, pointer string) []ADRRef {
	out := make([]ADRRef, 0, len(ids))

	for i, id := range ids {
		out = append(out, ADRRef{ID: id, Location: s.location(pointer + "/" + strconv.Itoa(i))})
	}

	return out
}

func (s sourceDoc) lines(items []string, pointer string) []int {
	out := make([]int, len(items))

	for i := range items {
		out[i] = s.lineAtPointer(pointer + "/" + strconv.Itoa(i))
	}

	return out
}
