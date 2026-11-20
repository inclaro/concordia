You are a developer on the `browser-use` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Enable the core pack and the trust context on browser-use

The security group asks for Concordia's built-in core rules on browser-use, with the layer and trust-boundary tags set on the architecture model, and for the telemetry data flow to be marked as crossing a trust boundary. Documentation: `/opt/concordia/docs/reference/core-rules.md`, `trust-context.md`, `architecture.md`, `workspace.md`, `policies.md`.

## Workspace

- Enable the built-in core pack in `concordia.yaml` next to the existing pack `browser-use-adopt` (keep that pack and its policy file).
- Tag the nodes in `architecture/browser-use.calm.json` (`concordia.tags`):

| Node | Tags |
|---|---|
| `bu-cli`, `bu-mcp` | `presentation` |
| `bu-agent` | `application` |
| `bu-tools` | `domain` |
| `bu-browser`, `bu-llm` | `infrastructure` |
| `bu-telemetry` | `infrastructure`, `external` |
| `bu-credentials` | `restricted` |
| `bu-shared` | `shared` |

- Rule overrides on the core pack: the rule about undeclared dependencies (`CORE-DECL-001`) has severity `warning` (it overlaps with the error-level `BU-DEC-001`), and the layer rule `CORE-LYR-001` is owned by `team-agent-platform`. For `CORE-DECL-001` and the trust-boundary rule `CORE-SEC-001`, the team accepts that Concordia cannot judge calls that end in web endpoints (HTTP routes) that no architecture node covers: those gaps are reported as unknown results but must not fail the build, so set the unknown handling of both rules to a warning.
- Trust context: the workspace allows the data classifications `public`, `internal` and `secret`. Declare on the model nodes (`concordia.trust`): `bu-agent` and `bu-credentials` are in the trust zone `core`; `bu-telemetry` is in the zone `egress`. `bu-credentials` has the classification `secret` and `bu-telemetry` has `internal`. The relationship `bu-agent-uses-bu-telemetry` carries the data classification `internal`, and the relationship `bu-agent-uses-bu-credentials` the classification `secret`. A flow from the agent to telemetry then crosses a trust boundary.
- Violations of the core rules that already exist in the code are recorded in the baseline `exceptions/concordia.baseline.json`, not fixed. Do not change the bindings or the source code.

## Acceptance criteria

- The model validates and `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
