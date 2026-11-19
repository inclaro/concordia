package parity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

type job struct {
	caseDir string
	run     Run
}

// recordCases runs the CALM CLI for each run of each case and writes expected.json.
func recordCases(ctx context.Context, opts *RecordOptions, tag string) (runCount, caseCount int, err error) {
	dirs, err := caseDirs(filepath.Join(opts.Cases, CasesDir))
	if err != nil {
		return 0, 0, err
	}

	jobs, expected, err := loadJobs(dirs, tag)
	if err != nil {
		return 0, 0, err
	}

	if err := recordJobs(ctx, opts, jobs, expected); err != nil {
		return 0, 0, err
	}

	if err := writeExpected(dirs, expected); err != nil {
		return 0, 0, err
	}

	return len(jobs), len(dirs), nil
}

// recordJobs runs the jobs and adds the outcome of each run to the expectation of its case.
func recordJobs(ctx context.Context, opts *RecordOptions, jobs []job, expected map[string]*Expected) error {
	results, errs := runJobs(ctx, opts, jobs)

	for i, j := range jobs {
		if errs[i] != nil {
			return fmt.Errorf("%s run %s: %w", j.caseDir, j.run.Name, errs[i])
		}

		expected[j.caseDir].Runs[j.run.Name] = results[i]
	}

	return nil
}

// writeExpected writes the expectation of each case directory to its expected.json.
func writeExpected(dirs []string, expected map[string]*Expected) error {
	for _, dir := range dirs {
		if err := writeJSON(filepath.Join(dir, ExpectedFile), expected[dir]); err != nil {
			return err
		}
	}

	return nil
}

// caseDirs gives the sorted directories below root that hold a case file.
func caseDirs(root string) ([]string, error) {
	var dirs []string

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && d.Name() == CaseFile {
			dirs = append(dirs, filepath.Dir(p))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk cases: %w", err)
	}

	slices.Sort(dirs)

	return dirs, nil
}

// loadJobs loads the cases of dirs. It gives a job for each run and an empty
// expectation for each case directory.
func loadJobs(dirs []string, tag string) ([]job, map[string]*Expected, error) {
	var jobs []job

	expected := map[string]*Expected{}

	for _, dir := range dirs {
		c, err := LoadCase(dir)
		if err != nil {
			return nil, nil, err
		}

		expected[dir] = &Expected{Tag: tag, Runs: map[string]Outcome{}}

		for _, run := range c.Runs {
			jobs = append(jobs, job{caseDir: dir, run: run})
		}
	}

	return jobs, expected, nil
}

// runJobs runs the jobs on all processors. The result and the error of a job
// are at the index of the job.
func runJobs(ctx context.Context, opts *RecordOptions, jobs []job) ([]Outcome, []error) {
	results := make([]Outcome, len(jobs))
	errs := make([]error, len(jobs))
	next := make(chan int)

	var workers sync.WaitGroup

	for range runtime.GOMAXPROCS(0) {
		workers.Go(func() {
			for i := range next {
				results[i], errs[i] = RunUpstream(ctx, opts, jobs[i].caseDir, &jobs[i].run)
			}
		})
	}

	for i := range jobs {
		next <- i
	}

	close(next)
	workers.Wait()

	return results, errs
}

