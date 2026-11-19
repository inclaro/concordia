package reporting_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

const updateGoldenEnv = "CONCORDIA_UPDATE_GOLDEN"

// golden compares text with testdata/<name>; CONCORDIA_UPDATE_GOLDEN=1 rewrites the file.
func golden(t *testing.T, name, text string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if os.Getenv(updateGoldenEnv) == "1" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if text != string(want) {
		t.Errorf("%s differs from the golden file (%s=1 rewrites it):\ngot:\n%s\nwant:\n%s", name, updateGoldenEnv, text, want)
	}
}

func TestADRGovernanceArea(t *testing.T) {
	t.Parallel()

	t.Run("A12 golden", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(true)
		equal(t, run.Areas.ADRGovernance.Verdict, conformance.AreaVerdict(conformance.VerdictViolation))
		equal(t, run.Areas.ADRGovernance.Counts["violation"], 2)
		equal(t, run.Areas.ADRGovernance.Counts["findings"], 2)

		golden(t, "adr-a12.pretty.golden", reporting.RenderPretty(run, plain))
	})

	t.Run("all-pass register golden", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(false)
		equal(t, run.Areas.ADRGovernance.Verdict, conformance.AreaVerdict(conformance.VerdictPass))

		golden(t, "adr-allpass.pretty.golden", reporting.RenderPretty(run, plain))
	})

	// Scenario: Area in the terminal report.
	t.Run("a violating area is not a single overall pass", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.ADRRun(true), plain)
		contains(t, text, "Violation area(s): ADR governance")
		notContains(t, text, "ADR governance: pass")
		notContains(t, text, "Not configured: Deployment trace, ADR governance")
	})

	t.Run("an advisory-only violation still names the area and is not green", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(true)
		run.Summary.ExitCode = 0

		text := reporting.RenderPretty(run, reporting.PrettyOptions{Color: true})
		contains(t, text, "Violation area(s): ADR governance")
		notContains(t, text, "\x1b[32m"+lastLine(text))
	})

	t.Run("an unknown area is named before the summary line", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(false)
		run.Areas.ADRGovernance.Verdict = conformance.AreaVerdict(conformance.VerdictUnknown)
		run.Summary.ExitCode = 0

		text := reporting.RenderPretty(run, reporting.PrettyOptions{Color: true})
		contains(t, text, "Unknown area(s): ADR governance")
		notContains(t, text, "\x1b[32m0 active")
	})

	t.Run("no register leaves the report as it was", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.SampleRun(), plain)
		notContains(t, text, "ADR governance:")
		notContains(t, text, "Violation area(s)")
		contains(t, text, "Not configured: Deployment trace, ADR governance")
	})

	t.Run("unsupported provider is stated", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(false)
		run.DecisionSummary.ProviderSupport = "unsupported"
		contains(t, reporting.RenderPretty(run, plain), "  Provider support: unsupported")
	})

	t.Run("control sequences of an ADR path are neutralized", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(false)
		run.DecisionSummary.Decisions[0].Path = "docs/adr/\x1b[31mevil.md"

		text := reporting.RenderPretty(run, plain)
		notContains(t, text, "\x1b[31m")
	})
}

func TestADRGovernanceSARIF(t *testing.T) {
	t.Parallel()

	t.Run("the location is the ADR path and line", func(t *testing.T) {
		t.Parallel()

		data, doc := renderSarif(t, testutil.ADRRun(true))
		assertValid(t, sarifValidator(t), data)

		var found bool

		for _, result := range doc.Runs[0].Results {
			if result.RuleID != "GOV-ADR-002" {
				continue
			}

			found = true

			equal(t, len(result.Locations), 1)

			loc := result.Locations[0].PhysicalLocation
			equal(t, loc.ArtifactLocation.URI, testutil.ADRPath)
			equal(t, loc.ArtifactLocation.URIBaseID, "SRCROOT")
			equal(t, loc.Region.StartLine, testutil.ADRStatusLine)
			equal(t, result.Properties["area"], any("adrGovernance"))
			equal(t, result.Properties["decisionId"], any("ADR-3"))
		}

		if !found {
			t.Fatalf("no GOV-ADR-002 result in %s", data)
		}
	})

	t.Run("a finding without a location falls back to the register entry", func(t *testing.T) {
		t.Parallel()

		_, doc := renderSarif(t, testutil.ADRRun(true))

		for _, result := range doc.Runs[0].Results {
			if result.RuleID == "GOV-ADR-003" {
				equal(t, result.Locations[0].PhysicalLocation.ArtifactLocation.URI, testutil.ADRRegisterPath)
				equal(t, result.Locations[0].PhysicalLocation.Region.StartLine, 12)
			}
		}
	})

	t.Run("a decision finding with no location uses the ADR file of its decision", func(t *testing.T) {
		t.Parallel()

		run := testutil.ADRRun(true)

		for i := range run.Findings {
			if run.Findings[i].Decision != nil {
				run.Findings[i].Location = nil
			}
		}

		_, doc := renderSarif(t, run)

		for _, result := range doc.Runs[0].Results {
			if result.RuleID == "GOV-ADR-002" {
				loc := result.Locations[0].PhysicalLocation
				equal(t, loc.ArtifactLocation.URI, testutil.ADRPath)
				equal(t, loc.Region.StartLine, testutil.ADRStatusLine)
			}
		}
	})

	t.Run("the run properties hold the area and the decisions", func(t *testing.T) {
		t.Parallel()

		data, _ := renderSarif(t, testutil.ADRRun(true))

		var raw struct {
			Runs []struct {
				Properties struct {
					ADR struct {
						Verdict   string `json:"verdict"`
						Decisions []struct {
							ID            string `json:"id"`
							Applicability string `json:"applicability"`
						} `json:"decisions"`
					} `json:"adrGovernance"`
				} `json:"properties"`
			} `json:"runs"`
		}

		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}

		area := raw.Runs[0].Properties.ADR
		equal(t, area.Verdict, "violation")
		equal(t, len(area.Decisions), 1)
		equal(t, area.Decisions[0].Applicability, "superseded")
	})

	t.Run("no register leaves the SARIF without the property", func(t *testing.T) {
		t.Parallel()

		data, _ := renderSarif(t, testutil.SampleRun())
		notContains(t, string(data), "adrGovernance")
	})
}
