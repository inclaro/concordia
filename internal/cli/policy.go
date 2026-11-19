package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

const (
	cmdVerify    = "verify"
	vendorDir    = "policies/vendor"
	vendorSuffix = ".policy.yaml"
	httpsPrefix  = "https://"
	// maxPackBytes is the size limit of a fetched pack.
	maxPackBytes = 1 << 20

	packBuiltin   = "builtin"
	viewsPackNote = "the views pack applies automatically when views are declared in the workspace; it is not listed in policies.packs"
	packVerified  = "verified"
	packMismatch  = "mismatch"
	packMissing   = "missing"
	packUnpinned  = "unpinned"
	packRemote    = "remote"
	packUnknown   = "unknown"
	packInvalid   = "invalid"

	jsonFormat = "json"
	keyID      = "id"
)

// fileNamePart is a pack id or version that is safe in the name of the vendored file.
var fileNamePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

type policyListOptions struct {
	rules bool
}

type policyAddOptions struct {
	id    string
	force bool
}

func newPolicyCmd(host *Host) *cobra.Command {
	group := &cobra.Command{Use: "policy", Short: "Manage policy packs"}

	var listOpts policyListOptions

	list := &cobra.Command{
		Use:   "list",
		Short: "List the built-in packs and the packs of the workspace",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(_ context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, policyList(app, &listOpts)
		}),
	}
	list.Flags().BoolVar(&listOpts.rules, "rules", false, "also list the rules of each pack")

	var addOpts policyAddOptions

	add := &cobra.Command{
		Use:   "add <source>",
		Short: "Add a built-in pack, or vendor a pack file or https URL, to the workspace",
		Args:  cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, policyAdd(ctx, app, args[0], &addOpts)
		}),
	}
	add.Flags().StringVar(&addOpts.id, "id", "", "name of the vendored file (default: metadata.id of the pack)")
	add.Flags().BoolVar(&addOpts.force, "force", false, "replace an existing entry and its vendored file")

	verify := &cobra.Command{
		Use:   cmdVerify,
		Short: "Check the digests of the packs and that each rule override names a rule of its pack",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(_ context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, policyVerify(app)
		}),
	}

	group.AddCommand(list, add, verify)

	return group
}

func usageError(detail string) *diag.Error {
	return diag.Fail(diag.WRK010, diag.Detail(detail))
}

type verifyResult struct {
	Status string `json:"status"`
	// TypedRules show the declaration of each rule with an obligation block, in the order of the packs.
	TypedRules []typedRuleRow `json:"typedRules,omitempty"`
	Packs      int            `json:"packs"`
	Rules      int            `json:"rules"`
}

// typedRuleRow is the declaration of one typed rule.
type typedRuleRow struct {
	Obligation  *policy.ObligationSpec `json:"obligation"`
	ID          string                 `json:"id"`
	Family      string                 `json:"family,omitempty"`
	Enforcement policy.Enforcement     `json:"enforcement,omitempty"`
}

// line is the text of the row: the rule, its family and enforcement, and the declaration.
func (r *typedRuleRow) line() string {
	head := "  " + r.ID

	if r.Family != "" {
		head += " family=" + r.Family
	}

	if r.Enforcement != "" {
		head += " enforcement=" + string(r.Enforcement)
	}

	return head + ": " + r.Obligation.Declaration() + "\n"
}

// verifyTally collects what `policy verify` finds in the policy files: the problems, the counts, and the typed rules.
type verifyTally struct {
	// declared maps a rule ID to the file that declares it first.
	declared map[string]string
	problems []diag.Diagnostic
	typed    []typedRuleRow
	packs    int
	rules    int
}

// addFile describes one policy file and adds its problems, its pack, and its rules.
func (t *verifyTally) addFile(file *policy.PolicyFile) {
	info, issues := policy.DescribePack(*file)
	if len(issues) > 0 {
		t.problems = append(t.problems, issues...)

		return
	}

	if file.Use != "" {
		t.packs++
	}

	t.problems = append(t.problems, policy.OverrideProblems(*file, info)...)

	for i := range info.Rules {
		t.addRule(file.Path, &info.Rules[i])
	}
}

// addRule counts a rule, or says that another file declared it first.
func (t *verifyTally) addRule(file string, rule *policy.RuleInfo) {
	if prior, dup := t.declared[rule.ID]; dup {
		t.problems = append(t.problems, diag.New(diag.POL003, diag.Params{keyID: rule.ID},
			diag.WithSource(diag.Source{URI: file}), diag.WithSuggestion("first declared in "+prior)))

		return
	}

	t.declared[rule.ID] = file
	t.rules++

	if rule.Obligation != nil {
		t.typed = append(t.typed, typedRuleRow{ID: rule.ID, Family: rule.Family, Enforcement: rule.Enforcement, Obligation: rule.Obligation})
	}
}

// write reports the verified policy as text or JSON.
func (t *verifyTally) write(app *Context) error {
	if app.Opts.Format == jsonFormat {
		data, err := canonjson.Marshal(verifyResult{Status: "ok", Packs: t.packs, Rules: t.rules, TypedRules: t.typed})
		if err != nil {
			return fmt.Errorf("encode result: %w", err)
		}

		return app.WriteOutput(string(data), "")
	}

	var text strings.Builder

	fmt.Fprintf(&text, "policy verified: %s, %s\n", plural(t.packs, "pack"), plural(t.rules, "rule"))

	for i := range t.typed {
		text.WriteString(t.typed[i].line())
	}

	return app.WriteOutput(text.String(), "")
}

func policyVerify(app *Context) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	files, problems, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	tally := &verifyTally{declared: map[string]string{}, problems: problems}
	for i := range files {
		tally.addFile(&files[i])
	}

	if err := policy.ThrowOnErrors(tally.problems); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return tally.write(app)
}
