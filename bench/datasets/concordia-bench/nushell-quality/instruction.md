You are a developer on the `Nushell` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add quality evidence to Nushell

Concordia is already adopted on this repository (workspace `concordia.yaml`, model `architecture/nushell.calm.json`, pack `nushell-adopt`). The architecture team wants a first quality scenario, a verification rule for the engine's unit tests, and a quality area check.

## Work

- Environment: declare one environment `ci` whose source is `Cargo.toml`.
- Quality scenarios: file `quality/nushell.yaml`, a quality scenario set with one structural scenario `QS-NU-LAYERING` for the area `modifiability`, owned by `team-nushell`, scoped to `nu-engine` and `nu-protocol`. It is judged by the rule `NU-LYR-001`.
- Required quality areas: `modifiability` and `availability` (every required area needs at least one scenario).
- Control: attach a control `testing-001` to the node `nu-engine` that requires the check `unit-tests`. A missing result for this check must only warn (`unknown-disposition: warn`). The control's requirement URL must be resolvable offline through a URL mapping (`architecture/url-mapping.json` and a local requirement file under `architecture/resources/`).
- Rule: policy pack `nushell-quality` (version `1.0.0`) at `policies/nushell-quality.policy.yaml`, registered beside `nushell-adopt`. It has one rule `NU-VER-001` with severity `warning` and owner `team-nushell`: the check `unit-tests` must have a passing result in the environment `ci`. An `unknown` verdict of this rule only warns.
- Verification results: results are read from `build/results/*.json`. Commit the result of the check `unit-tests` as `build/results/unit-tests.json`: an unsigned in-toto statement (check `unit-tests`, outcome `passed`, environment `ci`, evaluator `cargo-test` version `1`, covering `nu-engine`, time `2026-01-01T00:00:00Z`). It has no signature: Concordia must not treat it as evidence that the tests pass.
- Findings that the new checks report on the current repository are pre-existing: record them in the baseline (`exceptions/concordia.baseline.json`). Do not fix the code. The one exception is the `unknown` finding of `NU-VER-001`: keep it out of the baseline, so the missing test evidence stays visible as an active finding.

## Acceptance criteria

- The model, the workspace and the lock file are valid; `concordia assurance validate` succeeds; everything is committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`.
- The report shows the unsigned result as inadmissible, `NU-VER-001` as `unknown`, and the required area without scenario (`availability`) as a finding in the quality evaluation area.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `workspace.md`, `policies.md`, `architecture.md`, `results.md`.
