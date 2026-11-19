package reporting

import (
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
)

// diagramCurrencyTitle is the heading of the `diagramCurrency` area in the pretty report. The short title of the
// area (Area.Title) stays in the `Not configured:` line and the unknown-area line.
const diagramCurrencyTitle = "Diagram presence, freshness, and correspondence"

// relatedKindView is the kind of the `related` entry of a view finding.
const relatedKindView = "view"

// viewCheck is one of the three checks of a view and the built-in rule that reports it.
type viewCheck struct {
	// verdict gives the verdict of the check of one view.
	verdict func(*conformance.ViewResult) conformance.RuleVerdict
	// reasons gives the plain reasons of the check of one view.
	reasons func(*conformance.ViewResult) []string
	// name is the check: `presence`, `freshness` or `correspondence`.
	name string
	// ruleID is the rule that carries the findings of the check.
	ruleID string
	// title names the rule when the run does not hold its result.
	title string
}

// viewChecks are the checks of a view in report order.
var viewChecks = []viewCheck{
	{
		name: "presence", ruleID: "VIEW-PRES-001", title: "View is present",
		verdict: func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Presence.Status },
		reasons: func(v *conformance.ViewResult) []string { return v.Presence.Reasons },
	},
	{
		name: "freshness", ruleID: "VIEW-FRESH-001", title: "View is fresh",
		verdict: func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Freshness.Status },
		reasons: func(v *conformance.ViewResult) []string { return v.Freshness.Reasons },
	},
	{
		name: "correspondence", ruleID: "VIEW-CORR-001", title: "View corresponds to the architecture",
		verdict: func(v *conformance.ViewResult) conformance.RuleVerdict { return v.Correspondence.Status },
		reasons: func(v *conformance.ViewResult) []string { return v.Correspondence.Reasons },
	},
}

// viewIDOf gives the ID of the view that a finding relates to, or "" for a finding of another kind.
func viewIDOf(finding *conformance.Finding) string {
	for _, ref := range finding.Related {
		if ref.Kind == relatedKindView {
			return ref.ID
		}
	}

	return ""
}

// assessmentState gives the state of the assessment-currency entry of a view.
func assessmentState(view *conformance.ViewResult) string {
	if view.Assessment.State == "" {
		return conformance.AssessmentCurrencyNotEvaluated
	}

	return view.Assessment.State
}

// viewRow is the row of one view in the pretty report: the four check verdicts, the correspondence method, and the
// next-action category when a check does not pass. Every verdict is a word.
func viewRow(view *conformance.ViewResult) string {
	var row strings.Builder

	row.WriteString("  " + view.ID + " (" + view.Kind + ", " + view.Origin + "): ")
	row.WriteString("presence: " + string(view.Presence.Status))
	row.WriteString(", freshness: " + string(view.Freshness.Status))
	row.WriteString(", correspondence: " + string(view.Correspondence.Status))

	if view.Correspondence.Method != "" {
		row.WriteString(" (method: " + view.Correspondence.Method + ")")
	}

	row.WriteString(", assessment currency: " + assessmentState(view))

	if view.NextAction != "" {
		row.WriteString("; next action: " + view.NextAction)
	}

	return row.String()
}

// viewRows are the rows of the views of a run, sorted by view ID as the run holds them.
func viewRows(run *conformance.Run) []string {
	rows := make([]string, 0, len(run.Views))

	for i := range run.Views {
		rows = append(rows, viewRow(&run.Views[i]))
	}

	return sanitized(rows)
}

// viewsProperty is the view area in the SARIF run properties: the verdict of the area and, for each view, its four
// check verdicts, its correspondence method, and its next-action category. A SARIF viewer shows it beside the results.
func viewsProperty(run *conformance.Run) obj {
	area := run.Areas.Get(conformance.AreaDiagramCurrency)
	views := make([]any, 0, len(run.Views))

	for i := range run.Views {
		view := &run.Views[i]
		entry := obj{
			keyID: view.ID, keyKind: view.Kind, "origin": view.Origin,
			keyPresence: view.Presence.Status, "freshness": view.Freshness.Status, "correspondence": view.Correspondence.Status,
			"assessmentCurrency": assessmentState(view),
		}

		if view.Correspondence.Method != "" {
			entry["correspondenceMethod"] = view.Correspondence.Method
		}

		if view.NextAction != "" {
			entry["nextAction"] = view.NextAction
		}

		views = append(views, entry)
	}

	return obj{"verdict": area.Verdict, "views": views}
}
