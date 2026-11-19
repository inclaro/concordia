# Diagnostics reference

Concordia reports every problem as a diagnostic with a stable code. This page lists all 151 codes in the catalog (`internal/diag/codes.go`).

Diagnostic codes (`CO-<area>-<number>`) are a separate namespace from rule IDs such as `CO-BND-001`, which name policy rules and appear in policy packs. The code `CO-CON-021` is a diagnostic reason; the rule `CO-COV-001` is a rule ID.

## Columns

- **Severity**: the default severity: `error`, `warning`, or `info`.
- **Exit code**: the process exit code when the diagnostic stops the run. `none` means the code never stops a run by itself (it appears in the `diagnostics` array of the result, or as a rule reason).
- **Message template**: `{name}` placeholders stand for values that Concordia fills in.

Exit codes: 0 ok, 1 violation, 2 invalid input, 3 adapter failure (including a stale graph, `CO-OBS-008`), 4 unknown verdict or coverage shortfall, 5 toolchain missing or incompatible, 6 network or lock problem, 7 internal error, 130 interrupted. See [CLI reference](cli.md).

## Workspace (WRK)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-WRK-001` | error | 2 | workspace file not found: {path} |
| `CO-WRK-002` | error | 2 | workspace not valid: {detail} |
| `CO-WRK-003` | warning | none | lockfile does not agree with the found toolchain: {detail} |
| `CO-WRK-004` | error | 2 | profile not valid: unknown profile '{profile}' |
| `CO-WRK-005` | warning | none | existing file not overwritten: {path} |
| `CO-WRK-010` | error | 2 | usage error: {detail} |
| `CO-WRK-011` | error | 2 | destructive action needs confirmation: {detail} |
| `CO-WRK-012` | error | 5 | git not available or git command failed: {detail} |
| `CO-WRK-013` | warning | none | no git revision for project {project}: the project root is not in a git repository or has no commit |
| `CO-WRK-014` | error | 5 | cosign not available: {detail} |
| `CO-WRK-015` | error | 2 | git reference not valid: {detail} |
| `CO-WRK-016` | error | 6 | cosign needs network access, and --offline is set: {detail} |
| `CO-WRK-017` | error | 2 | environment not declared: {detail} |
| `CO-WRK-018` | error | 2 | check.rollback expired on {expires}: {detail} |
| `CO-WRK-099` | error | 7 | internal error: {detail} |

`CO-WRK-003` is a warning about the toolchain. Its detail names what differs: the CBM version, the capability profile, the CALM schema release, or the digest of the CBM executable (`CBM executable <path> has digest <found>, lock has <locked>`). It never stops a run. Run `concordia lock update` to accept the found toolchain. A lock without `resolved.cbm.sha256` gives no digest warning.

## Intent / architecture (INT)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-INT-001` | error | 2 | CALM document cannot be read: {detail} |
| `CO-INT-002` | error | 2 | CALM schema violation: {detail} |
| `CO-INT-003` | error | 2 | CALM schema version not supported: {schema} |
| `CO-INT-004` | error | 2 | duplicate unique-id: {id} |
| `CO-INT-005` | error | 2 | relationship endpoint not found: {id} |
| `CO-INT-006` | error | 2 | pattern violation: {detail} |
| `CO-INT-007` | error | 2 | Concordia extension not valid: {detail} |
| `CO-INT-008` | error | 2 | CALM decorator not valid: {detail} |
| `CO-INT-009` | error | 2 | decorator applies-to refers to an unknown unique-id: {id} |
| `CO-INT-010` | warning | none | decorator does not target the architecture: {path} |
| `CO-INT-012` | error | 6 | remote reference not resolved or not locked: {detail} |
| `CO-INT-013` | error | 6 | remote resource not fetched: {detail} |
| `CO-INT-014` | error | 2 | CALM rule violation: {detail} |
| `CO-INT-015` | warning | none | CALM rule warning: {detail} |

A diagnostic of `model validate` that a rule of the CALM CLI (`calm validate`) also reports has a `rule` field with the CALM CLI rule ID, for example `unique-ids-must-be-unique-in-architecture` or `json-schema`. `CO-INT-014` and `CO-INT-015` are the CALM CLI rules that have no older Concordia code; the severity follows the CALM CLI rule. A diagnostic without `rule` is Concordia's own.

