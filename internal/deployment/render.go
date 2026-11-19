package deployment

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
)

// DefaultListLimit caps a list in the text output: more items give "… and N more". The results file is never cut.
const DefaultListLimit = 20

// RenderOptions are the options of the text renderer.
type RenderOptions struct {
	Source       func(Location) []string
	Environments []string
	Limit        int
	// Verbose adds the text of each limit under its step, the obligation of each rule, and the consumer counts.
	Verbose bool
}

func (o *RenderOptions) limit() int {
	if o.Limit > 0 {
		return o.Limit
	}

	return DefaultListLimit
}

// Render writes the traces of one subject, one block for each environment of the trace (`concordia trace`). It
// says nothing about what runs: the level is `declared`, and every limit and gap shows. It reports whether the
// subject has a trace in the selected environments.
func Render(out io.Writer, trace *Trace, subject string, opts RenderOptions) (bool, error) {
	blocks := selectSubjectTraces(trace, subject, opts.Environments)

	for i := range blocks {
		if err := renderSubjectBlock(out, &blocks[i], i > 0, opts); err != nil {
			return true, err
		}
	}

	return len(blocks) > 0, nil
}

// subjectBlock is the trace of one subject in one environment.
type subjectBlock struct {
	env *EnvironmentTrace
	sub *SubjectTrace
}

// selectSubjectTraces gives the traces of the subject in the environments (all of them when environments is nil).
func selectSubjectTraces(trace *Trace, subject string, environments []string) []subjectBlock {
	var blocks []subjectBlock

	for i := range trace.Environments {
		env := &trace.Environments[i]
		if environments != nil && !slices.Contains(environments, env.Name) {
			continue
		}

		for j := range env.Subjects {
			if env.Subjects[j].ArchitectureID == subject {
				blocks = append(blocks, subjectBlock{env: env, sub: &env.Subjects[j]})
			}
		}
	}

	return blocks
}

// renderSubjectBlock writes the block, after an empty line when it follows another block.
func renderSubjectBlock(out io.Writer, block *subjectBlock, follows bool, opts RenderOptions) error {
	if follows {
		if _, err := fmt.Fprintln(out); err != nil {
			return fmt.Errorf("write trace: %w", err)
		}
	}

	return RenderSubject(out, block.env, block.sub, opts)
}

// RenderSubject writes the trace of one subject in one environment: the status, where the evidence comes from, the
// five steps with their evidence role and resolution, each entity named as a reader knows it with its locations, the
// gaps, the rendered or evaluated evidence, the limits, and the rules. `explain` prints the same block as the excerpt
// of a finding subject.
func RenderSubject(out io.Writer, env *EnvironmentTrace, sub *SubjectTrace, opts RenderOptions) error {
	var text strings.Builder

	cluster := ""
	if env.Cluster != "" {
		cluster = " (cluster " + env.Cluster + ")"
	}

	fmt.Fprintf(&text, "Deployment trace: %s in %s%s: %s\n", sub.ArchitectureID, env.Name, cluster, sub.Status)
	text.WriteString(env.levelSentence() + "\n")
	fmt.Fprintf(&text, "Workloads of %s: %s\n", env.Name, env.Coverage.Status)

	limits := renderSteps(&text, sub.Steps, &opts)

	renderEvaluated(&text, env, sub, &opts)

	if len(limits) > 0 && !opts.Verbose {
		fmt.Fprintf(&text, "Limits: %s (--verbose explains each)\n", strings.Join(limits, ", "))
	}

	renderRules(&text, sub.Rules, opts.Verbose)

	if _, err := io.WriteString(out, text.String()); err != nil {
		return fmt.Errorf("write trace: %w", err)
	}

	return nil
}

// renderSteps writes the numbered steps and gives the IDs of their limits, in the order of first appearance.
func renderSteps(text *strings.Builder, steps []StepTrace, opts *RenderOptions) []string {
	var limits []string

	for i := range steps {
		renderStep(text, i+1, &steps[i], opts)

		for _, item := range steps[i].Limits {
			limits = appendLimitID(limits, item.ID)
		}
	}

	return limits
}

func appendLimitID(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}

	return append(ids, id)
}

