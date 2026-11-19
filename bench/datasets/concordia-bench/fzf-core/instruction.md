You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Enable the built-in core rules on fzf

Concordia is already adopted on `main`. The architecture team wants the rules of Concordia's built-in `core` pack on top of the fzf rules, with the tags and tuning below.

Relevant documentation: `/opt/concordia/docs/reference/core-rules.md`, `policies.md`, `workspace.md`, `architecture.md`, `/opt/concordia/docs/how-to/use-policy-packs.md`.

## Tags

Tag the CALM nodes (in the `concordia` extension of each node):

| Node | Tag |
|---|---|
| `fzf-tui` | `presentation` |
| `fzf-core` | `application` |
| `fzf-algo` | `domain` |
| `fzf-protector` | `infrastructure` |
| `fzf-util` | `shared` (already set) and `shared-kernel`: the other modules may import it |

## Pack and overrides

- Use the built-in pack `core` from the workspace, without copying it.
- Raise the severity of `CORE-LYR-003` (shared code is a leaf) from `warning` to `error`.
- `CORE-LYR-002` (the domain does not depend on infrastructure or presentation) must also count the edges that Concordia finds with low confidence, so that it gives a verdict instead of `unknown`.
- Turn `CORE-DECL-001` off: the declared relationships of fzf are enforced by the team's own rules.
- Turn `CORE-SEC-001` off: fzf has no trust boundary to guard.

## Acceptance criteria

- The CALM model, the bindings and the policies are valid.
- Violations that the core rules find in the existing code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`, with no `unknown` rule verdict.
- Everything is committed on `main`.
