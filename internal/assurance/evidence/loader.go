package evidence

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

// paramPath is the parameter of the messages of CO-PRV-002 and CO-PRV-011.
const paramPath = "path"

// loader is the state of one Load.
type loader struct {
	ev       *Evidence
	opts     *Options
	cfg      *workspace.Assurance
	getenv   func(string) string
	run      render.Runner
	verifier *statement.Verifier
	// abs holds the resolved OS path of each statement file that was read, by workspace-relative path: cosign needs a
	// file path.
	abs         map[string]string
	trustedRoot string
	diags       []diag.Diagnostic
	cosignUsed  bool
}

// predicateTypes are the predicate types that each evidence kind accepts.
var predicateTypes = map[string]string{
	KindProvenance:  statement.PredicateSLSAProvenance,
	KindDeployment:  statement.PredicateDeploymentRecord,
	KindObservation: statement.PredicateWorkloadObservation,
	KindCheckResult: statement.PredicateCheckResult,
	KindMeasurement: statement.PredicateMeasurement,
	KindPlan:        statement.PredicatePlan,
}

// configure checks everything that is a configuration error before a process starts: the scenario files and the
// trusted root. Every scenario error is reported, not only the first.
func (l *loader) configure() error {
	var errs []*diag.Error

	if q := l.cfg.Quality; q != nil && !l.opts.DeferScenarios {
		set, qerrs := quality.Load(l.opts.Root, q.Scenarios, l.opts.Known)
		errs = append(errs, qerrs...)
		l.ev.Scenarios = set
	}

	if v := l.cfg.Verification; v != nil && v.TrustedRoot != "" {
		abs, err := fsutil.Confine(l.opts.Root, trimDot(v.TrustedRoot))

		switch {
		case errors.Is(err, fsutil.ErrOutsideRoot):
			errs = append(errs, diag.Fail(diag.PRV002, diag.Params{paramPath: v.TrustedRoot}))
		case err != nil:
			errs = append(errs, diag.Fail(diag.PRV001, diag.Detail("assurance.verification.trustedRoot "+v.TrustedRoot+" cannot be read: "+err.Error()),
				diag.WithSource(diag.Source{URI: v.TrustedRoot})))
		default:
			l.trustedRoot = abs
		}
	}

	if len(errs) > 0 {
		return &ConfigError{Errors: errs}
	}

	l.abs = map[string]string{}

	return nil
}

// loadRenders renders every declared source. A source that fails is a failed result and a diagnostic; the sources of
// the other environments are rendered all the same. Only a toolchain error or a canceled context stops the loop.
func (l *loader) loadRenders(ctx context.Context) error {
	cfg := l.cfg.Rendering
	if cfg == nil {
		return nil
	}

	sources := slices.Clone(cfg.Sources)
	slices.SortStableFunc(sources, func(a, b workspace.RenderSource) int {
		return compareKeys([]string{a.Environment, a.Path, a.Tool}, []string{b.Environment, b.Path, b.Tool})
	})

	limits := render.LimitsFrom(cfg.Limits)

	for i := range sources {
		if err := ctx.Err(); err != nil {
			return err //nolint:wrapcheck // the CLI maps the context error to exit 130.
		}

		renderOpts := &render.Options{
			Pins: l.opts.Pins, Run: l.opts.Run, Locate: l.opts.Locate, Root: l.opts.Root,
			Cluster: l.opts.Environments[sources[i].Environment], Source: sources[i], Limits: limits,
			RequireOsSandbox: cfg.RequireOsSandbox,
		}

		res, err := render.Render(ctx, renderOpts)
		if err != nil {
			return err //nolint:wrapcheck // a toolchain *diag.Error or the context error, both for the caller as they are.
		}

		if l.opts.RenderTwice {
			again, err := render.Render(ctx, renderOpts)
			if err != nil {
				return err //nolint:wrapcheck // as above.
			}

			l.diags = append(l.diags, render.CompareRenders(res, again)...)
		}

		l.diags = append(l.diags, res.Diagnostics...)
		l.ev.Renders = append(l.ev.Renders, res)

		if res.Renderer != nil {
			l.ev.Toolchain.Renderers = append(l.ev.Toolchain.Renderers, *res.Renderer)
		}
	}

	return nil
}

// readFile reads a statement file below the root and remembers its resolved path for cosign.
func (l *loader) readFile(rel string) ([]byte, error) {
	abs, err := fsutil.Confine(l.opts.Root, rel)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // Confine resolved the path inside the root.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}

	l.abs[rel] = abs

	return data, nil
}