func renderRules(text *strings.Builder, rules []RuleLink, verbose bool) {
	if len(rules) == 0 {
		return
	}

	text.WriteString("Rules:\n")

	for _, rule := range rules {
		if verbose {
			fmt.Fprintf(text, "  %s %s (obligation %s)\n", rule.RuleID, rule.Verdict, rule.ObligationID)
		} else {
			fmt.Fprintf(text, "  %s %s\n", rule.RuleID, rule.Verdict)
		}
	}
}

func words(identifier string) string { return strings.ReplaceAll(identifier, "-", " ") }

func renderStep(text *strings.Builder, number int, step *StepTrace, opts *RenderOptions) {
	fmt.Fprintf(text, "%d. %s [%s, %s]\n", number, words(string(step.Step)), words(string(step.Role)), words(string(step.Resolution)))

	renderEntityList(text, step.Entities, opts)

	if len(step.Entities) == 0 {
		text.WriteString("   nothing found\n")
	}

	renderGaps(text, step.Gaps)

	if opts.Verbose {
		for _, limitItem := range step.Limits {
			fmt.Fprintf(text, "   limit %s: %s\n", limitItem.ID, limitItem.Detail)
		}
	}
}

// renderEntityList writes the entities up to the list limit, then the count of those left out.
func renderEntityList(text *strings.Builder, entities []Entity, opts *RenderOptions) {
	limit := opts.limit()

	for i := range entities {
		if i == limit {
			fmt.Fprintf(text, "   … and %d more\n", len(entities)-limit)

			return
		}

		renderEntity(text, &entities[i], opts)
	}
}

func renderGaps(text *strings.Builder, gaps []Gap) {
	for i := range gaps {
		reason := string(gaps[i].Reason)
		if len(gaps[i].LossModes) > 0 {
			reason = strings.Join(gaps[i].LossModes, ", ")
		}

		fmt.Fprintf(text, "   gap: %s (%s)\n", gaps[i].Detail, reason)
	}
}

// entityText names an entity as a reader knows it: `Deployment commerce/billing`, `container app:
// registry.example.com/commerce/billing:1.4.0`, `namespace commerce`. The identity stays in results.json.
func entityText(entity *Entity) string {
	if text, ok := knownEntityText(entity); ok {
		return text
	}

	return words(entity.Kind) + " " + entity.Identity
}

// knownEntityText names an entity of a kind that has its own wording; not ok when the kind has none or the entity lacks
// the properties of the wording.
func knownEntityText(entity *Entity) (string, bool) {
	props := entity.Properties

	switch entity.Kind {
	case KindWorkload, KindManifest, KindRenderedWorkload:
		return workloadEntityText(props)
	case KindHostingTarget:
		return hostingTargetEntityText(props)
	case KindContainerImage:
		return containerEntityText(props)
	case KindUnlinkedImage:
		return "image " + cmp.Or(props[propRepository], entity.Identity) + ": no build definition names it", true
	case KindBaseImage:
		return baseImageEntityText(props)
	case KindDockerfile:
		return "Dockerfile " + entity.Identity, true
	case KindSourcePath:
		return "source " + entity.Identity, true
	case KindService:
		return entity.Identity, true
	}

	return "", false
}

func workloadEntityText(props map[string]string) (string, bool) {
	if props[propKind] == "" || props[propName] == "" {
		return "", false
	}

	if props[propNamespace] != "" {
		return props[propKind] + " " + props[propNamespace] + "/" + props[propName], true
	}

	return props[propKind] + " " + props[propName], true
}

func hostingTargetEntityText(props map[string]string) (string, bool) {
	if props[propName] == "" {
		return "", false
	}

	return strings.ToLower(cmp.Or(props[propKind], "namespace")) + " " + props[propName], true
}

func containerEntityText(props map[string]string) (string, bool) {
	if props[propContainer] == "" {
		return "", false
	}

	return "container " + props[propContainer] + ": " + cmp.Or(props[propReference], props[propRepository]), true
}

func baseImageEntityText(props map[string]string) (string, bool) {
	if props[propReference] == "" {
		return "", false
	}

	return "base image " + props[propReference], true
}

