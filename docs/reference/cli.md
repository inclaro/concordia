# CLI reference

This page describes every `concordia` command, option, exit code, and environment variable. Option names and defaults match `concordia <command> --help`.

For task-oriented instructions, see the [how-to guides](../README.md#how-to-guides). For the diagnostics that commands print, see [diagnostics](diagnostics.md).

Not available in this version: CALM Hub.

## Synopsis

```text
concordia [global options] <command> [command options]
```

Commands: `init`, `doctor`, `model validate`, `index`, `bind discover`, `bind validate`, `check`, `explain`, `trace`, `policy list`, `policy add`, `policy verify`, `baseline create`, `baseline update`, `waiver add`, `waiver validate`, `adr validate`, `assessment validate`, `assessment status`, `assessment approve`, `assessment revoke`, `assurance validate`, `render`, `lock update`, `diff`, `export bundle`, `export verify`, `watch`, `clean`.

## Global options

Global options apply to every command.

| Option | Value | Default | Description |
|---|---|---|---|
| `--workspace` | path | `concordia.yaml` | Workspace configuration. See [workspace](workspace.md). |
| `--profile` | name | none | Named profile from `profiles` in the workspace. An unknown name gives `CO-WRK-004`. |
| `--format` | `pretty`, `json`, `junit`, `sarif` (command-specific, see below) | command-specific | Output format where the command supports it. |
| `--output` | path | none | Write the primary output to a file. |
| `--offline` | flag | `false` | Prohibit network access and require locked local resources. |
| `--no-color` | flag | color on | Disable terminal color. |
| `--quiet` | flag | `false` | Emit the result only. |
| `--verbose` | flag | `false` | Emit diagnostics to stderr. |
| `--trace` | flag | `false` | Emit the adapter and evaluation trace without source content. |
| `--fail-on` | `error`, `warning`, `info` | `check.failOn` (`error`) | Minimum active severity that yields failure. |
| `--version` | flag | | Print four lines: the Concordia version, the CALM schema version, the CBM adapter ID, and the CBM version (or `missing`). The digest of the CBM executable is shown by [`doctor`](#doctor), not here. |
| `-h`, `--help` | flag | | Display help for a command. |

## init

Writes a starter workspace.

```text
concordia init [--calm <path>] [--preset <preset>] [--ci <ci>] [--force]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--calm` | path | none | Existing CALM architecture file. Without it, `init` writes `architecture/architecture.json`. |
| `--preset` | `modular-monolith`, `services`, `custom`, `core` | `custom` | Starter policy. `core` writes the same empty local policy file as `custom` and adds the built-in [core pack](core-rules.md) to `policies.packs`. |
| `--ci` | `github`, `gitlab`, `generic` | `github` | CI template. |
| `--force` | flag | off | Overwrite existing files. Without it, an existing file is kept and `CO-WRK-005` is reported. |

Outputs: `concordia.yaml`, `architecture/architecture.json` (unless `--calm` is set), `bindings/concordia.bindings.yaml`, `policies/default.policy.yaml`, `exceptions/waivers.yaml`, `.concordia.lock`, the CI template file, and the lines `build/concordia/` and `.concordia/` in `.gitignore`. With `--preset core`, `.concordia.lock` also records the core pack. After writing, `init` validates the workspace, the architecture, and the policies.

Exit codes: 0, or 2 when validation fails.

## doctor

Checks the toolchain: workspace, CALM schemas, CBM, git, and the optional Java, Structurizr, cosign, Helm, and Kustomize tools. It also lists the claim contracts of this build.

```text
concordia doctor [--json] [--strict]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--json` | flag | off | Machine-readable output. |
| `--strict` | flag | off | Degraded capabilities also give exit code 5. |

Outputs: one status per capability, for example `workspace`, `calm-schema`, `cbm`, `git`, and `lock`. A status is `available`, `degraded`, `missing`, or `incompatible`. The `structurizr` capability shows the version that `structurizr version` prints. It is `degraded`, with a warning, when that version is not the pinned `2025.11.09` that committed view exports need; `render --format` still works with it.

A CBM build that has a tested [capability profile](capability-profiles.md) is `available`, whatever its version number, and the detail names the profile. Without a profile, CBM `0.12.x` is `available` and supplies the code families only, and a newer version below `1.0` is `degraded` (`CO-OBS-004`). The text output has a line below `cbm` with the profile identifier, the profile digest, and the number of families for each support level, then one line for each family that has loss modes. With `--json`, each item of `capabilities[]` has `name`, `status`, `version`, `detail`, and `required` (`version`, `detail`, and `required` only when they have a value). The `cbm` item also has a `profile` object:

| Key | Description |
|---|---|
| `applies` | `true` when a profile is pinned to the build; `false` otherwise. |
| `id`, `digest` | Identifier and content digest (`sha256:<hex>`) of the profile. Only when `applies` is `true`. |
| `families` | One entry for each family: `family`, `level` (`supported`, `partial`, or `unsupported`), `maturity` (`advisory` or `stable`), and `lossModes` (a list that can be empty). Without a profile, the code families are `supported` and all others `unsupported`. |

The `lock` capability is `degraded` when the lock records a capability profile (`resolved.cbm.profile`) that differs from the profile of the found build.

The `cbm` capability also shows the executable that Concordia found: the resolved path and the digest of the file. The text output has a line `executable <path> sha256:<hex>` below `cbm`. With `--json`, the `cbm` item has `path` and `sha256`. A client without an executable (a test double) has neither key. The `lock` capability is `degraded` when `resolved.cbm.sha256` differs from the digest of the found executable (`lock has cbm sha256 <locked>, found <found> (<path>)`). A lock without `sha256` is not compared.

The `cbm` capability also compares the digest of the executable with the pinned CBM build of this Concordia version (the digest of the binary for your platform, see [`cbm install`](#cbm-install)). When they are equal, the detail says that this is the pinned build, the build that Concordia is tested with. When they differ, the capability is `degraded` and the detail is `not the CBM build Concordia is tested with (pinned <version> sha256:<short>)`. `doctor` gives no diagnostic code for this. A client without an executable is not compared.

The `helm` and `kustomize` capabilities are probed in this order, after `cosign`. Each shows the version and the first digits of the `sha256` of the executable that Concordia finds. The executable is the one that the environment variable `HELM` or `KUSTOMIZE` names, else the one on the `PATH` (see [Environment variables](#environment-variables)). A renderer that is not found is `missing` with the detail `optional: <tool> not found`, and does not fail `doctor`. When a declared [`assurance.rendering`](workspace.md#assurancerendering) source uses the tool, the capability has `required: true`: if it is `missing` or `incompatible`, `doctor` exits `5`, and the detail says that a declared rendering source requires it. `cosign` stays optional: without it, statements cannot be verified (see [Statements](assurance.md#statements)).

When a renderer on this machine differs from the pin in the lock, or a renderer that a source uses has no pin, the `lock` capability is `degraded` and the detail names the tool, the pinned version and digest, and what was found, or tells you to run `concordia lock update`. A pinned renderer that is not found is also a mismatch. A tool that no source uses and the lock does not pin is no problem.

After the capabilities, the text output has a section `claim contracts (registry sha256:<hex>)`. It has one block for each contract of the claim registry: the ID, the version, the kind (`adapter` or `evaluator`) and `experimental` when it is, the supported `tools`, the claims that it `enables`, and the claims that it `does not enable`. With `--json`, the document has a `claims` object next to `capabilities`: `digest` (the registry digest, also written to `toolchain.assurance.claimsRegistryDigest` of a result), `claims` (the IDs of every registered claim), and `contracts` (each with `id`, `kind`, `version`, `tools`, `inputs`, `fixtures`, `limits`, `assumes`, `enabled`, `notEnabled`, and `experimental`). The same contracts are described in [Assurance](assurance.md#claims-and-contracts).

Exit codes: 0; 5 when `workspace`, `calm-schema`, `cbm`, or `git` is `missing` or `incompatible`, when a renderer that a declared rendering source uses is `missing` or `incompatible`, or with `--strict` when any capability is `degraded`.

## cbm install

Installs the pinned codebase-memory-mcp (CBM) build that Concordia is tested with. The pin is in `internal/cbmpin/cbm-pin.tsv`: the release tag and, for each platform, the archive and binary digests. Linux uses the fully static `-portable` build.

```text
concordia cbm install [--dir <dir>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--dir` | path | `~/.local/bin` (Linux, macOS); `%LOCALAPPDATA%\Programs\codebase-memory-mcp` (Windows) | Install directory. It is created when it is missing. These are the directories of the upstream CBM installer. |

The command works in this order:

1. It looks for `codebase-memory-mcp` (`codebase-memory-mcp.exe` on Windows) on the `PATH`, then in the install directory.
2. When it finds a file and the digest is the pinned digest, it prints the path and the version, and exits 0. It changes nothing.
3. When it finds a file with another digest, it refuses and exits 5 with `CO-OBS-005`. The diagnostic names the path and digest that it found, and the pinned version and digest. It never overwrites a file, and it has no force option. CBM runs one daemon for each user, so the CBM that Concordia uses must be the same binary as the CBM of your AI agents. Point your agents and Concordia at one binary. See [CBM and your AI agents](../explanation/cbm-and-agents.md).
4. Otherwise it downloads the pinned archive over HTTPS from `https://github.com/DeusData/codebase-memory-mcp/releases/download/<tag>/<archive>`, checks the digest of the archive, extracts the binary, checks the digest of the binary, and writes it atomically to the install directory with mode `0755`. An archive entry with an unsafe path is refused. A download or digest failure gives `CO-OBS-006` and exit 6. Nothing stays in the install directory.

It prints the path of the file. It warns on stderr when the install directory is not on the `PATH`. `--offline` stops the download (`CO-OBS-006`).

The command does not run `codebase-memory-mcp install`. It does not configure the MCP clients of your AI agents, and it says so in its output.

Set `CONCORDIA_CBM_DOWNLOAD_URL` to an https directory that holds the archives, for a mirror. The archive is still checked against the pinned digests.

## model validate

Validates a CALM 1.2 or 1.3 architecture document with its patterns, decorators, and Concordia extensions; or CALM patterns alone; or a CALM timeline. It gives the results of `calm validate` of the CALM CLI: the same modes and the same rules, and each such diagnostic names its CALM CLI rule in `rule`. See [architecture](architecture.md).

```text
concordia model validate [--architecture <path>] [--pattern <path>]... [--decorator <path>]... [--strict]
concordia model validate --pattern-only [--pattern <path>]... [--strict]
concordia model validate --timeline <path> [--strict]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--architecture` | path | `architecture.source` | CALM architecture file. A `$schema` that is not a CALM release names a pattern (a path relative to the architecture, or a URL), which takes the place of the release schema. |
| `--pattern` | path | `architecture.patterns` | CALM pattern (JSON Schema). Repeatable. |
| `--decorator` | path | `architecture.decorators` | CALM decorator document. Repeatable. Added to `architecture.decorators`. |
| `--pattern-only` | flag | off | Validates the workspace patterns and each `--pattern` as CALM patterns, without an architecture. Needs at least one pattern; not with `--architecture`. |
| `--timeline` | path | none | Validates the CALM timeline document only. Not with `--architecture`, `--pattern`, or `--pattern-only`. |
| `--strict` | flag | off | Warnings also give exit code 2. |

The rules of the CALM CLI that run: the JSON Schema (the CALM release schema, or the patterns, narrowed by the decision answers of the architecture), the Spectral architecture, pattern, and timeline rules, the control requirement validation (`requirement-url` is a JSON Pointer into the pattern starting with `#`, a CALM release schema, or an http(s) URL), and the node details (each `details.detailed-architecture`, relative to the workspace root, is validated again below its pointer).

Outputs: diagnostics. With `--format json`, the document is `{"schemaVersion": "1.1", "mode", "architecture", "document", "diagnostics"}`. `mode` is `architecture`, `architecture-with-pattern` (with at least one pattern), `pattern`, or `timeline`. `architecture` (`path`, `digest`) is set in the architecture modes and `null` otherwise; `document` is the timeline, or the first pattern, in the other modes, and `null` in the architecture modes.

In the architecture modes, `model validate` also checks the `views` of the workspace against the model, the declared environments, and Git (`CO-DGM-001` and `CO-DGM-002`, exit code 2; see [views](workspace.md#views)). It prints one information diagnostic (`CO-DGM-005`) for each default view that the workspace does not declare: a `system-context` view for each software system, a `container` view for each system that has services, and a `deployment` view for each production environment. A suggestion never changes the exit code, and not even `--strict` counts it. `check` prints the same diagnostics in the `diagnostics` of its result and stops with exit code 2 on an invalid view.

Exit codes: 0; 2 for invalid input; 6 for an unmapped or unlocked remote reference (`CO-INT-012`).

## index

Indexes the repository with codebase-memory-mcp (CBM) and writes the file coverage.

```text
concordia index [--project <name>]... [--mode <mode>] [--persist] [--include <glob>]... [--exclude <glob>]... [repo-path]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `repo-path` | path | project `root` | Repository to index. |
| `--project` | name, repeatable | all projects | Workspace project to index. `repo-path` is allowed only when one project is selected. With two or more workspace projects, `index` also runs the CBM `cross-repo-intelligence` pass for each selected project (a failure gives `CO-OBS-014`). The coverage file of the primary project is `build/concordia/coverage.json`; of another project, `build/concordia/coverage-<name>.json`. With `--format json` and two or more selected projects, the output is an array. |
| `--mode` | `fast`, `moderate`, `full` | `observation.indexMode` (`moderate`) | CBM index mode. |
| `--persist` | flag | off | Write the CBM graph artifact to the repository. |
| `--include` | glob | project `include` | Coverage include glob. Repeatable. |
| `--exclude` | glob | project `exclude` | Coverage exclude glob. Repeatable. |

Outputs: the CBM project and `build/concordia/coverage.json`, which records the project, the graph generation, the source revision, the file counts, and the byte counts (`bytes`: `total`, `covered`, `ratio`). It also writes `build/concordia/index-result.json` (`index-result-<name>.json` for a project that is not the primary one): the directories that CBM skipped (`excluded.dirs`), the files that it did not index, and the files that it parsed in part, for the graph generation that the index made. `check` reads it as coverage evidence; see [coverage](results.md#coverage-section-of-the-run). Concordia never edits `.cbmignore`; a skipped directory under an environment source gives a gap with the line to add.

Exit codes: 0; 3 for an adapter failure; 5 when CBM is missing or incompatible.

## bind discover

Suggests binding candidates.

```text
concordia bind discover [--architecture <path>] [--project <name>]... [--strategy <list>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--architecture` | path | `architecture.source` | CALM architecture file. |
| `--project` | name, repeatable | all projects | Workspace project to use. |
| `--strategy` | comma-separated list of `path`, `package`, `build`, `route`, `cluster`, `deployment` | `path,package` | Discovery strategies. `build` proposes candidates from build-definition directories, `route` from observed routes, `cluster` from import clusters, and `deployment` from deployed resources (status `proposed`). Any other value gives `CO-WRK-010`. |

Outputs: `bindings/concordia.bindings.candidates.yaml`, or the path in `--output`.

Exit codes: 0, 2, 3.

## bind validate

Resolves bindings against the observed graph. Fails on conflicts and on unmatched required bindings. See [bindings](bindings.md).

```text
concordia bind validate [--architecture <path>] [--project <name>]... [--strict] [--show-unbound]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--architecture` | path | `architecture.source` | CALM architecture file. |
| `--project` | name, repeatable | all projects | Workspace project to use. |
| `--strict` | flag | off | Unbound files also give exit code 2. |
| `--show-unbound` | flag | off | List indexed files that have no binding (`CO-BND-005`) on stderr. |

Outputs: a one-line summary (or JSON with `--format json`).

Exit codes: 0; 2 for binding errors or, with `--strict`, unbound files; 3.

## check

Runs the conformance pipeline: CALM intent versus the observed code graph. See [results](results.md).

```text
concordia check [options]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--architecture` | path | `architecture.source` | CALM architecture file. |
| `--policy` | path | `policies.files` and `policies.packs` | Policy file. Repeatable. Replaces both `policies.files` and `policies.packs`: with `--policy`, no pack is read and no pack is locked. |
| `--rule` | id | all rules | Run only this rule. Repeatable. |
| `--control` | id | all controls | Run only the rules of this CALM control. Repeatable. |
| `--project` | name, repeatable | all projects | Workspace project to check. |
| `--environment` | id, repeatable | all declared environments | Declared environment to check. Environments that are not selected are listed in `inputs.environments.excluded` (results and manifest) and have no verdict. An id that `environments` does not declare gives `CO-WRK-017` (exit 2). |
| `--changed` | git ref | `HEAD` | Limit reporting to findings that touch files changed since the merge base of the ref and `HEAD`. Without a value, the working-tree changes. One positional ref is allowed with `--changed` (`check --changed origin/main`). Rules that cannot be reduced are reported in full (`CO-CON-027`). An invalid ref gives `CO-WRK-015`. |
| `--use-baseline` | path | `exceptions.baseline` | Baseline file. |
| `--waivers` | path | `exceptions.waivers` | Waiver register. |
| `--minimum-coverage` | ratio 0..1 | `check.minimumCoverage` (`0.95`) | Required file coverage. |
| `--unknown` | `fail`, `warn`, `ignore` | `check.unknown` (`fail`) | What an unknown rule verdict does. |
| `--evidence-dir` | dir | none | Write one evidence file for each finding. |
| `--rollout-report` | path (`--rollout-report=<path>`) | `build/concordia/rollout-report.json` | Write the [rollout report](#rollout-report) as canonical JSON and print a text summary on stderr. Without a value, the default path. A path must be given as `--rollout-report=<path>`. The path is relative to the working directory. The flag changes no result and no exit code. |
| `--incremental` | none | off | Reuse the review evaluation of an earlier run from the derived cache `.concordia/cache/impact/`, and write it. The result equals the result of a full evaluation; see [incremental evaluation](../explanation/review-currency.md#incremental-evaluation). A damaged cache or a cache of another key is not used: Concordia evaluates in full and reports the note `CO-ASM-010`. Without the flag, `check` reads and writes nothing in the cache directory. |

`--format` accepts `pretty` (default), `json`, `junit`, and `sarif`. Any other value gives `CO-WRK-010`.

Outputs: the main format on stdout or `--output`; every entry of `report.outputs` (default `build/concordia/results.json`, `schemaVersion` `1.1`, with the [areas](results.md#areas), the [observability counters](results.md#observability-counters), and the [`resultDigest`](results.md#result-digest)); `.concordia/runs/<runId>/manifest.json` (status, run, toolchain, inputs, summary, and counters); `.concordia.lock` when a remote reference or a policy pack is newly locked; the rollout report with `--rollout-report`. The pretty format shows each area in its own section.

With a workspace that has [`assessments`](workspace.md#assessments), the results and the run manifest hold an `analysis` object with the counters of the review evaluation: `mode` (`full` or `incremental`), `complete`, `visited`, `closureSize`, `invalidations`, `cacheHits`, `cacheMisses`, and `incompleteAnalyses`. The counters hold no secret values. `--incremental` is the only option that writes below `.concordia/cache/`; `check`, `index`, and `assessment status` write no CALM, binding, assessment, or exception file.

Exit codes: 0 ok; 1 an active finding at or above `failOn`; 2 invalid input, including a policy pack problem (`CO-POL-010` to `CO-POL-015`); 3 adapter failure, including a stale graph (`CO-OBS-008`); 4 an unknown verdict whose policy is `fail`, or a coverage shortfall; 5; 6 an unlocked remote reference, or a resource or policy pack whose digest differs from `.concordia.lock` (`CO-INT-012`); 7 when a rule has the verdict `error`.

### Rollout report

`check --rollout-report` reviews a rule family before you make it `mandatory`. It is built from the run that `check` just made and has one row for each rule family and environment:

| Field | Content |
|---|---|
| `family`, `environment` | The family that the rules declare (`rule.family`, empty when they declare none) and the declared environment (empty when the results have none). |
| `enforcement`, `rules` | The mode of the row, or `mixed` when its rules differ, and each rule with its mode. |
| `verdicts` | Counts of `pass`, `violation`, `unknown`, `notApplicable`, and `error`. The unit is the obligation result for a typed rule and the rule result for the other rules. |
| `superseded` | Obligations that a precedence entry placed below another rule. They are not in `verdicts`. |
| `unknown` | Each unknown result with its `reason`, the `missing` guarantees (`binding`, `coverage`, `environment`, or `evidence`, each with its detail and next action), and `wouldBlock`, which is true when the rule says `onUnknown: fail` and the mode does not gate yet. For a typed rule the reason is one of the closed unknown reasons. For another rule it is the diagnostic code of the rule reason. |
| `wouldBlock` | The live violations at or above `failOn` whose mode is `advisory` or `shadow`: they block if the row were `mandatory`. Waived and baselined findings are not listed. |

An active `check.rollback` is recorded in the report. Concordia never changes a mode because of the report. See [roll out new obligations](../how-to/roll-out-new-obligations.md#review-with-the-rollout-report).

## explain

Explains a rule or a finding: rationale, subjects, plan, evidence path, coverage, and disposition.

```text
concordia explain [--finding <fingerprint>] [--source] [--path shortest|all] [--results <path>] <rule-id>
```

| Option | Value | Default | Description |
|---|---|---|---|
| `<rule-id>` | id | required | Rule to explain. |
| `--finding` | fingerprint | none | Explain one finding. Accepts the full fingerprint or a unique hex prefix. |
| `--source` | flag | off | Show 2 lines of source before and after each location, read from the working tree. |
| `--path` | `shortest`, `all` | `shortest` | With `all`, print the related locations as `Related evidence:` instead of `Related locations:`. |
| `--results` | path | the JSON output of the workspace | Results file to read. |

Outputs: a text explanation on stdout. For a rule of the `infrastructure` pack (`INFRA-…`), each finding also prints its environment, scoped identity, and occurrence, and the trace excerpt of its subject in that environment, the same block as [`trace`](#trace).

When the results file has a `trust` section, each finding also prints the [trust crossings](trust-context.md#trust-in-resultsjson) of the flow from its source node to its target node: one `Trust crossings:` header with the flow identity and data classification, then one line for each dimension. `--format json` adds the same entries under a top-level `trust` object, and leaves the key out when the selected findings have none.

Exit codes: 0, 2.

## trace

Shows the declared-level deployment trace of one CALM node: service, workload, container image, build definition, and source evidence, with the evidence role, resolution, gaps, and limits of each step, for each selected environment. In an environment with rendered sources or plans (`assurance`), the text also has a `Rendered or evaluated` block with the sources, their coverage, and the rendered entities of the node, and the `Evidence:` line names the level `rendered-or-evaluated`. Entities are named by kind (`Deployment <namespace>/<name>`, `container <name>: <image>`), the limits of the steps are listed in one `Limits:` line, and `--verbose` adds the text of each limit, the obligation of each rule, and the consumer counts. It reads the results file like `explain`, or runs the pipeline when there is none. It does not evaluate or write the CALM model and works with `--offline`. The text never says that a workload runs. See [trace a deployment](../how-to/trace-a-deployment.md).

```text
concordia trace [--environment <id>]... [--source] [--results <path>] <architecture-id>
```

| Option | Value | Default | Description |
|---|---|---|---|
| `<architecture-id>` | id | required | CALM node to trace: a node tagged `deployable`, a deployed node of a `deployed-in` relationship, or a node with a deployment binding. |
| `--environment` | id | all declared | Declared environment to show. Repeatable. |
| `--source` | flag | off | Show the source line of each location with 2 lines around it, read inside the workspace root. Secret values are redacted (`CO-REP-005`). A location outside the root is shown without lines (`CO-REP-006`). |
| `--results` | path | the JSON output of the workspace | Results file to read. |
| `--format` | `pretty`, `json` | `pretty` | With `json`, the keys are `schemaVersion`, `architectureId`, `levels`, `traces`, and `sourceContext` (with `--source`). |

Outputs: the trace on stdout.

Exit codes: 0; 2 with `CO-WRK-010` (the id is not a trace subject) or `CO-WRK-017` (no environment is declared, or `--environment` names one that is not declared).

## policy list

Lists the policy packs. It needs no CBM and no index, and it never uses the network. See [use policy packs](../how-to/use-policy-packs.md).

```text
concordia policy list [--rules]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--rules` | flag | off | Also list the rules of each pack: ID, severity, and name. A rule that an override disables shows `(disabled)`. |

Outputs: the packs built into the binary (name, version, and number of rules), and the packs of `policies.packs` (source, pack ID and version, number of rules, status, and digest). The status of a pack of the workspace is `builtin`, `verified` (the digest agrees with the `sha256` pin), `mismatch`, `missing`, `unpinned`, `remote`, `unknown`, or `invalid`. A bad pack does not change the exit code.

With `--format json`, the output has the keys `builtin` and `configured`. Each entry has `ruleCount`, `digest`, and, with `--rules`, `rules` (`id`, `severity`, `name`, `enabled` for a configured pack). A configured entry also has `source`, `id`, `version`, `status`, `overrides`, and, for a bad pack, `problem`.

Exit codes: 0; 2 when the workspace file is not valid. Without a workspace file, the command lists only the built-in packs.

## policy add

Adds a policy pack to `policies.packs`. This is the only command that can use the network.

```text
concordia policy add [--id <name>] [--force] <source>
```

| Option | Value | Default | Description |
|---|---|---|---|
| `<source>` | `builtin:<name>`, path, or `https://` URL | required | The pack to add. |
| `--id` | name | `metadata.id` of the pack | Name of the vendored file. It does not apply to a built-in pack. |
| `--force` | flag | off | Replace an existing entry and its vendored file. |

A built-in pack is added as `{use: builtin:<name>}`. A file or URL is first validated against the pack schema. Then it is copied to `policies/vendor/<id>@<version>.policy.yaml`, and the entry `{use: <that path>, sha256: <digest>}` is added. The command edits the workspace file as YAML: comments and other keys stay. An existing entry without `--force` is refused (`CO-WRK-010`). With `--force`, the digest of the entry is replaced and its `rules` overrides stay. If `.concordia.lock` exists, the command also updates the lock entry of the pack.

`builtin:views` cannot be added: the views pack applies automatically when `views` are declared in the workspace, and `check` refuses it in `policies.packs`. `policy add builtin:views` fails with `CO-POL-001` and writes nothing. `policy list` shows the pack with a note to that effect (the `note` key in JSON).

A URL must use `https`. The command fetches it with a 30 s timeout and a limit of 1 MiB. `--offline` refuses the fetch.

Outputs: `added <use>`, or `updated <use>` when `--force` replaced an entry (with the old and the new digest when they differ).

Exit codes: 0; 2 for a pack that is not valid, an unknown built-in pack, an existing entry, or an unsupported source; 6 when the fetch fails or `--offline` is set (`CO-POL-016`).

## policy verify

Loads all packs of the workspace and checks them. It never uses the network.

```text
concordia policy verify
```

The command checks that each file pack agrees with its `sha256` pin, that each pack is valid, that each override names a rule of its pack, and that no rule ID is declared twice (in packs or in `policies.files`).

Outputs: `policy verified: <n> pack(s), <m> rule(s)`. Each rule with an `obligation` block adds one line with its declaration: the rule ID, its `family` and `enforcement` (if set), then the predicate type, multiplicity, direction, relation kind, `deployedIn=true` or `deployedIn=false` (if the relation target comes from the model), scope, evidence roles, minimum resolution, completeness, `passCompleteness` (if set), and `applies` selector (if set). With `--format json`, the keys are `status`, `packs`, `rules`, and `typedRules`.

Exit codes: 0; 2 with diagnostics (`CO-POL-001`, `CO-POL-002`, `CO-POL-003`, `CO-POL-004`, `CO-POL-005`, `CO-POL-010` to `CO-POL-015`).

## baseline create

Records current findings as the baseline. See [exceptions](exceptions.md).

```text
concordia baseline create [--from <results.json>] [--output <path>] [--severity <list>] [--expires <date>] [--force]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--from` | path | runs the check | Use an existing results file. |
| `--output` | path | `exceptions.baseline` | Baseline file. |
| `--severity` | comma-separated list | `warning,error` | Severities to include. |
| `--expires` | date | none | Expiry date for entries. |
| `--force` | flag | off | Overwrite an existing baseline. Without it, an existing file gives `CO-WRK-005` (exit 2). |

Outputs: the baseline file.

Exit codes: 0, 2.

## baseline update

Updates the baseline.

```text
concordia baseline update [--from <results.json>] [--prune-resolved] [--accept <fingerprint>]... [--reason <text>] [--owner <owner>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--from` | path | runs the check | Use an existing results file. |
| `--prune-resolved` | flag | off | Remove resolved entries. |
| `--accept` | fingerprint, repeatable | none | Accept current active findings. Refused when the `CI` environment variable is set (`CO-EXC-008`). |
| `--reason` | text | none | Reason recorded with accepted entries. |
| `--owner` | owner | none | Owner recorded with accepted entries. |

Outputs: the updated baseline file.

Exit codes: 0, 2.

## waiver add

Adds a waiver to the waiver register. A waiver covers one finding (`--finding`) or a scope of findings (`--subject`, `--environment`, `--obligation`). Any scope option writes a scoped waiver, which also needs `--rule` and `--approver`. See [Exceptions](exceptions.md).

```text
concordia waiver add [--finding <fingerprint>] [--rule <id>] [--subject <id>] [--environment <name>] [--obligation <id>] [--approver <identity@authority>] [--evidence <path>]... [--owner <owner>] [--reason <text>] [--expires <YYYY-MM-DD>] [--issue <id>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--finding` | fingerprint | none | Finding to waive. Not with a scope option. |
| `--rule` | id | none | Rule the waiver applies to. A finding waiver reads it from `results.json` when you omit it. A scoped waiver needs it. |
| `--subject` | CALM ID | none | Scoped waiver: the subject that the findings are about. |
| `--environment` | name | none | Scoped waiver: the environment of the findings. |
| `--obligation` | id | none | Scoped waiver: a full obligation ID. |
| `--approver` | `identity@authority` | none | Scoped waiver, required: the approver and the id of the authority of role `exception`. The text after the last `@` is the authority. |
| `--evidence` | path | none | Scoped waiver: a compensating evidence file, relative to the workspace root. The command records its digest. Repeat the option for more files. |
| `--owner` | owner | none | Owner of the waiver. |
| `--reason` | text | none | Reason for the waiver. |
| `--expires` | `YYYY-MM-DD` | none | Last day on which the waiver applies. With `exceptions.maxExpiryDays`, a scoped waiver must stay within that many days. |
| `--issue` | id | none | Related issue. |

`--finding` together with a scope option, a wildcard in a scope value, a missing approver, and a duration above `exceptions.maxExpiryDays` give `CO-EXC-002`.

Outputs: a new `WV-<n>` entry in `exceptions.waivers`.

Exit codes: 0, 2.

## waiver validate

Validates waivers: the shape of every entry (for a scoped waiver the scope, the approver, the expiry, and the maximum duration), the expiry, and whether each waiver still matches a finding of the last results. It also verifies the approver of each scoped waiver at the trust anchor and reports `CO-EXC-009` for a waiver that a run would not apply.

```text
concordia waiver validate [--at <date>] [--fail-expiring-within <days>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--at` | date | now | Evaluate at this date. |
| `--fail-expiring-within` | days | none | Report an error when a waiver expires within N days. |

Outputs: diagnostics.

Exit codes: 0; 2 when an error diagnostic is reported (for example `CO-EXC-002` or `CO-EXC-006`).

## adr validate

Validates the ADR governance declarations offline: the decision register files, the lifecycle profile, the triggers, the subjects against the architecture, and the rule links against the loaded policy packs. It reads files only: no CBM, no network, no writes. See [ADR governance](adr-governance.md).

```text
concordia adr validate [--profile <name>]
```

Outputs: a summary line on stdout, or a diagnostic (`CO-ADR-001` to `CO-ADR-005`) on stderr. A workspace without `adr.files` prints a note and exits 0.

Exit codes: 0; 2 when a declaration is not valid.

## assessment validate

Validates the assessment documents offline: the `Assessment`, `Assumption`, `AssessmentApproval`, and `AssessmentRevocation` files under `assessments.paths`. It checks each file against its closed schema, the references between files, duplicate versions, the authored state, and that no approved version was edited. It reads files only: no CBM, no git, no network, no writes. A decision record is checked for its form and the digests it binds; who made it is not checked here. See [assessments](assessments.md).

```text
concordia assessment validate [--json] [--profile <name>]
```

| Option | Type | Default | Description |
|---|---|---|---|
| `--json` | boolean | `false` | Print one canonical JSON document instead of text. `--format json` does the same. |

Outputs: a summary on stdout with each assessment version, its type, environment, outcome, and authored state; with `--json`, a document with `ok`, the lists `assessments`, `assumptions`, `approvals`, and `revocations` (each with its digest and file), and `diagnostics`. Problems are diagnostics `CO-ASM-001` to `CO-ASM-003` on stderr, all of them, each with its file and line; with `--json` they are in `diagnostics` and stdout holds the document. A workspace without an `assessments` key and without an `assessments/` directory prints a note and exits 0.

Exit codes: 0; 2 when a document is not valid.

## assessment status

Shows the currency of every assessment version and every required assessment, evaluated as `check` evaluates it: the state (`current`, `review-required`, `expired`, `revoked`, or `unknown`), the reasons, the approval that the result rests on, the decision records that were not applied and why, the triggers with their shortest explanation paths, the removed items without a disposition, and the digest-impact report of the current digest contract. Expiry and revocation are judged at the evaluation time, which the output states in its first line (`evaluatedAt` with `--json`). The command reads files and git and never writes: it does not approve, and it prints no secret value. See [assessments](assessments.md#currency).

```text
concordia assessment status [--json] [--at <time>] [--assessment <id>] [--profile <name>]
```

| Option | Type | Default | Description |
|---|---|---|---|
| `--json` | boolean | `false` | Print one canonical JSON document instead of text. `--format json` does the same. The output of two runs on equal inputs is byte-identical. |
| `--at` | date or RFC 3339 time | now, or `CONCORDIA_NOW` | The evaluation time. `--at 2027-01-01` expires an approval that expires earlier. An unparsable value is exit code 2. |
| `--assessment` | assessment id | all | Show only this assessment, its required entries, and its approvals in the digest-impact report. An id that no document declares is exit code 2. |

Outputs: text with one block per assessment version, then the required entries and the digest impact, with diagnostics (`CO-ASM-006`, `CO-ASM-007`, `CO-ASM-008`, `CO-ASM-009`, `CO-SEC-002`) on stderr; with `--json`, the document that [assessments](assessments.md#status-output) shows, and the diagnostics in `diagnostics`.

Exit codes: 0 when the evaluation ran, whatever the currency says (`check` gates on it); 2 for an invalid document, an unknown `--assessment`, or an unparsable `--at`; 5 when cosign is needed and missing.

## assessment approve

Records the decision of a reviewer on one assessment version. It prints the subject (the assessment, its dependency items with their digests, the assumptions, the evidence, the digest contract, the capability profile, and the subject digest) and writes one `AssessmentApproval` record when `--confirm-subject` gives that digest. It writes only the record file, and with `--sign` its signature bundle. It refuses to run when the `CI` environment variable is true. See [review and approve assessments](../how-to/review-and-approve-assessments.md).

```text
concordia assessment approve <assessment-id> --reviewer <identity> [--confirm-subject <digest>] [--authority <id>] [--version <n>] [--expires <date>] [--condition <text>] [--decision approved|rejected] [--path <file>] [--sign] [--sign-key <key>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--reviewer` | identity | required | The reviewer who makes the decision. |
| `--confirm-subject` | `sha256:<hex>` | none | The subject digest that the reviewer examined. Without it, or when it differs from the current subject, the command prints the subject and writes nothing. |
| `--authority` | authority id | the authority of the required assessment | The authority that the reviewer acts under. |
| `--version` | integer | the latest version | The assessment version. |
| `--expires` | `YYYY-MM-DD` or RFC 3339 | none | The last day of the approval (it ends at the start of the next day, UTC), or the time it ends. It must be in the future. |
| `--condition` | text | none | An expiry condition in words. Repeatable. |
| `--decision` | `approved`, `rejected` | `approved` | The decision. |
| `--path` | workspace-relative file | `<assessments>/approvals/<id>-v<n>-<seq>.yaml` | Where the record goes. A record is never overwritten. |
| `--sign` | flag | off | Sign the canonical JSON of the record with `cosign sign-blob` and write `<record>.sigstore.json`. |
| `--sign-key` | key reference | keyless | The cosign key. Signing needs network access, also with a key: cosign 3 records every signature in the public transparency log. |

Outputs: the subject summary on stdout, then the path and digest of the record, and the next step. The record binds the current dependency items, the digest contract, the capability profile, and what the approval was produced under (normalization and evaluator versions, the policy digest of the review rules, and the digest of the authority configuration).

Exit codes: 0; 2 (`CO-ASM-005`) for CI, a missing `--reviewer`, a missing or wrong `--confirm-subject`, a subject that cannot be computed, or an existing record, and (`CO-ASM-001`) for an assessment that does not exist; 5 when `--sign` needs `cosign` and it is missing (`CO-WRK-014`) or signing fails (`CO-REP-003`); 6 for signing with `--offline`, with or without `--sign-key` (`CO-WRK-016`).

## assessment revoke

Records the revocation of an approval. It prints the approval and its digest and writes one `AssessmentRevocation` record when `--confirm-subject` gives the digest of the approval record. It writes only the record file, and with `--sign` its signature bundle. It refuses to run when the `CI` environment variable is true.

```text
concordia assessment revoke <approval-id> --reviewer <identity> --reason <text> [--confirm-subject <digest>] [--authority <id>] [--path <file>] [--sign] [--sign-key <key>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--reviewer` | identity | required | The reviewer who makes the decision. |
| `--reason` | text | required | Why the approval is revoked. |
| `--confirm-subject` | `sha256:<hex>` | none | The digest of the approval record that the reviewer examined. |
| `--authority` | authority id | the authority of the approval | The authority that the reviewer acts under. |
| `--path` | workspace-relative file | `<assessments>/revocations/<approval id>.yaml` | Where the record goes. |
| `--sign`, `--sign-key` | | | As for `assessment approve`. |

Exit codes: as for `assessment approve`, with (`CO-ASM-001`) for an approval that does not exist.

## assurance validate

Loads the assurance inputs that the workspace key [`assurance`](workspace.md#assurance) declares, as `check` does, and shows for each input how far it is admissible. It renders every source twice, evaluates no rule, loads no code graph, and writes no file. Use it before `check` to find an input that cannot support a claim. See [Add stronger evidence](../how-to/add-stronger-evidence.md).

```text
concordia assurance validate [--json]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--json` | flag | off | Print one canonical JSON document instead of text. `--format json` does the same. |

The command reads the rendering sources, the plans and their statements, the provenance statements, the deployment records, the observations, the check results, the measurements, and the quality scenario files. It verifies each statement as `check` does, so a declared producer and a cosign (or `--offline` with a trusted root) decide the integrity. See [Statements](assurance.md#statements).

Output, for a workspace with a rendering source, a plan, and two check results:

```text
rendering staging deploy/plain
  integrity: content verified by digest
  admissible: yes
  coverage: complete
plan staging build/plans/create.json
  integrity: content not verified
  admissible: no
  reasons: not-authenticated
  gaps: unknown-after-apply
  coverage: complete
check-result build/results/fresh.json
  integrity: content not verified
  admissible: no
  reasons: not-authenticated
  signature: none
check-result build/results/stale.json
  integrity: content not verified
  admissible: no
  reasons: not-authenticated, stale
  signature: none

rendering by environment
  staging: complete (1 source, 1 resource, 2 container occurrences)

4 inputs, 1 admissible
```

One block for each input, in the order `rendering`, `plan`, `plan-statement`, the statement kinds (`build-provenance`, `deployment-record`, `workload-observation`, `check-result`, `measurement`), and `quality-scenarios`, then by environment and path. A block has the kind, the environment (when it has one), the path, and then:

| Line | Meaning |
|---|---|
| `integrity` | `content not verified`, `content verified by digest`, or `producer authenticated`. A render is `content verified by digest` unless it failed. A quality scenario file is `content verified by digest`. |
| `admissible` | `yes` when a claim may rest on the input. A render is admissible unless it failed. |
| `reasons` | Every reason that the input is not admissible, for example `not-authenticated` or `stale`. For a failed render, the gap kinds. |
| `gaps` | The kinds of the gaps of a render or a plan, for example `unknown-after-apply`. |
| `coverage` | `complete`, `partial`, or `failed`, for a render or a plan. |
| `signature` | `none`, `no-producer`, `unavailable`, `failed`, or `verified`, with the producer, for a statement. |

The section `rendering by environment` gives the worst coverage of the sources of each environment with the number of sources, resources, and container occurrences, and one line `gap <kind>: <detail> [<path>]` for each gap. A section `renderers` lists the name and version of each renderer that ran. When the last `check` run wrote results with test associations of the code graph, a block `from the last check run (<results path>)` lists them: they are candidates for a check and never evidence. The command does not read the code graph, so they can be older than the working tree. The last line is the count of inputs and of admissible inputs. A workspace with no declared input prints `no assurance inputs declared`.

With `--json`, the document has `schemaVersion` (`1.0`), `inputs`, `environments`, `renderers`, `candidates`, `candidatesFrom` (only when there are candidates), and `diagnostics`. Each input has `kind`, `path`, `integrity`, `reasons`, `gaps`, `admissible`, and, when they apply, `environment`, `tool`, `coverage`, `signature`, `producer`, and `digest`. Each environment has `environment`, `coverage`, `gaps` (`kind`, `detail`, `source`, `path`), `sources`, `resources`, and `containerOccurrences`.

Because every source is rendered twice, a source whose two outputs differ gives the warning `CO-PRV-014`: the render is not deterministic, and `check` cannot rely on it. Diagnostics go to stderr as they do for `check`; with `--json` they are also in `diagnostics`.

Exit codes: 0; 2 when a declaration is not valid, or a diagnostic of severity error is among the diagnostics; 5 when a renderer is missing, not pinned, or different from the pin (`CO-PRV-003`), or an OS sandbox that the workspace requires is unavailable (`CO-PRV-004`); 130 when the command is interrupted while it renders.

## render

Projects the architecture and conformance status to a Structurizr DSL workspace. See [render diagrams](../how-to/render-diagrams.md).

```text
concordia render [--view <view>] [--focus <calm-id>] [--architecture <path>] [--workspace-out <path>] [--results <path>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--view` | `landscape`, `context`, `container`, `component`, `drift`, `deployment`, `dynamic` | all views | Limit the output to one view. |
| `--focus` | CALM id | none | Limit views to the system or container that contains this CALM node. |
| `--architecture` | path | `architecture.source` | CALM architecture file. |
| `--workspace-out` | path | `<projection.output>/workspace.dsl` | DSL output path. |
| `--results` | path | runs the check | Use a `results.json` instead of running the check. |
| `--finding` | fingerprint | none | Add a `dynamic` view of this finding. Accepts the full fingerprint or a unique hex prefix (at least 4 characters). `--view dynamic` needs it. |
| `--open` | flag | off | Open the DSL file, or the output of another format, when it is written. A failure gives `CO-PRJ-007`. |

`--format` accepts `dsl` (default), `json`, `mermaid`, `plantuml`, `dot`, and `static`. Every format except `dsl` needs the Structurizr CLI. Concordia looks for it in `STRUCTURIZR_CLI`, then for `structurizr-cli`, `structurizr.sh`, and `structurizr` on the PATH.

`--view deployment` writes the deployment views of all environments that the workspace declares (`environments`), in ID order, into the one workspace DSL under `projection.output`. It needs no `views` declaration, and it writes no manifest. Each environment is one `deploymentEnvironment` named with the environment ID, and one Structurizr view `deployment-<environment id>`. A workspace without declared environments gets one environment named `Default` that shows the placement of the CALM `deployed-in` relationships. `render` reads no placement records, so it shows the declared placement of each environment and its instances carry `concordia:unknown`; the placement records and the other status tags belong to the declared views. An environment that has no declared placement and no placement record is left out, with the warning `CO-PRJ-008` that names the environment. See [read the placement status](../how-to/render-diagrams.md#read-the-placement-status).

Outputs: `build/concordia/structurizr/workspace.dsl`; for other formats, files in `build/concordia/structurizr/<format>/`.

Exit codes: 0; 2; 5 when the Structurizr CLI is missing (`CO-PRJ-001`).

### render --views

Writes the generated views that the workspace declares under `views`, at the paths that their definitions give. See [keep diagrams current](../how-to/keep-diagrams-current.md).

```text
concordia render --views [--view-id <id>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--views` | flag | off | Write the artifact, the exports, and the manifest of each view with `origin: generated`. |
| `--view-id` | view ID | all generated views | With `--views`: write only this view and its files. An unknown ID, and the ID of an authored view, give `CO-WRK-010`. |

For each generated view `render --views` writes three kinds of file, each with an atomic replace:

| File | Path | Content |
|---|---|---|
| Artifact | `output.path` | The Structurizr DSL of the view. It has one Structurizr view whose key is the view ID. It has no timestamp, no waiver or baseline state, and no finding fingerprint. The relationship tags come from the reconciliation classification only. A view with one `scope` entry shows the system or container that contains it; a view with several entries has one Structurizr view for each system, and the first has the view ID as its key. A view that is not a deployment view has no `deployed-in` placement; a deployment view has the deployment environment of its `environment` only. |
| Exports | `output.exports[].path` | One file for each export: `json`, `mermaid`, `plantuml`, or `dot`, made by the pinned Structurizr CLI in the [confined runner](#pinned-cli-confined-rendering-and-export-formats) and canonicalized. |
| Manifest | `<output.path>.manifest.json` | Canonical JSON with the view ID, the definition version and digest, the kind, the scope, the environment, the digest of each artifact, the input digests, the semantic digest, the generator, the revisions, and the dirty-tree digest. It has no timestamp. It records `generator.structurizr` with the CLI version and `confinement: scrubbed-env` when the view has an export, and `null` otherwise. |

`render --views` runs the check pipeline, so it needs a current index: run `concordia index` first. The pipeline gives the observed evidence, the bindings, and the placement records that the observed projection and the deployment view need; a `results.json` holds none of them, so `--results` is not accepted. Concordia checks every definition against the model and the repository before it writes anything (the checks of `model validate`). An error diagnostic (`CO-DGM-001`, `CO-DGM-002`) stops the command with exit code 2 and no file is written, also for the views that `--view-id` leaves out. Every file is built in memory first, so a failed export writes no file either.

Two runs on identical inputs write byte-identical files. The manifest records the revision of each project, so a new commit changes the manifest of the next render even when no input of the view changed; [`check`](#check) compares the recomputed digests and the regenerated bytes, never the revisions. Render again after an input changed, or limit the run with `--view-id`.

`--views` does not combine with `--view`, `--focus`, `--architecture`, `--workspace-out`, `--results`, `--finding`, or `--format` (`CO-WRK-010`). `render` without `--views` is unchanged and writes `build/concordia/structurizr/workspace.dsl`. `check` and `index` never write the files of a view.

Outputs: the files of the table, listed on stderr as `view written: <path>`.

Exit codes: 0; 2 for a view definition or an output path that is not valid (`CO-DGM-001`, `CO-DGM-002`), a rendering input that is refused (`CO-DGM-003`), and a usage error (`CO-WRK-010`); 3 when the Structurizr CLI fails (`CO-PRJ-002`); 5 when the Structurizr CLI is missing (`CO-PRJ-001`) or is not the pinned version (`CO-DGM-004`).

### Pinned CLI, confined rendering, and export formats

`render --format` accepts any Structurizr CLI version. Exports that belong to a declared view are committed files, so Concordia generates and verifies them with one version only: `2025.11.09` (`projection.PinnedStructurizrCLI`). It runs `structurizr version` first. A CLI with another version fails with `CO-DGM-004` and exit code 5, and a view check reports the export as `unknown`. [`doctor`](#doctor) warns when the version differs from the pin. A new pin is a Concordia release, and the exports of every view regenerate with it.

Concordia runs the CLI for view exports in a confined process:

- A fresh scratch directory is the working directory. The DSL is copied into it and the CLI writes its output there. Concordia copies the result out and removes the directory.
- The environment is replaced. Only `PATH` and `JAVA_HOME` are inherited, and `HOME` and `TMPDIR` point to the scratch directory. No proxy variable (`HTTP_PROXY`, `HTTPS_PROXY`), credential, or `JAVA_TOOL_OPTIONS` reaches the CLI.
- Before the CLI starts, Concordia scans the DSL for `!include`, `!script`, `!plugin`, `!docs`, `!adrs`, and `!import`, and for URLs in them. A directive is refused with `CO-DGM-003` (exit code 2) and the CLI does not start, unless its path is relative, stays inside the workspace, and is listed in `projection.allowIncludes`. URLs and inline scripts are always refused. Concordia scans the allowed files for the same directives. An allowed file that cannot be read makes the export `unknown` with the reason `include not available`.

The confinement is not a sandbox. Go cannot deny network access to a child process, so the scrubbed environment is the only limit that Concordia enforces. The manifest of a view records `confinement: scrubbed-env`. To deny the network, run the CLI through the wrapper with `--network none` ([install Structurizr through Docker](../how-to/render-diagrams.md#install-the-structurizr-cli-through-docker)).

View exports accept `json`, `mermaid`, `plantuml`, and `dot`; `static` has no canonicalization rule and fails validation. Concordia canonicalizes each export before it writes or compares it:

| Format | Canonical form |
|---|---|
| `json` | Without the renderer metadata (the workspace `id`, the `structurizr.dsl` and `structurizr.inspection.*` properties, and the generation fields `lastModifiedDate`, `lastModifiedAgent`, `lastModifiedUser`, and `revision`), with sorted keys, 2-space indent, and a final newline. |
| `mermaid`, `plantuml`, `dot` | The CLI output with LF line endings. |

## lock update

Downloads the locked remote resources again, re-pins the CALM schema release and the toolchain, and updates `.concordia.lock`. When a pinned component changes, it writes a digest-impact report.

```text
concordia lock update [--uri <uri>]...
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--uri` | uri | all remote resources | Update only this resource. Repeatable. A URI that is not a locked remote resource gives `CO-WRK-010`. With `--uri`, the CALM release, the toolchain, and the report are left alone. |

Prints `updated <uri> <old> -> <new>` or `unchanged <uri>` for each resource. Without `--uri`, it also sets `resolved.calm.schemaVersion` to the CALM release of the architecture, and prints `updated calm schema <old> -> <new>` or `unchanged calm schema <version>`. An architecture that cannot be read gives `CO-INT-001`; an unsupported `$schema` gives `CO-INT-003` (exit 2). With `--offline`, or without a lockfile, the command gives `CO-WRK-010`. A failed download gives `CO-INT-013`.

Without `--uri`, the command also sets `resolved.cbm.executableVersion`, `resolved.cbm.sha256`, `resolved.cbm.profile`, `resolved.cbm.adapterSchema`, and `resolved.cbm.normalizationVersion` to what this build of Concordia finds. `resolved.cbm.sha256` is the digest of the executable file that `observation.executable` names, found on the `PATH` with symbolic links resolved. A CBM build that it cannot run leaves the CBM version, the digest, and the profile as they are.

Also without `--uri`, it pins the renderers that a declared `assurance.rendering` source uses (`helm`, `kustomize`): `resolved.renderers.<tool>` gets the version and the `sha256` of the executable that it finds. It prints `pinned <tool> <version> <digest>`, `updated <tool> <old> -> <new>`, or `unchanged <tool> <version>`, and `removed <tool> pin: no source uses it` for a pin that no source needs. A renderer that a source uses and that is missing gives `CO-PRV-003` (exit 5). See [the lock file](workspace.md#concordialock).

### Digest-impact report

When the CBM build, the CBM executable digest, the capability profile, the adapter schema, the CALM schema release, or the normalization version changed (`resolved.cbm.normalizationVersion` or `resolved.cbm.sha256` that the lock does not record counts as `unrecorded`), the command writes `build/concordia/digest-impact.json` (canonical JSON) and prints a text summary on stdout. The component of the executable digest is `cbm-executable`. The report has:

| Section | Content |
|---|---|
| `changes` | Each changed component with its old and new value. |
| `capabilities` | Present when the profile changed. The families whose level, maturity, or loss modes differ (`added`, `improved`, `reduced`, `removed`, `maturity-changed`, `loss-modes-changed`), the regressions (a family that the locked profile supported and the new one does not), the recordings that validate the changed families (`fixtures`), and all recordings of the new profile (`profileFixtures`). A profile that this build does not ship cannot be compared: `comparable` is `false` and `note` says which profile. |
| `latestRun` | The latest recorded run (the JSON output of the workspace, `build/concordia/results.json` by default), evaluated again with the new toolchain and without writing files. The digests `normalizedGraph`, `findings`, `architecture`, and `policy` before and after, the findings whose fingerprint is removed or added, and the rules whose verdict changed. The run does not record a digest of the normalized graph, so its `before` is empty and `recorded` is `false`. If the evaluation fails, `reevaluated` is `false` and `reason` says why. `null` when no run is recorded. |
| `equivalence` | `shown` and a `statement`: `equivalence shown for the latest run`, or `equivalence not shown` with the `reasons`. |

Equivalence is shown only when the capability diff has no regression and the profiles are comparable, the latest run was evaluated again, its findings digest, rule verdicts, architecture digest, and policy digest are unchanged. It says nothing about other revisions of the code. The report never changes a result: run `concordia check` to produce the results with the new toolchain.

Exit codes: 0; 2; 5 when a renderer that a source uses is missing (`CO-PRV-003`); 6 when a download fails.

## diff

Compares the architecture, bindings, observed state, and findings of two states.

```text
concordia diff [--from <side>] [--to <side>] [--kind <kind>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--from` | `worktree`, git ref, results file, or bundle (`.tar.gz`) | `HEAD` | Left side. |
| `--to` | `worktree`, git ref, results file, or bundle (`.tar.gz`) | `worktree` | Right side. |
| `--kind` | `intent`, `observed`, `bindings`, `findings`, `all` | `all` | What to compare. With `all`, kinds that one side cannot give are listed as skipped. An explicit kind that is not available gives `CO-WRK-010`. |

`--format` accepts `pretty` (default) and `json`. Any other value gives `CO-WRK-010`. The `findings` kind includes the coverage and waiver sections.

Exit codes: 0; 2.

## export bundle

Writes a conformance bundle (results, SARIF, coverage, inputs, optional graph and source snippets) as a `tar.gz` archive with a `manifest.json` of file digests.

```text
concordia export bundle [--include-graph] [--include-source-snippets none|violations] [--sign] [--sign-key <ref>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--include-graph` | flag | off | Add the observed nodes and edges of the run. |
| `--include-source-snippets` | `none`, `violations` | `none` | Add source lines around live violations. |
| `--sign` | flag | off | Sign the bundle with `cosign` (`COSIGN` or the PATH). Writes `<output>.sigstore.json`. |
| `--sign-key` | ref | keyless signing | Cosign key reference. Signing with `--offline` gives `CO-WRK-016`, with or without a key: cosign 3 records every signature in the public transparency log. |

Output: `--output`, else `build/concordia/conformance.tar.gz`. Prints the path and `sha256:<hex>`.

The manifest records the source revisions and dirty state, the CALM release and digest, policy versions and digests, graph snapshot identifiers, the workspace profile, the selected environments, exception registers, evaluators, the build digest, and timestamps under `provenance`. It also has `integrity` (`algorithm`, a digest for each file in `files`, `manifestDigest`, `level`), `references` (for each content-addressed input: `kind`, `digest`, `availability` `included` or `unavailable`, and `reason` `not-included`, `missing`, or `not-reproducible`), `reproduction` (`available` is false when a reference is unavailable, for example the graph without `--include-graph`), `redactions` (one entry for each place where a value was removed: `path`, `file`, `line`, `property`, `class`, `count`; empty when nothing was redacted), and `redactionRules` (the version of the redaction rule set). `export bundle` prints a `CO-REP-005` notice with the count for each file that has redactions. See [Redaction](redaction.md).

A workspace that declares `views` adds the files of each view below `views/<id>/`: `manifest.json` (the committed manifest of a generated view), `projection.json` (the semantic projection of the run: `model`, `observed` or `null`, `semanticDigest`, `digestContract`, `projectionContract`), and the artifacts (the DSL and exports of a generated view, or the source of an authored view) at `artifacts/<repository path>`. The committed bytes come from the tracked revision, never from the working tree. The manifest key `views` lists each view with `id`, `origin`, `state`, and `files` (`role`, `path`, `state`, and for a present file `file` and `sha256`). A declared file that the tracked revision does not hold has the state `absent` and no content, and the view has the state `absent`. A workspace without views has no `views` key.

A workspace that evaluates assessments (an `assessments` key) adds the review evaluation of the run: the assessment and assumption files as `assessments/<repository path>`, the approval and revocation files as `approvals/<repository path>`, and `verification/assessments.json`, which holds the verification result of each decision record (`applied`, `mechanism`, `authority`, `signer`, `reason`), the authority configuration that verification used, the trust anchor (`ref` and `revision`), the digest contract, the currency of each assessment version, and `notReproducible`. The manifest `provenance.review` records the contract version, the capability profile, the authority digest, and the trust anchor revision. The files pass through redaction, so no secret value reaches the bundle. An evidence reference of an assessment that no longer resolves (the file is missing, or its content differs from the digest that the document records) is a reference of the kind `assessment-evidence` with the reason `not-reproducible`: `reproduction.available` is false and a historical reproduction is not claimed for it. A workspace with no `assessments` key adds none of these.

`export bundle` refuses a workspace whose latest `check` did not finish (the run manifest status is not `complete`): `CO-REP-004`, exit 2.

Exit codes: 0, also when the run has violations; 2 (`CO-REP-004`); 5 when `cosign` is missing (`CO-WRK-014`) or signing fails (`CO-REP-003`); 6.

## export verify

Checks a conformance bundle: the digest of each file listed in the manifest, that no file is missing or extra, `integrity.files`, and `integrity.manifestDigest`.

```text
concordia export verify <bundle> [--verify-signature] [--key <ref>] [--certificate-identity <id>] [--certificate-oidc-issuer <url>] [--trusted-root <file>]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--verify-signature` | flag | off | Verify `<bundle>.sigstore.json` with `cosign verify-blob` (`COSIGN` or the PATH). |
| `--key` | ref | keyless verification | Cosign public key reference. |
| `--certificate-identity` | id | none | Signer identity for keyless verification. |
| `--certificate-oidc-issuer` | url | none | OIDC issuer for keyless verification. |
| `--trusted-root` | path | fetched by cosign | Sigstore trusted root file. cosign 3 fetches the trusted root over the network to verify any signature; with this file it verifies offline. Required with `--offline`. |

Output: `<bundle>: content verified by digest` when the digests match. A digest check is not authentication: with `--verify-signature` and a verified signature, the line is `<bundle>: producer authenticated`.

Exit codes: 0; 2 (`CO-REP-004`) for a changed, missing, or extra file (the diagnostic names the file), a manifest that does not match its digest, a missing signature file, or a signature that does not verify; 5 when `cosign` is missing (`CO-WRK-014`); 6 (`CO-WRK-016`) for `--verify-signature` with `--offline` and no `--trusted-root`.

## watch

Indexes and checks again when the source changes, and prints the transitions.

```text
concordia watch [--rules changed|all] [--debounce <ms>] [--render]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--rules` | `changed`, `all` | `all` | With `changed`, each cycle runs `check --changed HEAD`. |
| `--debounce` | ms | `1000` | Poll interval. Less than 200 gives `CO-WRK-010`. |
| `--render` | flag | off | Write the Structurizr DSL after each cycle. |

`--format` accepts `pretty` (default) and `json` (one JSON object for each line). Other values give `CO-WRK-010`. An error in a cycle is written to stderr and the loop continues.

Exit codes: 130 when interrupted; 2 when the workspace is not valid.

## clean

Removes caches, generated files, or the CBM project.

```text
concordia clean [--cache] [--generated] [--project [name]] [--all] [--yes]
```

| Option | Value | Default | Description |
|---|---|---|---|
| `--cache` | flag | off | Remove `.concordia/`. |
| `--generated` | flag | off | Remove `build/concordia/`. |
| `--project` | optional name | all workspace projects | Delete the CBM project (without a name, all workspace projects). |
| `--all` | flag | off | Do all three actions. |
| `--yes` | flag | off | Do not ask for confirmation. |

Outputs: none besides the removals.

Exit codes: 0.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | ok |
| 1 | violation |
| 2 | invalid input |
| 3 | adapter failure, including a stale graph (`CO-OBS-008`) |
| 4 | unknown verdict or coverage shortfall |
| 5 | toolchain missing or incompatible |
| 6 | network or lock problem |
| 7 | internal error |
| 130 | interrupted |

The exit code of `check` is computed in this order: 7 if any rule has the verdict `error`; 1 if any finding fails at the `failOn` severity; 4 if any rule is `unknown` and its policy is `fail`; otherwise 0. Only rules and findings whose [enforcement mode](../how-to/run-in-ci.md#gate-by-environment) is `mandatory` count for 1, 4, and 7. A rule that declares no mode is `mandatory`, except a typed rule (a rule with an `obligation` block), which is `advisory`. The report areas do not change the exit code: an `unknown` area changes `summary.verdict` and is named in the output, and the rules of the area give the exit code. The per-code exit codes of diagnostics are in [diagnostics](diagnostics.md).

## Environment variables

| Variable | Effect |
|---|---|
| `CONCORDIA_NOW` | Fixed clock (ISO 8601 timestamp). Used for waiver expiry and recorded times. |
| `CONCORDIA_CBM_DOWNLOAD_URL` | https directory that `cbm install` downloads the pinned archive from, instead of the GitHub release. |
| `STRUCTURIZR_CLI` | Path to the Structurizr CLI. |
| `HELM`, `KUSTOMIZE` | Path to the Helm or Kustomize executable that renders an `assurance.rendering` source. Without it, the tool is searched on the `PATH`. The render needs a pin in the lock (`lock update`). |
| `COSIGN` | Path to the cosign executable that verifies statements and signatures. Without it, cosign is searched on the `PATH`. |
| `NO_COLOR` | Turns off color. |
| `CI` | When set, `baseline update --accept` is refused (`CO-EXC-008`). |

## Related

- [Workspace reference](workspace.md) for the keys that supply the defaults above.
- [Run in CI](../how-to/run-in-ci.md) for exit codes in pipelines.
- [Getting started](../tutorials/getting-started.md) to run these commands in order.