// readDeclared reads a file that the workspace names directly (a plan, a binding statement). A path outside the
// root is a configuration error; a file that is missing is for the caller to report.
func (l *loader) readDeclared(rel string) ([]byte, error) {
	data, err := l.readFile(trimDot(rel))
	if errors.Is(err, fsutil.ErrOutsideRoot) {
		return nil, diag.Fail(diag.PRV002, diag.Params{paramPath: rel})
	}

	return data, err
}

// verified is one statement file after parsing and signature verification.
type verified struct {
	verification statement.Verification
	loaded       statement.Loaded
	persisted    statement.Persisted
}

// verifyAll reads the statement files, drops those that are malformed or of the wrong predicate type for the kind
// (a CO-PRV-009 warning each), verifies the signature of the others, and persists their redacted form. A cosign that
// is needed and missing is the only error.
func (l *loader) verifyAll(ctx context.Context, kind string, paths []string) ([]verified, error) {
	loaded, diags := statement.LoadAll(paths, l.readFile)
	l.diags = append(l.diags, diags...)

	out := make([]verified, 0, len(loaded))

	for i := range loaded {
		if err := ctx.Err(); err != nil {
			return nil, err //nolint:wrapcheck // the CLI maps the context error to exit 130.
		}

		item := &loaded[i]

		if want := predicateTypes[kind]; item.Statement.PredicateType != want {
			bad := &statement.MalformedError{Path: item.Path, Reason: fmt.Sprintf("the predicate type is %q, want %q for %s", item.Statement.PredicateType, want, kind)}
			l.diags = append(l.diags, bad.Diagnostic())

			continue
		}

		ver, err := l.verifier.Verify(ctx, &statement.VerifyInput{
			Path: l.abs[item.Path], Root: l.opts.Root, TrustedRoot: l.trustedRoot, Loaded: *item,
			Producers: l.cfg.Producers, Offline: l.opts.Context.Offline,
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // the CO-WRK-014 *diag.Error of a missing cosign, for the caller as it is.
		}

		if ver.Signature == statement.SignatureVerified || ver.Signature == statement.SignatureFailed {
			l.cosignUsed = true
		}

		l.diags = append(l.diags, ver.Diagnostics(item.Path)...)

		persisted, err := statement.Persist(item, redact.Default())
		if err != nil {
			bad := &statement.MalformedError{Path: item.Path, Reason: err.Error()}
			l.diags = append(l.diags, bad.Diagnostic())

			continue
		}

		out = append(out, verified{loaded: *item, verification: ver, persisted: persisted})
	}

	return out, nil
}

// add records a statement in the lists that every kind shares.
func (l *loader) add(kind string, item *verified, admission assurance.Admission) Statement {
	st := Statement{
		Kind: kind, Loaded: item.loaded, Verification: item.verification, Admission: admission, Persisted: item.persisted,
	}

	l.ev.Persisted = append(l.ev.Persisted, item.persisted)
	l.ev.Statements = append(l.ev.Statements, summarize(&st))

	return st
}

// summarize builds the results line of a statement.
func summarize(st *Statement) StatementSummary {
	subjects := st.Loaded.Statement.Digests()
	if subjects == nil {
		subjects = []string{}
	}

	return StatementSummary{
		Kind: st.Kind, Path: st.Loaded.Path, Digest: st.Loaded.Digest, SubjectDigests: subjects,
		Producer: st.Verification.ProducerID, Integrity: st.Admission.Integrity, Signature: st.Verification.Signature,
		Details: st.Admission.Details, Admissible: st.Admission.Admissible,
	}
}

// inadmissible adds the CO-PRV-010 info of a statement that cannot support a claim.
func (l *loader) inadmissible(path string, admission assurance.Admission) {
	if admission.Admissible {
		return
	}

	reasons := make([]string, len(admission.Details))
	for i, d := range admission.Details {
		reasons[i] = string(d)
	}

	l.diags = append(l.diags, diag.New(diag.PRV010, diag.Detail(path+": "+strings.Join(reasons, ", ")),
		diag.WithSource(diag.Source{URI: path})))
}

// context is the evaluation context that statements are admitted against: the environment is left to the statement
// itself, so that no environment of the run decides the admission of a statement of another.
func (l *loader) context() assurance.Context {
	ctx := l.opts.Context
	ctx.Environment = ""

	return ctx
}

func maxAge(days int) time.Duration { return verdict.MaxAge(days) }

// expandGlobs expands every declared glob once and records each with its files. A glob that matches no file is a
// CO-PRV-011 warning.
func (l *loader) expandGlobs() (map[string][]string, error) {
	var declared []Glob

	collect := func(kind string, patterns []string) {
		for _, p := range patterns {
			declared = append(declared, Glob{Kind: kind, Pattern: p})
		}
	}

	if p := l.cfg.Provenance; p != nil {
		collect(KindProvenance, p.Statements)
	}

	if r := l.cfg.Records; r != nil {
		collect(KindDeployment, r.Deployments)
		collect(KindObservation, r.Observations)
	}

	if v := l.cfg.Verification; v != nil {
		collect(KindCheckResult, v.Results)
	}

	if q := l.cfg.Quality; q != nil {
		collect(KindMeasurement, q.Measurements)
	}

	paths := map[string][]string{}
	if len(declared) == 0 {
		return paths, nil
	}

	patterns := make([]string, len(declared))
	for i := range declared {
		patterns[i] = trimDot(declared[i].Pattern)
	}

	files, err := binding.ExpandFileGlobs(l.opts.Root, patterns)
	if err != nil {
		return nil, fmt.Errorf("expand evidence globs: %w", err)
	}

	for i := range declared {
		match := fsutil.GlobMatcher([]string{declared[i].Pattern})

		declared[i].Paths = []string{}

		for _, f := range files {
			if match(f) {
				declared[i].Paths = append(declared[i].Paths, f)
			}
		}

		if len(declared[i].Paths) == 0 {
			l.diags = append(l.diags, diag.New(diag.PRV011, diag.Params{paramPath: declared[i].Pattern},
				diag.WithSource(diag.Source{URI: declared[i].Pattern})))
		}

		paths[declared[i].Kind] = append(paths[declared[i].Kind], declared[i].Paths...)
	}

	for kind, list := range paths {
		slices.SortFunc(list, canonjson.CompareUTF16)
		paths[kind] = slices.Compact(list)
	}

	l.ev.Globs = declared

	return paths, nil
}

// loadStatements loads the statements of every declared glob.
func (l *loader) loadStatements(ctx context.Context) error {
	paths, err := l.expandGlobs()
	if err != nil {
		return err
	}

	provenanceAge, recordAge, checkAge := 0, 0, 0

	if l.cfg.Provenance != nil {
		provenanceAge = l.cfg.Provenance.MaxAgeDays
	}

	if l.cfg.Records != nil {
		recordAge = l.cfg.Records.MaxAgeDays
	}

	if l.cfg.Verification != nil {
		checkAge = l.cfg.Verification.MaxAgeDays
	}

	steps := []struct {
		fn   func(context.Context, []string, int) error
		kind string
		age  int
	}{
		{l.loadProvenance, KindProvenance, provenanceAge},
		{l.loadDeployments, KindDeployment, recordAge},
		{l.loadObservations, KindObservation, recordAge},
		{l.loadCheckResults, KindCheckResult, checkAge},
		{l.loadMeasurements, KindMeasurement, 0},
	}

	for _, step := range steps {
		if len(paths[step.kind]) == 0 {
			continue
		}

		if err := step.fn(ctx, paths[step.kind], step.age); err != nil {
			return err
		}
	}

	return nil
}

// admitStatement runs the engine for a typed fact set of a statement kind.
func (l *loader) admitStatement(kind string, item *verified, facts *admit.Facts, days int) assurance.Admission {
	return admit.Admit(&admit.Input{
		Verification: item.verification, Kind: kind, Context: l.context(), Producers: l.cfg.Producers, Facts: *facts,
		MaxAge: maxAge(days),
	})
}

// malformed records a statement whose predicate cannot be read.
func (l *loader) malformed(item *verified, err error) {
	bad := &statement.MalformedError{Path: item.loaded.Path, Reason: err.Error()}
	l.diags = append(l.diags, bad.Diagnostic())
}

func (l *loader) loadProvenance(ctx context.Context, paths []string, days int) error {
	items, err := l.verifyAll(ctx, KindProvenance, paths)
	if err != nil {
		return err
	}

	for i := range items {
		slsa, perr := statement.ParseSLSA(&items[i].loaded.Statement, items[i].verification)
		if perr != nil {
			l.malformed(&items[i], perr)

			continue
		}

		facts := admit.SLSAFacts(&slsa)
		admission := l.admitStatement(KindProvenance, &items[i], &facts, days)
		l.inadmissible(items[i].loaded.Path, admission)
		l.ev.Provenance = append(l.ev.Provenance, l.add(KindProvenance, &items[i], admission))
	}

	return nil
}

func (l *loader) loadDeployments(ctx context.Context, paths []string, days int) error {
	return loadStated(ctx, l, KindDeployment, paths, days, statement.ParseDeploymentRecord,
		func(r *statement.DeploymentRecord) (admit.Facts, string) {
			return admit.DeploymentRecordFacts(r), r.Digest
		},
		&l.ev.Deployments)
}

func (l *loader) loadObservations(ctx context.Context, paths []string, days int) error {
	return loadStated(ctx, l, KindObservation, paths, days, statement.ParseWorkloadObservation,
		func(o *statement.WorkloadObservation) (admit.Facts, string) {
			return admit.WorkloadObservationFacts(o), o.Digest
		},
		&l.ev.Observations)
}

// loadStated verifies and admits deployment records or workload observations: statements whose predicate states the
// digest of a container. A statement that names no subject with that digest is an integrity failure, the same check
// as provenance.NewRecord and NewObservation, so both admissions of one statement agree.
func loadStated[T any](ctx context.Context, run *loader, kind string, paths []string, days int,
	parse func(*statement.Statement) (T, error), factsOf func(*T) (admit.Facts, string), into *[]Statement,
) error {
	items, err := run.verifyAll(ctx, kind, paths)
	if err != nil {
		return err
	}

	for i := range items {
		parsed, perr := parse(&items[i].loaded.Statement)
		if perr != nil {
			run.malformed(&items[i], perr)

			continue
		}

		facts, stated := factsOf(&parsed)
		if items[i].loaded.Statement.Contradicts(stated) {
			facts.Extra = append(facts.Extra, assurance.DetailIntegrityFailure)
		}

		admission := run.admitStatement(kind, &items[i], &facts, days)
		run.inadmissible(items[i].loaded.Path, admission)
		*into = append(*into, run.add(kind, &items[i], admission))
	}

	return nil
}

// loadCheckResults admits the check results, then groups them by check and environment and gives each group its
// verdict.
func (l *loader) loadCheckResults(ctx context.Context, paths []string, days int) error {
	items, err := l.verifyAll(ctx, KindCheckResult, paths)
	if err != nil {
		return err
	}

	ids := map[string]struct{}{}

	for i := range items {
		result, perr := checkresult.Parse(&items[i].loaded.Statement)
		if perr != nil {
			l.malformed(&items[i], perr)

			continue
		}

		admitted := verdict.Admit(&verdict.Input{
			Result: &result, Verification: items[i].verification, Path: items[i].loaded.Path, Digest: items[i].loaded.Digest,
			Context: l.context(), Producers: l.cfg.Producers, MaxAge: maxAge(days),
		})

		l.inadmissible(items[i].loaded.Path, admitted.Admission)
		l.add(KindCheckResult, &items[i], admitted.Admission)
		l.ev.CheckResults = append(l.ev.CheckResults, admitted)

		if result.Check != "" {
			ids[result.Check] = struct{}{}
		}
	}

	for id := range ids {
		l.ev.CheckIDs = append(l.ev.CheckIDs, id)
	}

	slices.SortFunc(l.ev.CheckIDs, canonjson.CompareUTF16)

	l.groupChecks()

	return nil
}

// groupChecks gives the verdict of every check in every environment that its results name.
func (l *loader) groupChecks() {
	type key struct{ check, environment string }

	groups := map[key][]verdict.Admitted{}

	for i := range l.ev.CheckResults {
		res := l.ev.CheckResults[i].Result
		k := key{check: res.Check, environment: res.Environment}
		groups[k] = append(groups[k], l.ev.CheckResults[i])
	}

	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, func(a, b key) int {
		return compareKeys([]string{a.check, a.environment}, []string{b.check, b.environment})
	})

	for _, k := range keys {
		outcome := verdict.Evaluate(k.check, groups[k])
		l.diags = append(l.diags, outcome.Diagnostics...)
		l.ev.CheckOutcomes = append(l.ev.CheckOutcomes, CheckOutcome{Environment: k.environment, Outcome: outcome})
	}
}