func renderEntity(text *strings.Builder, entity *Entity, opts *RenderOptions) {
	text.WriteString(entityLine(entity, opts) + "\n")

	if opts.Source != nil {
		renderSourceLines(text, entity.Locations, opts.Source)
	}
}

// entityLine is the line of an entity: its name, lineage, consumers, locations, and bindings.
func entityLine(entity *Entity, opts *RenderOptions) string {
	line := "   " + entityText(entity)

	if entity.Lineage != "" {
		line += ", lineage " + string(entity.Lineage)
	}

	if entity.Consumers > 0 && opts.Verbose {
		line += fmt.Sprintf(", used by %d workload(s)", entity.Consumers)
	}

	if places := entityPlaces(entity); len(places) > 0 {
		line += "  at " + strings.Join(places, ", ")
	}

	if len(entity.Bindings) > 0 {
		line += "  (bound by " + strings.Join(entity.Bindings, ", ") + ")"
	}

	return line
}

// entityPlaces gives the locations of an entity as text. A source path is its own location: naming it twice says nothing.
func entityPlaces(entity *Entity) []string {
	places := make([]string, 0, len(entity.Locations))

	for _, loc := range entity.Locations {
		if place := locationText([]Location{loc}); place != entity.Identity {
			places = append(places, place)
		}
	}

	return places
}

// renderSourceLines writes the source lines of the locations that have a line.
func renderSourceLines(text *strings.Builder, locations []Location, source func(Location) []string) {
	for _, loc := range locations {
		if loc.Line == 0 {
			continue
		}

		for _, line := range source(loc) {
			fmt.Fprintf(text, "     | %s\n", line)
		}
	}
}

// levelSentence says where the trace of the environment comes from: always the declared configuration, and the
// level `rendered-or-evaluated` only when the environment has rendered sources or plans. The execution and runtime
// levels have no trace step (records and observations are judged through claims).
func (e *EnvironmentTrace) levelSentence() string {
	const notTraced = " Not traced: deployment execution and runtime."

	switch {
	case e.Evaluated.Evaluated():
		return "Evidence: declared configuration, and rendered or evaluated from " + sourceCount(len(e.Evaluated.Sources)) +
			" (below)." + notTraced
	case e.Evaluated != nil:
		return "Evidence: declared configuration only; no rendered source or plan of " + e.Name + " holds evidence." + notTraced
	}

	return "Evidence: declared configuration only." + notTraced
}

// LevelsSentence is the evidence line of a whole trace: the declared level always, and the environments in which the
// level `rendered-or-evaluated` is evaluated.
func (t *Trace) LevelsSentence() string {
	var evaluated []string

	for i := range t.Environments {
		if t.Environments[i].Evaluated.Evaluated() {
			evaluated = append(evaluated, t.Environments[i].Name)
		}
	}

	const notTraced = " Not traced: deployment execution and runtime."

	if len(evaluated) == 0 {
		return "Evidence: declared configuration only." + notTraced
	}

	return "Evidence: declared configuration, and rendered or evaluated in " + strings.Join(evaluated, ", ") + "." + notTraced
}

func sourceCount(n int) string {
	if n == 1 {
		return "1 source"
	}

	return fmt.Sprintf("%d sources", n)
}

// renderEvaluated writes the level `rendered-or-evaluated` of the environment: each rendered source or plan with its
// coverage, then the rendered workloads and planned resources that a binding maps to the subject. Nothing is written
// for an environment without rendered sources or plans, so such a trace reads as before.
func renderEvaluated(text *strings.Builder, env *EnvironmentTrace, sub *SubjectTrace, opts *RenderOptions) {
	if env.Evaluated == nil {
		return
	}

	fmt.Fprintf(text, "Rendered or evaluated: %s\n", words(env.Evaluated.Status))

	for _, source := range env.Evaluated.Sources {
		fmt.Fprintf(text, "   %s %s %s: %s\n", source.Kind, source.Tool, source.Path, source.Coverage)
	}

	if len(sub.Evaluated) == 0 {
		text.WriteString("   no rendered or planned entity\n")

		return
	}

	renderEntityList(text, sub.Evaluated, opts)
}
