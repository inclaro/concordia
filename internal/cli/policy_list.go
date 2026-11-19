package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

type ruleRow struct {
	ID       string              `json:"id"`
	Severity policy.RuleSeverity `json:"severity"`
	Name     string              `json:"name"`
	// Enabled is false when a workspace override drops the rule.
	Enabled bool `json:"enabled"`
}

type builtinRow struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
	// Note says that the pack is not listed in policies.packs: it applies by itself.
	Note      string    `json:"note,omitempty"`
	Rules     []ruleRow `json:"rules,omitempty"`
	RuleCount int       `json:"ruleCount"`
}

type configuredRow struct {
	Source  string `json:"source"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	// Digest is the digest of the pack text. It is empty when the pack did not load.
	Digest string `json:"digest,omitempty"`
	// Status is builtin, verified (a file whose digest agrees with its pin), or the problem: mismatch, missing, unpinned, remote, unknown, invalid.
	Status    string    `json:"status"`
	Problem   string    `json:"problem,omitempty"`
	Rules     []ruleRow `json:"rules,omitempty"`
	RuleCount int       `json:"ruleCount"`
	Overrides int       `json:"overrides"`
}

type policyListing struct {
	Builtin    []builtinRow    `json:"builtin"`
	Configured []configuredRow `json:"configured"`
}

// statusOfProblem maps a pack diagnostic to the status of a configured pack.
func statusOfProblem(code string) string {
	switch code {
	case diag.POL010:
		return packMismatch
	case diag.POL011:
		return packMissing
	case diag.POL012:
		return packUnknown
	case diag.POL013:
		return packUnpinned
	case diag.POL014:
		return packRemote
	default:
		return packInvalid
	}
}

func rowsOf(info *policy.PackInfo, overrides map[string]workspace.RuleOverride) []ruleRow {
	rows := make([]ruleRow, len(info.Rules))

	for i, rule := range info.Rules {
		row := ruleRow{ID: rule.ID, Severity: rule.Severity, Name: rule.Name, Enabled: true}

		if override, ok := overrides[rule.ID]; ok {
			if override.Severity != "" {
				row.Severity = policy.RuleSeverity(override.Severity)
			}

			row.Enabled = override.Enabled == nil || *override.Enabled
		}

		rows[i] = row
	}

	return rows
}

func builtinRows(withRules bool) []builtinRow {
	rows := make([]builtinRow, 0, len(packs.Names()))

	for _, name := range packs.Names() {
		text, _ := packs.Source(name)
		info, problems := policy.DescribePack(policy.PolicyFile{Path: packs.Prefix + name, Text: string(text)})

		row := builtinRow{Name: name, ID: info.ID, Version: info.Version, RuleCount: len(info.Rules), Digest: digest.OfBytes(text)}
		if name == views.PackID {
			row.Note = viewsPackNote
		}

		if len(problems) > 0 {
			row.ID, row.Version = name, ""
		}

		if withRules {
			row.Rules = rowsOf(&info, nil)
		}

		rows = append(rows, row)
	}

	return rows
}

// configuredRowOf loads one `policies.packs` entry and describes it. The load
// problem is the status of the row: `policy list` never fails for a bad pack.
func configuredRowOf(root string, use *workspace.PolicyPackUse, withRules bool) configuredRow {
	row := configuredRow{Source: use.Use, Overrides: len(use.Rules)}

	files, problems := policy.ResolvePacks(root, []workspace.PolicyPackUse{*use})
	if len(problems) > 0 {
		row.Status, row.Problem = statusOfProblem(problems[0].Code), problems[0].Message

		return row
	}

	file := files[0]
	info, issues := policy.DescribePack(file)

	if len(issues) > 0 {
		row.Status, row.Problem = packInvalid, issues[0].Message

		return row
	}

	row.ID, row.Version, row.RuleCount = info.ID, info.Version, len(info.Rules)
	row.Digest = digest.OfBytes([]byte(file.Text))
	row.Status = packVerified

	if strings.HasPrefix(use.Use, packs.Prefix) {
		row.Status = packBuiltin
	}

	if overrideProblems := policy.OverrideProblems(file, info); len(overrideProblems) > 0 {
		row.Status, row.Problem = packInvalid, overrideProblems[0].Message
	}

	if withRules {
		row.Rules = rowsOf(&info, use.Rules)
	}

	return row
}

// configuredRows describes the packs of the workspace. A directory without a workspace file has none.
func configuredRows(app *Context, withRules bool) ([]configuredRow, error) {
	rows := []configuredRow{}

	wsp, err := app.LoadWorkspace()
	if err != nil {
		if isMissingWorkspace(err) {
			return rows, nil
		}

		return nil, err
	}

	for i := range wsp.Policies.Packs {
		rows = append(rows, configuredRowOf(wsp.Root, &wsp.Policies.Packs[i], withRules))
	}

	return rows, nil
}

// isMissingWorkspace says whether an error is that the workspace file does not exist.
func isMissingWorkspace(err error) bool {
	var failure *diag.Error

	return errors.As(err, &failure) && failure.Diagnostic.Code == diag.WRK001
}

func policyList(app *Context, opts *policyListOptions) error {
	configured, err := configuredRows(app, opts.rules)
	if err != nil {
		return err
	}

	listing := policyListing{Builtin: builtinRows(opts.rules), Configured: configured}
	text := prettyListing(&listing)

	if app.Opts.Format == jsonFormat {
		data, marshalErr := canonjson.Marshal(listing)
		if marshalErr != nil {
			return fmt.Errorf("encode pack list: %w", marshalErr)
		}

		text = string(data)
	}

	return app.WriteOutput(text, "")
}

func writeRules(out *strings.Builder, rules []ruleRow) {
	for _, rule := range rules {
		line := fmt.Sprintf("    %-14s %-8s %s", rule.ID, rule.Severity, rule.Name)
		if !rule.Enabled {
			line += " (disabled)"
		}

		out.WriteString(line + "\n")
	}
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}

	return strconv.Itoa(count) + " " + noun + "s"
}

func prettyListing(listing *policyListing) string {
	var out strings.Builder

	out.WriteString("Built-in packs:\n")

	for i := range listing.Builtin {
		writeBuiltinRow(&out, &listing.Builtin[i])
	}

	out.WriteString("\nPacks of the workspace:\n")

	if len(listing.Configured) == 0 {
		out.WriteString("  none\n")
	}

	for i := range listing.Configured {
		writeConfiguredRow(&out, &listing.Configured[i])
	}

	return out.String()
}

func writeBuiltinRow(out *strings.Builder, row *builtinRow) {
	fmt.Fprintf(out, "  %s%s  %s  %s\n", packs.Prefix, row.Name, row.Version, plural(row.RuleCount, "rule"))

	if row.Note != "" {
		out.WriteString("    " + row.Note + "\n")
	}

	writeRules(out, row.Rules)
}

func writeConfiguredRow(out *strings.Builder, row *configuredRow) {
	identity := "-"
	if row.ID != "" {
		identity = row.ID + "@" + row.Version
	}

	fmt.Fprintf(out, "  %s  %s  %s  %s", row.Source, identity, plural(row.RuleCount, "rule"), row.Status)

	if row.Digest != "" {
		out.WriteString("  " + row.Digest)
	}

	out.WriteString("\n")

	if row.Problem != "" {
		out.WriteString("    problem: " + row.Problem + "\n")
	}

	writeRules(out, row.Rules)
}
