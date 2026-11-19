package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestVersionPrintsFourLines(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "--version")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q", lines)
	}

	if lines[0] != "concordia dev" {
		t.Errorf("line 1 = %q", lines[0])
	}

	if lines[1] != "calm-schema 1.2,1.3" {
		t.Errorf("line 2 = %q", lines[1])
	}

	if lines[2] != "cbm-adapter concordia-cbm/v2" {
		t.Errorf("line 3 = %q", lines[2])
	}

	if !regexp.MustCompile(`^cbm (\d+\.\d+\.\d+|missing)$`).MatchString(lines[3]) {
		t.Errorf("line 4 = %q", lines[3])
	}
}

func TestVersionAfterDoubleDashIsNotTheFlag(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "doctor", "--", "--version")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2 (unexpected argument)", res.Code)
	}

	if strings.Contains(res.Stdout, "calm-schema") {
		t.Errorf("stdout = %q", res.Stdout)
	}
}

func TestUsageErrorGivesExitCode2AndWRK010(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--no-such-option"},
		{"--bad-flag"},
		{"no-such-command"},
		{"doctor", "--no-such-option"},
		{"doctor", "extra"},
	} {
		res := runCLI(t, args...)
		if res.Code != 2 {
			t.Errorf("%v: exit code = %d", args, res.Code)
		}

		if !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: stderr = %q", args, res.Stderr)
		}

		if res.Stdout != "" {
			t.Errorf("%v: stdout = %q", args, res.Stdout)
		}
	}
}

func TestInvalidFailOnGivesExitCode2(t *testing.T) {
	t.Parallel()

	// The root option is validated before the command runs, so any command shows it.
	// (`check --fail-on fatal` is tested with the check command.)
	for _, args := range [][]string{{"--fail-on", "bad"}, {"doctor", "--fail-on", "fatal"}} {
		res := runCLI(t, args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}

	for _, value := range []string{"error", "warning", "info"} {
		if res := runCLI(t, "--fail-on", value, "clean"); res.Code != 2 || !strings.Contains(res.Stderr, "nothing to clean") {
			t.Errorf("--fail-on %s: exit code = %d, stderr = %q", value, res.Code, res.Stderr)
		}
	}
}

func TestHelpGivesExitCode0(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "--help")
	if res.Code != 0 {
		t.Fatalf("exit code = %d", res.Code)
	}

	for _, name := range []string{"--workspace", "--profile", "--format", "--output", "--offline", "--no-color", "--quiet", "--verbose", "--trace", "--fail-on", "--version"} {
		if !strings.Contains(res.Stdout, name) {
			t.Errorf("help does not show %s", name)
		}
	}

	if !strings.Contains(res.Stdout, "concordia.yaml") {
		t.Errorf("help does not show the default workspace file")
	}

	for _, name := range []string{"init", "doctor", "clean"} {
		if res := runCLI(t, name, "--help"); res.Code != 0 {
			t.Errorf("%s --help: exit code = %d", name, res.Code)
		}
	}
}

func TestNoArgumentsPrintRootHelp(t *testing.T) {
	t.Parallel()

	res := runCLI(t)
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "Usage:") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if res.Stderr != "" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestHandlerErrorsAreMapped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if res := runIn(t, dir, "init", "--ci", "generic"); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	failing := func(err error) cli.Deps {
		return cli.Deps{NewClient: func(*workspace.Workspace, *cli.Context) observation.Client {
			return failingClient{err: err}
		}}
	}

	t.Run("an error that is not a diagnostic gives CO-WRK-099 and exit code 7", func(t *testing.T) {
		t.Parallel()

		res := runWith(t, &runConfig{Dir: dir, Deps: failing(errors.New("boom"))}, "clean", "--project", "--yes")
		requireMappedFailure(t, &res, 7, "CO-WRK-099", "boom")
	})

	t.Run("a panic is recovered", func(t *testing.T) {
		t.Parallel()

		deps := cli.Deps{NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { panic("exploded") }}

		res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "doctor")
		requireMappedFailure(t, &res, 7, "CO-WRK-099", "exploded")
	})

	t.Run("a stopped context gives exit code 130", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		res := runWith(t, &runConfig{Ctx: ctx, Dir: dir, Deps: failing(context.Canceled)}, "clean", "--project", "--yes")
		if res.Code != 130 || res.Stderr != "" {
			t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
		}
	})
}

// requireMappedFailure fails the test unless the run exited with the code and its stderr holds each text.
func requireMappedFailure(t *testing.T, res *result, code int, texts ...string) {
	t.Helper()

	if res.Code != code {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, text := range texts {
		if !strings.Contains(res.Stderr, text) {
			t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
		}
	}
}

func TestDoctorWritesOutputFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	deps := cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client {
			return observation.NewFakeClient(func(string, map[string]any) (any, error) { return map[string]any{"projects": []any{}}, nil }, "")
		},
		Probe: func(context.Context, string, []string) *cli.ProbeResult { return nil },
	}

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "doctor", "--json", "--output", "out/doctor.json")
	if res.Stdout != "" {
		t.Fatalf("stdout = %q", res.Stdout)
	}

	var doc struct {
		Capabilities []struct {
			Name string `json:"name"`
		} `json:"capabilities"`
	}

	if err := json.Unmarshal([]byte(readFile(t, dir, "out/doctor.json")), &doc); err != nil {
		t.Fatal(err)
	}

	if len(doc.Capabilities) == 0 {
		t.Fatal("no capabilities in the output file")
	}
}

// failingClient is a CBM client whose calls fail with err.
type failingClient struct{ err error }

func (f failingClient) Call(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, f.err
}

func (f failingClient) Version(context.Context) (string, error) { return "0.12.1", nil }