func (l *loader) loadMeasurements(ctx context.Context, paths []string, _ int) error {
	items, err := l.verifyAll(ctx, KindMeasurement, paths)
	if err != nil {
		return err
	}

	for i := range items {
		measurement, diags := quality.ReadMeasurement(&items[i].loaded, items[i].verification)
		l.diags = append(l.diags, diags...)

		if measurement.Path == "" {
			continue
		}

		// The age and the criterion belong to the scenario, so the evaluation of the scenario decides them; the
		// line in the results shows what the statement alone says (signature, producer trust, revision, run).
		facts := admit.MeasurementFacts(&measurement.Predicate)
		admission := l.admitStatement(KindMeasurement, &items[i], &facts, 0)
		l.add(KindMeasurement, &items[i], admission)
		l.ev.Measurements = append(l.ev.Measurements, measurement)
	}

	return nil
}

// loadPlans reads each declared plan, verifies its binding statement, and binds the plan to the revision.
func (l *loader) loadPlans(ctx context.Context) error {
	declared := slices.Clone(l.cfg.Plans)
	slices.SortStableFunc(declared, func(a, b workspace.AssurancePlan) int {
		return compareKeys([]string{a.Environment, a.File, a.Statement}, []string{b.Environment, b.File, b.Statement})
	})

	for i := range declared {
		if err := ctx.Err(); err != nil {
			return err //nolint:wrapcheck // the CLI maps the context error to exit 130.
		}

		one, err := l.loadPlan(ctx, &declared[i])
		if err != nil {
			return err
		}

		l.ev.Plans = append(l.ev.Plans, one)
	}

	return nil
}