## Observation (OBS)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-OBS-001` | error | 3 | CBM call failed: {detail} |
| `CO-OBS-002` | error | 3 | project not indexed, not ready, or index timeout: {detail} |
| `CO-OBS-003` | error | 5 | CBM executable missing: {executable} |
| `CO-OBS-004` | error | 5 | CBM version not compatible: found {version}, supported 0.12.x |
| `CO-OBS-005` | error | 5 | another CBM build is installed: {path} has {found}, Concordia is tested with {version} ({pinned}) |
| `CO-OBS-006` | error | 6 | pinned CBM build not installed: {detail} |
| `CO-OBS-008` | error | 3 | graph generation stale or not stable: {detail} |
| `CO-OBS-010` | error | 3 | query row count does not agree: {detail} |
| `CO-OBS-011` | error | 3 | path cannot be put in a query: {path} |
| `CO-OBS-012` | error | 2 | runtime trace file not valid: {detail} |
| `CO-OBS-013` | warning | none | runtime spans skipped: {detail} |
| `CO-OBS-014` | warning | none | cross-project matching not done for {project}: {detail} |
| `CO-OBS-015` | warning | none | cross-project edges without a resolved target: {count} |
| `CO-OBS-016` | warning | none | native edge kind kept as unsupported evidence: {kind} ({count} edges) |
| `CO-OBS-017` | warning | none | capability profile {profile}: {detail} |
| `CO-OBS-018` | warning | none | scope failure in project {project}: {detail}; its coverage is failed and the run continues |
| `CO-OBS-019` | warning | none | native edges kept as ambiguous evidence: {kind} ({count} edges): {detail} |
| `CO-OBS-020` | warning | none | environment {environment} source {source} is under the provider default-excluded directory {directory}: {hint} |
| `CO-OBS-021` | warning | none | competing declarations of {id}: {locations}: {detail} |

