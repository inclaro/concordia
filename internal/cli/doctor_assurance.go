package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/renderer"
	"github.com/inclaro/concordia/internal/workspace"
)

// rendererState is what `doctor` found for one renderer: the executable, or why there is none. The probe runs once, so
// the capability line and the lock comparison read the same answer.
type rendererState struct {
	err   error
	tool  string
	found renderer.Found
	// needed says that a declared rendering source runs the tool.
	needed bool
}

// probeRenderers probes helm and kustomize, in that order (the order of renderer.Tools). A renderer that no source
// uses is still probed: it is an optional capability.
func probeRenderers(ctx context.Context, app *Context, wsp *workspace.Workspace) []rendererState {
	var assurance *workspace.Assurance
	if wsp != nil {
		assurance = wsp.Assurance
	}

	tools := renderer.Tools()
	states := make([]rendererState, 0, len(tools))

	for _, tool := range tools {
		found, err := renderer.Probe(ctx, tool, app.IO.Getenv)
		states = append(states, rendererState{tool: tool, found: found, err: err, needed: assurance.UsesRenderer(tool)})
	}

	return states
}

// capability gives the doctor line of the renderer. It is optional unless a declared source runs it.
func (s *rendererState) capability() capability {
	if s.err != nil {
		detail := "optional: " + s.tool + " not found"
		if s.needed {
			detail = "required by a declared rendering source: " + message(s.err)
		}

		return capability{Name: s.tool, Status: statusMissing, Detail: detail, Required: s.needed}
	}

	return capability{
		Name: s.tool, Status: statusAvailable, Version: s.found.Pin.Version, Detail: firstHex(s.found.Pin.SHA256), Required: s.needed,
	}
}

// rendererPinMismatch says how the renderers on this machine differ from the pins of the lock, "" when they do not.
// A used renderer without a pin is a mismatch too: a render needs the pin (CO-PRV-003), so `check` would stop. A tool
// that no source uses and the lock does not pin is no problem.
func rendererPinMismatch(lock *workspace.Lockfile, states []rendererState) string {
	for i := range states {
		state := &states[i]
		pin := lock.Renderer(state.tool)

		switch {
		case pin == nil && state.needed && state.err == nil:
			return fmt.Sprintf("%s %s is used by a rendering source and is not pinned; run `concordia lock update`", state.tool, state.found.Pin.Version)
		case pin == nil:
			continue
		case state.err != nil:
			return fmt.Sprintf("lock pins %s %s, which was not found", state.tool, pin.Version)
		case !samePin(pin, state.found.Pin.Pin()):
			return fmt.Sprintf("lock has %s %s %s, found %s %s",
				state.tool, pin.Version, firstHex(pin.SHA256), state.found.Pin.Version, firstHex(state.found.Pin.SHA256))
		}
	}

	return ""
}

// samePin compares two renderer pins the way a render does: the version without a "v" prefix and build metadata, the
// digest without its prefix and case.
func samePin(a *workspace.LockRenderer, b workspace.LockRenderer) bool {
	return render.NormalizeVersion(a.Version) == render.NormalizeVersion(b.Version) &&
		strings.EqualFold(strings.TrimPrefix(a.SHA256, "sha256:"), strings.TrimPrefix(b.SHA256, "sha256:"))
}

// claimRegistryView is the `claims` object of the doctor JSON: the registry digest, the registered claims, and the
// contracts with the claims that each enables and does not enable.
type claimRegistryView struct {
	Digest    string            `json:"digest"`
	Claims    []string          `json:"claims"`
	Contracts []claims.Contract `json:"contracts"`
}

func newClaimRegistryView(registry *claims.Registry) claimRegistryView {
	ids := claims.IDs()
	names := make([]string, len(ids))

	for i, id := range ids {
		names[i] = string(id)
	}

	return claimRegistryView{Digest: registry.Digest(), Claims: names, Contracts: registry.Contracts()}
}

// prettyContracts gives the claim contract section of the text output: one block per contract with its supported
// tools, the claims it enables, and the claims it states it does not enable.
func prettyContracts(registry *claims.Registry) string {
	var out strings.Builder

	fmt.Fprintf(&out, "\nclaim contracts (registry %s)\n", registry.Digest())

	contracts := registry.Contracts()

	for i := range contracts {
		contract := &contracts[i]

		head := fmt.Sprintf("  %s %s (%s", contract.ID, contract.Version, contract.Kind)
		if contract.Experimental {
			head += ", experimental"
		}

		out.WriteString(head + ")\n")

		if len(contract.Tools) > 0 {
			out.WriteString("    tools: " + strings.Join(contract.Tools, ", ") + "\n")
		}

		enabled := make([]string, len(contract.Enabled))
		for i := range contract.Enabled {
			enabled[i] = string(contract.Enabled[i].Claim)
		}

		out.WriteString("    enables: " + joinOrNone(enabled) + "\n")
		out.WriteString("    does not enable: " + joinOrNone(contract.NotEnabled) + "\n")
	}

	return out.String()
}