// unbound is the binding of a plan that has no readable plan or no statement: content not verified.
func unbound() plan.Binding {
	return plan.Binding{Admission: assurance.NewAdmission(assurance.IntegrityNotVerified, []assurance.Detail{assurance.DetailNotAuthenticated})}
}

func (l *loader) loadPlan(ctx context.Context, declared *workspace.AssurancePlan) (Plan, error) {
	out := Plan{Declared: *declared, Coverage: assurance.CoverageFailed, Binding: unbound(), Gaps: []assurance.Gap{}}
	source := diag.WithSource(diag.Source{URI: declared.File})

	data, err := l.readDeclared(declared.File)

	var configErr *diag.Error
	if errors.As(err, &configErr) {
		return Plan{}, configErr
	}

	if err != nil {
		detail := "the plan file cannot be read: " + err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			detail = "the plan file does not exist"
		}

		out.Gaps = append(out.Gaps, assurance.Gap{Kind: GapPlanMissing, Detail: detail, Path: declared.File})
		l.diags = append(l.diags, diag.New(diag.PRV008, diag.Detail(declared.File+": "+detail), source))

		return out, nil
	}

	read, diags := plan.Read(data, declared.File)
	l.diags = append(l.diags, diags...)

	if read == nil {
		out.Gaps = append(out.Gaps, assurance.Gap{Kind: GapPlanUnusable, Detail: "the plan file is not a supported plan", Path: declared.File})

		return out, nil
	}

	out.Plan, out.Coverage = read, read.Coverage

	bindCtx := l.context()
	bindCtx.Environment = declared.Environment

	input := &plan.BindInput{
		Plan: read, File: declared.File, Context: bindCtx, Producers: l.cfg.Producers, MaxAge: maxAge(declared.MaxAgeDays),
	}

	var bound *verified

	if declared.Statement != "" {
		if _, err := fsutil.Confine(l.opts.Root, trimDot(declared.Statement)); errors.Is(err, fsutil.ErrOutsideRoot) {
			return Plan{}, diag.Fail(diag.PRV002, diag.Params{paramPath: declared.Statement})
		}

		// The subject is compared by Bind, which also gives the warning: the verification gets no expected digest, so
		// a mismatch is not reported twice.
		items, err := l.verifyAll(ctx, KindPlan, []string{trimDot(declared.Statement)})
		if err != nil {
			return Plan{}, err
		}

		if len(items) == 1 {
			bound = &items[0]
			input.Statement, input.Verification = &bound.loaded, bound.verification
		}
	}

	out.Binding = plan.Bind(input)
	l.diags = append(l.diags, out.Binding.Diagnostics...)

	if bound != nil {
		l.inadmissible(bound.loaded.Path, out.Binding.Admission)

		st := l.add(KindPlan, bound, out.Binding.Admission)
		out.Statement = &st
	}

	return out, nil
}
