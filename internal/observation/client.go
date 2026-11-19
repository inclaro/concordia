package observation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
)

// Client is the port to codebase-memory-mcp (CBM). Tests plug in a FakeClient
// or a ReplayClient.
//
// Call gives the JSON body of the tool result. It gives a *diag.Error
// (CO-OBS-001, CO-OBS-002 or CO-OBS-003) when the call fails. Each caller
// decodes the body into its own struct.
type Client interface {
	Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error)
	// Version gives the version text of the executable, for example `0.12.1`.
	Version(ctx context.Context) (string, error)
}

var versionPattern = regexp.MustCompile(`codebase-memory-mcp (\d+\.\d+\.\d+)`)

// ParseVersion reads the output of `codebase-memory-mcp --version`. The bool
// is false when no version is found.
func ParseVersion(text string) (string, bool) {
	m := versionPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}

	return m[1], true
}

// VersionSupport says how well a CBM build is supported.
type VersionSupport string

// Version support values.
const (
	VersionSupported VersionSupport = "supported"
	VersionDegraded  VersionSupport = "degraded"
)

var semverPrefix = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

const (
	supportedMajor = 0
	supportedMinor = 12
)

// BuildAssessment is the decision about one CBM build.
type BuildAssessment struct {
	// Profile is the capability profile that is pinned to exactly this build and
	// the adapter schema. It is nil when no profile applies.
	Profile *CapabilityProfile
	Support VersionSupport
}

// AssessBuild decides what evidence a CBM build may supply:
//
//   - A build with a capability profile is supported, whatever its version number.
//   - Without a profile, `0.12.x` is supported (the code families only: every
//     infrastructure family is unsupported, see NoProfileFamilies).
//   - Without a profile, above `0.12` and below `1.0` is degraded (CO-OBS-004 warning).
//   - All other versions give CO-OBS-004 (exit 5).
func AssessBuild(version string) (BuildAssessment, []diag.Diagnostic, error) {
	if profile, ok := ProfileFor(version, AdapterSchema); ok {
		return BuildAssessment{Support: VersionSupported, Profile: profile}, nil, nil
	}

	if m := semverPrefix.FindStringSubmatch(version); m != nil {
		major, majorErr := strconv.Atoi(m[1])
		minor, minorErr := strconv.Atoi(m[2])

		if majorErr == nil && minorErr == nil && major == supportedMajor {
			switch {
			case minor == supportedMinor:
				return BuildAssessment{Support: VersionSupported}, nil, nil
			case minor > supportedMinor:
				warn := diag.New(diag.OBS004, diag.Params{"version": version}, diag.WithSeverity(diag.SeverityWarning))

				return BuildAssessment{Support: VersionDegraded}, []diag.Diagnostic{warn}, nil
			}
		}
	}

	return BuildAssessment{}, nil, diag.Fail(diag.OBS004, diag.Params{"version": version})
}

// decode reads a JSON body into v. Numbers that are decoded into an `any`
// keep their text (json.Number).
func decode(tool string, raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(v); err != nil {
		return diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("%s: unexpected response: %v", tool, err)))
	}

	return nil
}

// Decode reads the body of a Client.Call result into a value of type T.
// A body that does not fit gives CO-OBS-001.
func Decode[T any](tool string, raw json.RawMessage) (T, error) { //nolint:ireturn // T is the caller's result type, not an interface.
	var out T

	err := decode(tool, raw, &out)

	return out, err
}

// CallAs calls a tool and decodes the body into a value of type T.
func CallAs[T any](ctx context.Context, c Client, tool string, args map[string]any) (T, error) { //nolint:ireturn // T is the caller's result type, not an interface.
	var zero T

	raw, err := c.Call(ctx, tool, args)
	if err != nil {
		return zero, err
	}

	return Decode[T](tool, raw)
}