`CO-OBS-001` also reports a CBM process that timed out, exited, or gave an answer that Concordia cannot read; the detail ends with the stderr of CBM. When CBM refuses to start because the executable is another build than the running CBM daemon, the detail says so and tells you to point every CBM client at one binary; Concordia does not retry that failure. See [observation](workspace.md#observation).

`CO-OBS-005` is the refusal of `concordia cbm install`. A CBM executable is already on the `PATH` or in the install directory, and it is not the pinned build. Concordia never overwrites it. CBM runs one daemon for each user, and a second build is refused while the daemon runs, so point your agents and Concordia at one binary. See [CBM and your AI agents](../explanation/cbm-and-agents.md). `CO-OBS-006` is a download or check failure of `concordia cbm install`: the platform has no pinned build, the download failed, or the digest of the archive or of the binary is not the pinned digest. Nothing is installed. Set `CONCORDIA_CBM_DOWNLOAD_URL` to use a mirror. `doctor` reports a CBM build that is not the pinned build as `degraded`, with no diagnostic code.

## Bindings (BND)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-BND-001` | error | 2 | binding refers to an unknown CALM ID: {id} |
| `CO-BND-002` | error | 2 | binding selector not valid: {detail} |
| `CO-BND-003` | error | 2 | ambiguous exclusive binding: {detail} |
| `CO-BND-004` | error | 2 | required binding matched zero entities: {id} |
| `CO-BND-005` | warning | none | observed entity has no binding: {path} |
| `CO-BND-006` | error | 2 | duplicate binding ID: {id} |
| `CO-BND-008` | error | 2 | binding with cardinality one matched more than one entity: {detail} |
| `CO-BND-009` | warning | none | ambiguous non-exclusive bindings: {detail} |

## Policies (POL)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-POL-001` | error | 2 | policy not valid: {detail} |
| `CO-POL-002` | error | 2 | selector, predicate, or traversal not supported: {detail} |
| `CO-POL-003` | error | 2 | duplicate rule ID: {id} |
| `CO-POL-004` | error | 2 | unknown edge kind: {kind} |
| `CO-POL-005` | error | 2 | unknown CALM ID in a selector or control: {id} |
| `CO-POL-009` | warning | none | evidence kind not supported by the observation provider: {kind} (rule {ruleId}) |
| `CO-POL-010` | error | 2 | policy pack digest does not agree with its pin: {detail} |
| `CO-POL-011` | error | 2 | policy pack file not found or not readable: {path} |
| `CO-POL-012` | error | 2 | unknown built-in policy pack: {name} |
| `CO-POL-013` | error | 2 | policy pack pin not valid: {detail} |
| `CO-POL-014` | error | 2 | policy pack source must be a local file or a built-in pack: {source} |
| `CO-POL-015` | error | 2 | rule override names a rule that the pack does not have: {id} (pack {pack}) |
| `CO-POL-016` | error | 6 | policy pack not fetched: {detail} |
| `CO-POL-017` | error | 2 | policy conflict: {detail} |
| `CO-POL-018` | error | 2 | enforcement not valid: rule {rule} is mandatory in {environment}, but a guarantee that it depends on is absent: {guarantee} |

The pack codes `CO-POL-010` to `CO-POL-016` come from `policies.packs` and `concordia policy`:

- `CO-POL-010`: the digest of a pack file is not the `sha256` in the workspace. Review the change. If you trust it, run `concordia policy add <file> --force` to pin the new digest.
- `CO-POL-011`: the pack file does not exist, or Concordia cannot read it.
- `CO-POL-012`: `builtin:<name>` names no pack in the binary. Run `concordia policy list` to see the names.
- `CO-POL-013`: a file pack has no `sha256`, or a built-in pack has one. A built-in pack is pinned by the binary.
- `CO-POL-014`: `use` is a URL. Vendor the pack with `concordia policy add <url>`, then use the vendored file.
- `CO-POL-015`: an entry in `rules` has a rule ID that the pack does not declare. This guards against typos.
- `CO-POL-016`: `concordia policy add <url>` could not fetch the pack, or `--offline` is set. Only `policy add` uses the network.

A rule ID that two packs (or a pack and a policy file) declare gives `CO-POL-003`. A pack whose digest differs from `.concordia.lock` gives `CO-INT-012`, as for a resource.

`CO-POL-017` stops `check` before any evaluation. Two rules with a typed obligation contradict each other for the same subject, environment, and relation: one requires or allows it and the other forbids it, and their `applies` selectors can match the same occurrence. No rule has implicit priority and the first match never wins. List both rules in the pack `precedence` (the rule listed first wins, and the other rule stays visible in the results as superseded) or change one of the rules. Two rules whose `applies` selectors cannot match the same occurrence (for example `protocol: https` and `protocol: administrative`) do not conflict.

`CO-POL-018` stops `check` before any evaluation and produces no gate result. A rule runs as `mandatory` (from `check.enforcement`, or its own `enforcement`), and a guarantee that it depends on is absent in an environment. The message names the rule, the environment, and the guarantee: `environment attribution missing` (the environment is not declared under `environments`, or the workspace declares none), `family <name> has maturity advisory` (the provider profile does not mark the evidence family `stable`), or `coverage predicate <family> missing` (the run has no coverage record for the family). Concordia never lowers the mode: set `advisory` or `shadow` in `check.enforcement`, or provide the guarantee. See [roll out new obligations](../how-to/roll-out-new-obligations.md).

## Conformance (CON)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-CON-021` | warning | none | minimum coverage not met: {ratio} < {required} |
| `CO-CON-022` | info | none | rule selects no subject |
| `CO-CON-023` | warning | none | evidence below the confidence threshold: {count} edge(s) |
| `CO-CON-024` | warning | none | empty rule scope |
| `CO-CON-025` | info | none | rule selects no target |
| `CO-CON-026` | warning | none | language coverage not met: {language} {ratio} < {required} |
| `CO-CON-027` | info | none | rules not reducible to changed code, full scope reported: {rules} |
| `CO-CON-028` | warning | none | byte coverage not met: {ratio} < {required} |
| `CO-CON-029` | info | none | the changed scope has no file |
| `CO-CON-030` | warning | none | limit exceeded: {limit} is configured as {configured}, observed {observed}; dependent results are unknown, no evidence was cut |
| `CO-CON-031` | info | none | unknown because the family {family} is unsupported by the provider profile |
| `CO-CON-032` | warning | none | predicate coverage not complete: {predicate} is {status} in {scope}: {detail} |
| `CO-CON-033` | warning | none | edges skipped from the evaluation: {count} with {reason} |
| `CO-CON-099` | error | 7 | rule evaluation error: {detail} |

## Exceptions (EXC)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-EXC-001` | error | 2 | baseline not valid: {detail} |
| `CO-EXC-002` | error | 2 | waiver not valid: {detail} |
| `CO-EXC-003` | warning | none | orphaned waiver: {id} matches no current finding |
| `CO-EXC-004` | error | 2 | duplicate waiver ID: {id} |
| `CO-EXC-006` | warning | none | waiver expired: {id} (expired {expires}) |
| `CO-EXC-007` | warning | none | waiver expires soon: {id} (expires {expires}) |
| `CO-EXC-008` | error | 2 | baseline accept refused in CI: new findings never enter the baseline in CI |
| `CO-EXC-009` | warning | none | scoped waiver not applied: {detail} |
| `CO-EXC-010` | warning | none | scoped waiver matches no current finding: {detail} |
| `CO-EXC-011` | warning | none | compensating evidence unresolved or digest differs: {detail} |

`CO-EXC-005` is reserved. It is not in the catalog, Concordia never emits it, and no later change reuses the number for another meaning, so a stored reference to the code keeps its meaning. New exception codes start at `CO-EXC-009`.

- `CO-EXC-009` covers a scoped waiver whose approver is not verified. The waiver does not apply, and the finding keeps its disposition.
- `CO-EXC-010` covers a scoped waiver that no current finding matches.
- `CO-EXC-011` covers a compensating evidence reference that does not resolve or whose digest differs from the declared `sha256`.

## Projection (PRJ)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-PRJ-001` | error | 5 | Structurizr CLI not available; it is needed for {format} |
| `CO-PRJ-002` | error | 3 | Structurizr validate or export failed: {detail} |
| `CO-PRJ-005` | error | 2 | C4 level mapping not valid: {detail} |
| `CO-PRJ-006` | info | none | element not projected: {id} ({detail}) |
| `CO-PRJ-007` | warning | none | cannot open the output: {detail} |
| `CO-PRJ-008` | warning | none | view not generated: {detail} |

## Diagrams (DGM)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-DGM-001` | error | 2 | view definition not valid: {view}: {detail} |
| `CO-DGM-002` | error | 2 | view output path not allowed: {view}: {path}: {detail} |
| `CO-DGM-003` | error | 2 | rendering input not allowed: {detail} |
| `CO-DGM-004` | error | 5 | Structurizr CLI is not the pinned version: {detail} |
| `CO-DGM-005` | info | none | suggested view not declared: {detail} |

- `CO-DGM-003` covers a rendering input that the confined renderer refuses.
- `CO-DGM-004` covers a Structurizr CLI whose version is not the pinned one; the export becomes `unknown`.

## ADR governance (ADR)

| Code | Severity | Exit code | Message template |
| --- | --- | --- | --- |
| `CO-ADR-001` | error | 2 | decision register not valid: {detail} |
| `CO-ADR-002` | error | 2 | duplicate registered decision ID: {id} |
| `CO-ADR-003` | error | 2 | governs reference unknown or selector empty: {detail} |
| `CO-ADR-004` | error | 2 | enforcedBy or released rule or pack version not loaded: {detail} |
| `CO-ADR-005` | error | 2 | lifecycle profile or trigger not valid: {detail} |
| `CO-ADR-006` | warning | none | provider has no ADR capability, or revision unavailable: {detail} |
| `CO-ADR-007` | info | none | decision register configured but the adr-governance pack is not enabled |
| `CO-ADR-008` | info | none | unregistered or mention-based decision candidate: {detail} |
| `CO-ADR-009` | info | none | scope triggers configured but no change set: {detail} |

- `CO-ADR-006` covers a provider without ADR evidence and a project without a git revision for the committed-presence check.
- `CO-ADR-007`, `CO-ADR-008`, and `CO-ADR-009` are informational and never change a verdict.
- `CO-ADR-004` and `CO-ADR-005` stop the run before evaluation.

## Assessment currency (ASM)

| Code | Severity | Exit code | Message template |
| --- | --- | --- | --- |
| `CO-ASM-001` | error | 2 | assessment document not valid: {detail} |
| `CO-ASM-002` | error | 2 | approved assessment version edited: {detail} |
| `CO-ASM-003` | error | 2 | authored state claims an approval: {detail} |
| `CO-ASM-004` | error | 2 | mandatory review rules need an authority and a trust anchor: {detail} |
| `CO-ASM-005` | error | 2 | assessment approve or revoke refused: {detail} |
| `CO-ASM-006` | warning | none | approval not applied: {detail} |
| `CO-ASM-007` | warning | none | trust anchor unreadable or not an ancestor of HEAD: {detail} |
| `CO-ASM-008` | warning | none | incomplete change-impact analysis: {detail} |
| `CO-ASM-009` | info | none | traversal cycle seen: {detail} |
| `CO-ASM-010` | info | none | incremental cache not usable, full evaluation used: {detail} |
| `CO-ASM-011` | warning | none | signature verification unavailable: {detail} |

- `CO-ASM-001` covers an invalid assessment, assumption, approval, or revocation document, an unknown reference, and a duplicate id and version.
- `CO-ASM-005` covers `assessment approve` or `assessment revoke` in CI, and a missing reviewer or confirmation digest.
- `CO-ASM-006` covers an approval that is not on the trust anchor, whose reviewer or signer the authority does not list, or whose digests do not match.
- `CO-ASM-011` is a signature check that cannot run, for example any signed approval with `--offline` (cosign 3 fetches the Sigstore trusted root). The currency of the affected assessment is `unknown` with the reason `verification-unavailable`; the run never exits 6 for it.

## Trust context (SEC)

| Code | Severity | Exit code | Message template |
| --- | --- | --- | --- |
| `CO-SEC-001` | error | 2 | trust declaration not valid: {detail} |
| `CO-SEC-002` | warning | none | dependency kind unsupported by the capability profile: {detail} |

- `CO-SEC-001` covers an invalid trust declaration, a dimension that neither the built-in list nor `trust.dimensions` declares, and a classification outside `trust.classifications`.

## Assurance evidence (PRV)

| Code | Severity | Exit code | Message template |
| --- | --- | --- | --- |
| `CO-PRV-001` | error | 2 | assurance declaration not valid: {detail} |
| `CO-PRV-002` | error | 2 | assurance path outside the workspace root: {path} |
| `CO-PRV-003` | error | 5 | renderer missing, not pinned, or different from the pin: {detail} |
| `CO-PRV-004` | error | 5 | OS sandbox required and unavailable: {detail} |
| `CO-PRV-005` | warning | none | render failed, partial, or over a limit: {detail} |
| `CO-PRV-006` | warning | none | embedded command or remote include ignored: {detail} |
| `CO-PRV-007` | warning | none | duplicate resource identity in one render: {detail} |
| `CO-PRV-008` | warning | none | plan format version unsupported or plan unparseable: {detail} |
| `CO-PRV-009` | warning | none | statement malformed, signature failed, or subject digest mismatch: {detail} |
| `CO-PRV-010` | info | none | statement not admissible: {detail} |
| `CO-PRV-011` | warning | none | evidence glob matches no file: {path} |
| `CO-PRV-012` | warning | none | conflicting admissible results: {detail} |
| `CO-PRV-013` | warning | none | evaluator input incomplete or construct unsupported: {detail} |
| `CO-PRV-014` | warning | none | render output differs between two renders: {detail} |

- Configuration problems (`CO-PRV-001`, `CO-PRV-002`) stop the run with exit 2. Content problems in one evidence source are warnings: the source becomes a coverage gap or an inadmissible statement, the claims that need it are `unknown`, and independent claims continue.
- An `environment` in an `assurance` entry that is not declared is `CO-WRK-017`. A missing `cosign` is `CO-WRK-014`.

## Quality evaluation (QUA)

| Code | Severity | Exit code | Message template |
| --- | --- | --- | --- |
| `CO-QUA-001` | error | 2 | quality scenario file not valid: {detail} |
| `CO-QUA-002` | error | 2 | quality scenario reference unresolved: {detail} |
| `CO-QUA-003` | warning | none | measurement malformed or not admissible: {detail} |

- `CO-QUA-001` and `CO-QUA-002` stop the run with exit code 2. `CO-QUA-003` is a warning: the scenario that needs the measurement is `unknown`.

## Reporting (REP)

| Code | Severity | Exit code | Message template |
|---|---|---|---|
| `CO-REP-001` | error | 7 | output write failed: {path}: {detail} |
| `CO-REP-002` | error | 7 | SARIF serialization failed: {detail} |
| `CO-REP-003` | error | 5 | bundle not signed: {detail} |
| `CO-REP-004` | error | 2 | bundle not valid: {detail} |
| `CO-REP-005` | info | none | redaction applied: {count} value(s) removed from {output} |
| `CO-REP-006` | error | 2 | path refused: {path}: {detail} |

## Related

- [Results reference](results.md): where diagnostics and rule reasons appear in `results.json`.
- [CLI reference](cli.md): exit codes and options.
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md): fixes for the `CO-CON` and `CO-POL` reasons.
- [Run in CI](../how-to/run-in-ci.md): handling `CO-OBS-008` and lock problems.