// RunUpstream runs `calm validate -f json` for one run in a copy of the case directory.
func RunUpstream(ctx context.Context, opts *RecordOptions, caseDir string, run *Run) (Outcome, error) {
	dir, err := os.MkdirTemp("", "calm-parity-*")
	if err != nil {
		return Outcome{}, fmt.Errorf("make temp dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	if err := CopyDir(caseDir, filepath.Join(dir, caseCopyName), nil); err != nil {
		return Outcome{}, err
	}

	out := filepath.Join(dir, "out.json")

	args, err := upstreamArgs(opts, out, run)
	if err != nil {
		return Outcome{}, err
	}

	code, err := runCalm(ctx, opts, dir, args)
	if err != nil {
		return Outcome{}, err
	}

	data, err := readCalmOutput(out)
	if err != nil {
		return Outcome{}, err
	}

	outcome, err := FromUpstream(data, code)
	if err != nil {
		return Outcome{}, err
	}

	return WithoutTempPaths(outcome, dir), nil
}

// upstreamArgs gives the arguments of the CALM CLI for a run that writes its output to out.
func upstreamArgs(opts *RecordOptions, out string, run *Run) ([]string, error) {
	cli, err := filepath.Abs(filepath.Join(opts.Upstream, "cli", "dist", "index.js"))
	if err != nil {
		return nil, fmt.Errorf("upstream CLI path: %w", err)
	}

	args := []string{cli, "validate", "-f", "json", "-o", out}

	// The CALM CLI has no short option for --timeline.
	for _, option := range []struct{ flag, value string }{
		{"-a", run.Architecture}, {"-p", run.Pattern}, {"--timeline", run.Timeline}, {"-u", run.URLMapping},
	} {
		if option.value != "" {
			args = append(args, option.flag, option.value)
		}
	}

	return args, nil
}

// runCalm runs the Node.js executable with args in the copy of the case in dir
// and gives the exit code. Exit code 1 is a finding and no failure.
func runCalm(ctx context.Context, opts *RecordOptions, dir string, args []string) (int, error) {
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, opts.Node, args...) //nolint:gosec // the Node.js path and arguments come from the record command.
	cmd.Dir = filepath.Join(dir, caseCopyName)
	// A fixed home keeps the user's CALM CLI configuration out of the results.
	cmd.Env = append(os.Environ(), "HOME="+dir, "NO_COLOR=1")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err := cmd.Run()
	if cmd.ProcessState == nil {
		return 0, fmt.Errorf("start calm validate: %w", err)
	}

	code := cmd.ProcessState.ExitCode()

	if calmFailed(runCtx, err, code) {
		return 0, fmt.Errorf("calm validate failed (exit %d, %v): %s", code, err, strings.TrimSpace(stderr.String())) //nolint:errorlint // the error can be nil here.
	}

	return code, nil
}

// calmFailed tells if a run of the CALM CLI failed: it timed out, or it ended with an error
// and an exit code other than 1.
func calmFailed(runCtx context.Context, err error, code int) bool {
	if runCtx.Err() != nil {
		return true
	}

	return err != nil && code != 1
}

// readCalmOutput reads the output file of a run. A missing file gives no data.
func readCalmOutput(out string) ([]byte, error) {
	data, err := os.ReadFile(out) //nolint:gosec // the output file of the run.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read calm output: %w", err)
	}

	return data, nil
}

// Placeholders of the temporary directories of a run in the recorded messages.
const (
	casePlaceholder = "<case>"
	tempPlaceholder = "<tmp>"
	caseCopyName    = "case"
)

// WithoutTempPaths replaces the temporary directory of a run in the finding messages: the copy of the case
// (`<dir>/case`) by `<case>` and any other path below dir by `<tmp>`. The calm CLI names absolute paths in its load
// errors, and the directory name is random, so without this every recording would rewrite expected.json. The
// resolved form of dir (`/private/var/...` for `/var/...` on macOS) is replaced too. The recordings are compared
// across machines, so paths are compared and recorded with forward slashes: on Windows the backslashes of the
// message and of dir become `/` first, which also makes what follows a placeholder the same on every OS.
func WithoutTempPaths(outcome Outcome, dir string) Outcome {
	roots := []string{filepath.ToSlash(dir)}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		if slashed := filepath.ToSlash(resolved); slashed != roots[0] {
			roots = append(roots, slashed)
		}
	}

	pairs := make([]string, 0, 2*2*len(roots))
	for _, root := range roots {
		// The case directory first: it is below dir.
		pairs = append(pairs, root+"/"+caseCopyName, casePlaceholder, root, tempPlaceholder)
	}

	replacer := strings.NewReplacer(pairs...)

	findings := make([]Finding, len(outcome.Findings))
	for i, f := range outcome.Findings {
		f.Message = replacer.Replace(filepath.ToSlash(f.Message))
		findings[i] = f
	}

	outcome.Findings = findings

	return outcome
}
