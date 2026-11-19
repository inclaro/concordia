You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the structure rules for fzf

Concordia is already adopted on `main` (model, bindings, pack `fzf-adopt`, baseline). The architecture team wants five more rules that guard the finer structure of fzf. Put them in a new pack `fzf-structure` (version `1.0.0`) at `policies/fzf-structure.policy.yaml`. Every rule has the owner `team-fzf`, a one-sentence rationale and a remediation. Choose the rule kinds that fit each intent; for each rule, imports and calls both count.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `results.md`, `exceptions.md`, `cli.md`.

## Rules

- `FZF-DNY-001` "The matcher never reaches the renderer", severity `error`: no chain of dependencies of up to 3 steps may lead from `fzf-algo` to `fzf-tui`, whatever packages lie in between.
- `FZF-API-001` "Utilities are used through their public API", severity `error`: `fzf-core`, `fzf-tui` and `fzf-algo` may call `fzf-util` only through the functions declared in `src/util/util.go` and `src/util/chars.go`. Only calls count for this rule.
- `FZF-ENT-001` "The protector entry point exists", severity `error`: `fzf-protector` must contain a function whose name is `Protect`.
- `FZF-AGG-001` "The renderer does not use the matcher", severity `error`: `fzf-tui` must not depend on `fzf-algo`. Report one finding for each offending source file, however many imports and calls it has.
- `FZF-WRN-001` "The sandbox package avoids the utilities", severity `warning`: `fzf-protector` must not depend on `fzf-util`. A breach is a warning: the default check still exits 0, and the check run with the fail threshold `warning` exits 1.

## Acceptance criteria

- The new pack is loaded by the workspace and its rules are valid.
- Violations that already exist in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`, with no `unknown` rule verdict.
- Everything is committed on `main`.
