package quality

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
)

// adrForm is the form of an `addressedBy` identifier. The canonical comparison with a registered decision
// (`ADR-0007` equals `ADR-7`) happens in ResolveLinks, where the register is known.
var adrForm = regexp.MustCompile(`^ADR-\d+$`)

// preCheck makes the two checks whose messages the schema library cannot word: a criterion that is not
// structured (free text alone, PRD QUAL-01), and an `addressedBy` identifier of the wrong form.
func preCheck(src sourceDoc, doc map[string]any) []*diag.Error {
	var errs []*diag.Error

	scenarios, _ := doc["scenarios"].([]any)

	for i, raw := range scenarios {
		if scenario, ok := raw.(map[string]any); ok {
			errs = append(errs, preCheckScenario(src, scenario, "/scenarios/"+strconv.Itoa(i))...)
		}
	}

	findings, _ := doc["findings"].([]any)

	for i, raw := range findings {
		if finding, ok := raw.(map[string]any); ok {
			errs = append(errs, checkADRForms(src, "finding "+stringOf(finding["id"])+": ",
				finding["addressedBy"], "/findings/"+strconv.Itoa(i)+"/addressedBy")...)
		}
	}

	return errs
}

// preCheckScenario checks the criterion and the ADR identifiers of the scenario at the pointer.
func preCheckScenario(src sourceDoc, scenario map[string]any, base string) []*diag.Error {
	name := "scenario " + stringOf(scenario["id"]) + ": "

	var errs []*diag.Error

	if crit, present := scenario["criterion"]; present {
		if _, isMap := crit.(map[string]any); !isMap {
			errs = append(errs, src.invalidAt(name+criterionHint(stringOf(scenario["mode"])), base+"/criterion"))
		}
	}

	return append(errs, checkADRForms(src, name, scenario["addressedBy"], base+"/addressedBy")...)
}

// criterionHint says what a free-text criterion must be replaced with.
func criterionHint(mode string) string {
	switch mode {
	case string(ModeMeasured):
		return "the criterion is not measurable: state a metric, a unit, a comparison (lt, lte, gt, gte, eq), and a threshold"
	case string(ModeStructural):
		return "the criterion is free text: name one or more policy rules"
	case string(ModeReviewed):
		return "the criterion is free text: name the assessment subject and the reviewer authority"
	default:
		return "the criterion is not measurable: state it as a mapping that its mode needs"
	}
}

func checkADRForms(src sourceDoc, name string, raw any, pointer string) []*diag.Error {
	list, _ := raw.([]any)

	var errs []*diag.Error

	for i, item := range list {
		id, isString := item.(string)
		if isString && adrForm.MatchString(id) {
			continue
		}

		at := pointer + "/" + strconv.Itoa(i)

		errs = append(errs, src.invalidAt(fmt.Sprintf("%saddressedBy %q is not of the form ADR-<number>", name, stringOf(item)), at))
	}

	return errs
}
