# Results reference

This page describes the output of `concordia check`: the `results.json` document, verdicts, the exit code calculation, reconciliation classes, the report formats (pretty, JUnit, SARIF), finding fingerprints, and the run manifest.

## Output files

`check` always writes the canonical JSON result. It also writes each format listed in `report.outputs` of the [workspace file](workspace.md), the run manifest, and, with `--evidence-dir`, one evidence file for each finding.

| File | Content |
|---|---|
| `build/concordia/results.json` | The canonical result (`schemaVersion` `"1.1"`). Written even when `report.outputs` does not list it (the default `report.outputs` is this one file). |
| `.concordia/runs/<runId>/manifest.json` | The run manifest. |
| `<evidence-dir>/<fingerprint hex>.json` | One file for each finding (`--evidence-dir`). |

The commands `explain`, `render`, and `baseline` read the same result but write no reports.

## `results.json`

The JSON writer uses canonical JSON. All collections are sorted:

| Collection | Sort key |
|---|---|
| `rules` | rule ID |
| `findings` | `(ruleId, fingerprint)` |
| `reconciliation` | `(from, to)` |
| `controls` | `(controlId, scope, pointer)` |
| `diagnostics` | `(code, source, message)` |
| `views` | view ID |

### Top-level fields

| Field | Description |
|---|---|
| `schemaVersion` | Always `"1.1"`. Version 1.1 added the coverage fields `files.bytes`, `files.unsupported`, `edgeKinds` and `unresolvedReferences`, the coverage section of the run, the finding fields for typed obligations and the OUT-02 fields, `areas`, `deploymentTrace`, `observability`, and `summary.resultDigest`. The `views` array and the finding kinds of the views are additive: they do not change the version. The same holds for the keys of [ADR governance](adr-governance.md): the area content of `areas.adrGovernance`, the top-level `decisionSummary`, `inputs.decisions`, and the finding key `decision`. A new optional key never changes `schemaVersion`; a key that is removed or renamed, or a changed meaning of an existing key, does. A result with none of the new keys is the result of before. The embedded JSON Schema of version 1.1 is `internal/conformance/schemas/results.schema.json`; the tests validate the results of the commerce and infra fixture runs against it. |
| `run` | Run identity: `id`, `startedAt`, `finishedAt` (ISO 8601 UTC), `sourceRevision` (git `HEAD`, or `null` outside git), `dirtyTreeDigest` (`sha256:<hex>`), `graphGeneration` (`cbm:<project>:<16 hex>`), `project`, `host` (`platform`, `arch`, `go`; `go` is the Go runtime version, for example `go1.26.8`). |
| `toolchain` | `concordia` (version), `calmSchema` (the CALM release of the architecture: `"1.2"` or `"1.3"`), `cbm` (`executableVersion`, `sha256`, `adapterSchema`, and `profile`), and `structurizr`. `cbm.sha256` is `sha256:<hex>` of the CBM executable file that served the run (the resolved file, as `doctor` shows it); it is left out when the client has no executable file, so a run of before has none. `profile` is `{"applies": true, "id", "digest"}` for the [capability profile](capability-profiles.md) of the build, or `{"applies": false}` when no profile applies; the field is never left out. `structurizr` is the version of the Structurizr CLI that regenerated a [view](../how-to/keep-diagrams-current.md) export in the run; it is left out when no export was verified. The manifest has the same `toolchain`. The evaluation bundle records the digest as `provenance.graph.cbmSha256`. |
| `toolchain.assurance` | The renderers, cosign, evaluators, and digests that the assurance evidence used; see [`toolchain.assurance`](#toolchainassurance). Left out without an `assurance` key. |
| `inputs` | Digest of each material input; see [Inputs](#inputs). |
| `summary` | Totals, summary verdict, and exit code; see [Summary](#summary). |
| `rules` | One entry for each rule that ran or was skipped; see [Rules](#rules). |
| `findings` | One entry for each violation; see [Findings](#findings). |
| `reconciliation` | One entry for each architecture pair; see [Reconciliation classes](#reconciliation-classes). |
| `controls` | One entry for each control attachment: `controlId`, `scope`, `ruleIds`, `checkIds` (when the control lists checks), `verdict` (the worst verdict of the control's rules that ran and of its checks; a control with checks and no rules is judged from its checks alone; `not-applicable` when neither ran), and `reasons` for its `unknown` or `error` checks. See [Checks of a control](#checks-of-a-control). |
| `assurance` | The evidence of the assurance adapters; see [Assurance](#assurance). Left out unless the workspace has an `assurance` key. |
| `exceptions` | `resolvedBaselineEntries` (baseline fingerprints with no current finding), `orphanedWaivers` (waiver IDs with no current finding), `waivers` (the waivers that findings refer to), and `scopedWaivers` (present when the register has a scoped waiver: for each one `id`, `rule`, `scope`, `owner`, `expires`, `state` (`active` or `expired`), `digest`, `approver` (`identity`, `authority`), `verification` (`verified`, `mechanism`, `authority`, `signer`, `reason`, `detail`), `compensatingEvidence` (`path`, `sha256`, and `state`: `resolved`, `unresolved`, or `digest-mismatch`), and `coveredFindings`, the fingerprints of the findings it covered; it holds no waiver text). Only rules that ran count. |
| `diagnostics` | Warnings and notes of the run. Errors stop the run and are not part of the result. See [Diagnostics reference](diagnostics.md). |
| `coverage` | The coverage section of the run; see [Coverage](#coverage). |
| `areas` | The six report areas, each with its own verdict and counts; see [Areas](#areas). |
| `deploymentTrace` | The declared-level trace from each service to its workloads, images, build definitions, and source evidence; see [Deployment trace](#deployment-trace). |
| `observability` | The counters of the run; see [Observability counters](#observability-counters). |
| `views` | One entry for each view that the workspace declares under `views`; see [Views](#views). Left out when the workspace declares none. |
| `decisionSummary` | The per-decision detail of the ADR governance area; see [Decision summary](#decision-summary). Left out unless the workspace has a decision register and the `adr-governance` pack runs. |
| `trust` | The boundary crossings of the relationships, flow steps, and observed flows; see [Trust crossings](#trust-crossings). Left out unless a node, relationship, or binding declares a trust value. |

### Inputs

Each input has `path` (workspace-relative, POSIX) and `digest` (`sha256:<hex>`).

| Field | Description |
|---|---|
| `workspace` | The workspace file. |
| `lock` | `.concordia.lock`, when present. |
| `architecture` | The CALM architecture file. |
| `patterns` | Pattern files. |
| `decorators` | Decorator files. |
| `policies` | One item for each policy pack file. A built-in pack has the path `builtin:<name>` and the digest of its embedded text. |
| `bindings` | One item for each binding file. |
| `baseline` | Present when a baseline was applied. |
| `waivers` | Present when a waiver register was applied. |
| `queryPlanDigest` | `sha256` of the sorted list of the CBM query digests. |
| `environments` | `{selected, excluded, digest}`. `selected` and `excluded` are environment ids in declaration order (`check --environment` chooses `selected`; without it all declared environments are selected). Excluded environments have no verdict. `digest` is the `sha256` of the canonical JSON of the declared environments. |
| `indexResults` | One item for each index result file that the run read (`build/concordia/index-result.json`). |

### Summary

| Field | Description |
|---|---|
| `verdict` | The worst rule verdict, in the order `error`, `violation`, `unknown`, `pass`, `not-applicable`. `not-applicable` when there are no rules. |
| `exitCode` | The process exit code; see [Exit code calculation](#exit-code-calculation). |
| `failOn` | The severity threshold (`--fail-on`, else `check.failOn`). |
| `unknownPolicy` | The unknown policy of the run (`--unknown`, else `check.unknown`). Each rule has its own effective `onUnknown`. |
| `active`, `baselined`, `waived`, `expiredWaiver` | Number of findings with each disposition. |
| `unknownRules` | Number of rules with the verdict `unknown`. |
| `rules` | Rule counts: `pass`, `violation`, `unknown`, `notApplicable`, `error`. |
| `resultDigest` | `sha256:<hex>` of the canonical JSON of the run with the volatile fields zeroed; see [Result digest](#result-digest). |

### Rules

| Field | Description |
|---|---|
| `ruleId`, `name`, `severity` | Rule identity and severity (`error`, `warning`, `info`). |
| `policy` | `{ id, version }` of the policy pack. |
| `family` | The predicate family. |
| `verdict` | `pass`, `violation`, `unknown`, `not-applicable`, or `error`. |
| `reasons` | List of `{ code, message }`. For `unknown`, `not-applicable`, and `error` verdicts, `code` is a [diagnostic code](diagnostics.md). |
| `subjects`, `targets` | Resolved CALM IDs, sorted. |
| `plan` | The compiled rule plan: traversal, predicate, `evidenceRequirements`, `unsupportedEdgeKinds`. |
| `coverage` | Always present for `pass`; absent for `not-applicable` and `error`. Fields: `files` (`total`, `covered`, `ratio`, `excluded`, `stale`, `unindexed`, `unsupported` (files in scope whose language CBM has no File node for), `languages`, `bytes` (`total`, `covered`, `ratio`: the sizes of the files), and `emptyScope` when the scope is empty), `required` (required file ratio), `edgeKinds` (for each edge kind of the rule: `supported`, `minimumConfidence`, `observed`, `belowThreshold`), `unboundEndpoints`, `unresolvedReferences` (`ambiguous-usage` edges that start in a subject), `subThresholdEvidence`, `skipped` and `skipReasons` (see [Skip counts](#skip-counts)), `predicates` (the [predicate records](#predicate-coverage-records) of the families of the rule's edge kinds in the projects that the rule depends on), and `traversalIncomplete` (true when a cycle or path traversal stopped at `limits.traversal.maxVisited`). |
| `findingCount` | Number of findings of the rule. |

### Findings

| Field | Description |
|---|---|
| `fingerprint` | `sha256:<hex>`; see [Fingerprint](#fingerprint). |
| `ruleId`, `severity` | The rule and its severity. |
| `kind` | `forbidden-dependency`, `layer-violation`, `undeclared`, `contradiction`, `unobserved`, `cycle`, `forbidden-path`, `disallowed-dependency`, `peer-dependency`, `non-public-api`, `ownership-violation`, `budget-exceeded`, `missing-edge`, `missing-entity`, `binding-gap`, for a typed obligation `obligation-violation`, `obligation-absent`, or `obligation-unknown`, or for a [view](#views) `view-absent`, `view-stale`, or `view-drift`, for a [decision governance](adr-governance.md#finding-reasons) rule `adr-<reason>` (for example `adr-superseded`), for a review rule `assessment-<reason>` (`assessment-review-required`, `assessment-expired`, `assessment-revoked`, `assessment-no-approval`, `assessment-undisposed-removal`, `assessment-relaxation`, or `assessment-<unknown reason>`), or for a quality scenario `quality`. An `obligation-unknown` finding has the result `unknown` and the severity `info`: it explains the gap and never blocks by itself; the verdict of the rule does. |
| `message` | Human-readable text. |
| `source`, `target` | `architectureId` (comma-joined for several CALM IDs) and, when observed, `observed` (`qualifiedName`, `filePath`, `label`). |
| `edge` | Observed edge: `kind` (canonical), `nativeKind`, `confidence`, `resolutionMethod`. |
| `evidencePath` | Ordered steps. Each step is a `node` (observed node, with `scopedId` for a Kubernetes resource) or an `edge` (edge fields plus `from`, `to`, optional `location`, and `identity` when the fact has an environment, an occurrence, an image reference, or a lossy mark). |
| `location` | Primary `{ path, line? }`. |
| `relatedLocations` | Other locations, sorted and unique, without the primary location. |
| `owner` | Owner string, or `null`. |
| `disposition` | `active`, `baselined`, `waived`, or `expired-waiver`; see [Exceptions reference](exceptions.md). |
| `dispositionRef` | Waiver ID, `baseline`, or `null`. |
| `area` | The [report area](#areas) of the finding: `conformance`, `deploymentTrace`, `adrGovernance`, `diagramCurrency`, `assessmentCurrency`, or `qualityEvaluation`. The view findings (`view-absent`, `view-stale`, `view-drift`) are `diagramCurrency`; every other finding of the current rules is `conformance`. |
| `enforcement` | The effective [enforcement mode](../how-to/run-in-ci.md#gate-by-environment) of the finding: `mandatory`, `advisory`, or `shadow`. |
| `expected` | The condition that the rule requires, in words: for example `forbidden calls-service relation from-subject ledger of billing in environment production`. Never empty. |
| `observed` | The observed evidence or the gap: the fact (kind, endpoints, place), or for an `unknown` finding the unknown reason and what is missing. Never empty. |
| `delta` | The reconciliation entry of the pair of the finding when it is not `conforming`: `{class, from, to, relationshipId?, observedKinds}`. `null` when the finding has no pair or the pair conforms. |
| `providerLimits` | `{profile, families}`. `profile` is `{applies, id?, digest?}` as in `toolchain.cbm.profile`. `families` lists the evidence families that the finding depends on, each with `family`, `level`, `maturity`, and `lossModes` (sorted), for example `merged-occurrences`. The list is empty when the finding depends on no family of the profile. |
| `related` | The decisions, views, and assessments that the finding relates to: `[{kind, id}]` with `kind` `decision`, `view`, or `assessment`. A view finding has one entry `{kind: "view", id: <view ID>}`. `[]` when none; the changes that own decisions and assessments fill it. |
| `nextAction` | One of `fix implementation`, `correct intent`, `resolve binding`, `supply evidence`, `regenerate view`, `get review`, `fix analysis configuration`; see [Next actions](#next-actions). |
| `obligationId`, `subject`, `environment`, `result`, `occurrence`, `reason` | For the findings of a typed obligation: the obligation ID, the CALM ID of the subject, the declared environment (left out when none), the result (`violation` or `unknown`), the occurrence discriminator (for example a container name), and the unknown reason of an `unknown` result. A view finding has `result` (`violation` or `unknown`) and no other of these keys. Left out for the other findings. |

### Identity keys

| Key | Form | Where it appears |
|---|---|---|
| Scoped ID | `k8s:<env>/<cluster>/<group>/<Kind>/<namespace>/<name>`; `~` for an unknown part | `scopedId` of an evidence node |
| Edge key | `kind\|from\|to\|environment\|operation\|reference\|occurrence` | `identity.key` of an evidence step; `identity.key` of an edge in `graph/evidence.json` |
| Occurrence | Container name, else `stage:<index>` | `identity.occurrence` |
| Image reference | The `image` property of the edge: repository with tag or digest | `identity.image` |
| Possibly lossy | `true` when the capability profile states the loss mode `merged-occurrences` for the kind | `identity.possiblyLossy`, with `identity.lossModes`; the profile is `toolchain.cbm.profile` (`profile` in `graph/evidence.json`) |

## Coverage

Coverage says how much of the evidence the provider could extract and how far Concordia can trust a result. There are three levels: the file coverage of a rule (`files`), the predicate coverage records, and the coverage section of the run (`coverage` at the top level of `results.json`).

### Predicate coverage records

A record covers one predicate (a relationship family of the [capability profile](capability-profiles.md), or a property family such as `container-occurrence-versions`, `rendered-templates`, `index-result`, or `diagram-identity`) in one scope. A source project has records for all families (`scope.project`). A declared environment has records for the infrastructure families, `container-occurrence-versions`, and `rendered-templates` (`scope.environment`).

| Field | Description |
|---|---|
| `predicate` | The family name. |
| `scope` | `{project}` for a project, `{environment}` for the sources of a declared environment. |
| `status` | `complete`, `partial`, `unsupported`, `failed`, or `unknown`; see the table below. |
| `unresolved` | The number of gaps that apply to the predicate in the scope. |
| `lossModes` | The loss modes that apply, sorted by `id`. Each has `gaps`: the places where evidence is missing, with `path` and `line` (see [Gaps](#gaps)). A loss mode that the profile states for the whole family has no gap. |
| `reasons` | Why the status is below `complete`, sorted. For example `family unsupported by the provider build`, `index result unavailable`, `scope failed: <code>: <message>`, `required property missing: <name>`, `manifest cross-check did not run for the scope`, `no native edge of this family in the graph`. |

| Status | Meaning |
|---|---|
| `complete` | The provider supplies the family, and no loss mode applies in the scope. |
| `partial` | A loss mode applies: a gap in the scope, or a limit that the profile states for the whole family (a `partial` family). A missing item is then `unknown`, not a violation. |
| `unsupported` | The provider build does not supply the family. |
| `failed` | The query of the project failed (`CO-OBS-018`). |
| `unknown` | Concordia cannot tell: no index result is available for the sources of an environment, or the manifest cross-check did not read them. |

A missing necessary item is a `violation` only if the provider supports the observation, the observation is in scope, the analysis succeeded, and every predicate it needs is `complete` in the scope. Otherwise the result is `unknown` with the coverage reason. A rule that requires an edge (`requireEdge`, `observedRequired`) or an entity (`requireEntity`) follows this rule: with a predicate that is not `complete` it gives `unknown` (`CO-CON-032`, or `CO-CON-031` for an unsupported family) and no finding. A rule that forbids something keeps its result and shows the records in its `coverage.predicates`.

### Loss modes

The identifiers come from the observations of CBM 0.12.1 and from the capability profile.

| Loss mode | Meaning | Detected by |
|---|---|---|
| `default-excluded-directory` | An environment source is under a directory that the provider skips by default (`deploy/`, `deployments/`, `build/`, `dist/`, `node_modules/`, `target/`). The gap names the `.cbmignore` line, for example `!deploy/`; Concordia never edits `.cbmignore`. | Index result (`excluded.dirs`) |
| `raster-not-indexed` | A raster image (for example `docs/context.png`) is not indexed. No diagram predicate treats it as covered. | Index result (`not_indexed_files`) |
| `parse-partial` | The provider parsed a file in part. The gap has the first line of the unparsed range. | Index result (`parse_partial`) |
| `unrendered-template` | A Helm template is not rendered: its resource has a templated name or no namespace, or the provider parsed it in part. No fact is invented for it. | Index result, resource identity, manifest cross-check (file skipped) |
| `merged-occurrences` | A container has no `uses-image` fact because the provider keeps one edge for each workload and image repository (O4). The gap names the workload and the container. `container-occurrence-versions` is `partial`, and a fact of `uses-image` is marked possibly lossy. | Manifest cross-check |
| `target-not-declared` | A reference has no edge because no manifest declares its target (O5). The gap names the reference mode (`env`, `envFrom`, `volume`, `serviceAccountName`, `tls`, `parentRef`, `roleRef`, `subject`, `scaleTarget`), the target name, and the target kind. It is not a fact and not a violation. | Manifest cross-check |
| `empty-selector` | A NetworkPolicy or PodDisruptionBudget has an empty selector that may select all pods (O6). The result never says that the policy selects nothing. | Manifest cross-check |
| `unsupported-selector-expression` | A selector uses `matchExpressions`, which the provider does not evaluate (O6). | Manifest cross-check |
| `selector-join-directory-scoped` | The provider joins a selector only to workloads in the same directory (O6). | Profile (the family is `partial`); manifest cross-check for a selecting resource without a join |
| `selector-join-one-of-competing` | The provider joins one of several competing declarations of an object (O6). | Profile |
| `route-identity-unscoped` | Two environments that declare the same host and path share one `Route` node (O7). The environment comes from the Ingress or HTTPRoute resource. | Profile |
| `deploy-target-is-file` | A `kubectl apply -f` action targets a file, so the resources in it are not claimed as deployed (O8). | Evidence (`deploys-artifact` with `via: kubectl`) |
| `build-link-needs-build-command` | An image is linked to its Dockerfile only when a script or Compose file names it in a build command (O9). | Profile |
| `override-unsignalled` | A Kustomize `images:` override gives no edge (O11), so a version claim for an overridden image is `unknown`. | Profile |

`container-occurrence-versions` is `complete` for an environment when the cross-check found an occurrence for every container that it read, no template or index loss applies, and the sources are indexed.

### Gaps

A gap holds names, kinds, paths, and lines only: `reason` (the loss mode), `path`, `line`, `project`, `environment`, `workload` (`<Kind>/<name>`), `container`, `referenceMode`, `target`, `targetKind`, `source` (the environment source glob), `hint`, and `detail`. The manifest cross-check reads only the allow-listed structural fields of plain Kubernetes manifests in the sources of declared environments (container names and image presence, service account names, the names that `env`, `envFrom`, volumes, `tls`, `parentRefs`, `roleRef`, `subjects`, and `scaleTargetRef` refer to, and the presence and shape of selectors). It never reads an environment value, a Secret `data` or `stringData` entry, or an argument, so no such value appears in a gap, a log, or a report. A templated file is skipped and listed in `coverage.crossCheck.filesSkipped`.

### Coverage section of the run

| Field | Description |
|---|---|
| `predicates` | All predicate records of the run, sorted by predicate, project, and environment. |
| `scopeFailures` | `{project, reason}` for each project whose query failed. A failed scope is shown here even when no rule uses it. |
| `indexResults` | One entry for each project: `available`, the `reason` when it is not, and the `record` (`excludedDirs`, `notIndexedFiles`, `parsePartial`, and the counts that the provider reports). `concordia index` writes the record to `build/concordia/index-result.json` (`index-result-<project>.json` for other projects); `check` reads it when its graph generation equals the generation of the run. Without a record, the coverage of environment sources is `unknown` with the reason `index result unavailable`. |
| `skipped`, `skipReasons` | The skip counts of all fact kinds; see [Skip counts](#skip-counts). |
| `limits` | `configured` (the [limits](workspace.md#limits) of the run) and `breaches` (`limit`, `configured`, `observed`). |
| `crossCheck` | `environments` that were read, `filesChecked`, and `filesSkipped`. |

`inputs.indexResults` lists the index result files that the run read, with their digests.

### Skip counts

A rule leaves out some candidate edges. The edges stay in the evidence, and every skip is counted with a reason. `coverage.skipped` of a rule (and of the run, for all fact kinds) has four counts, and `skipReasons` has a reason for each count that is not zero.

| Count | Reason |
|---|---|
| `unbound` | An endpoint has no binding to a CALM element (resolve the binding). Also in `unboundEndpoints`. |
| `endpointMissing` | The edge refers to a node that the graph query did not return. `CO-CON-033` reports the count. |
| `outOfScope` | An endpoint is in a file outside the coverage scope (excluded by the project settings). |
| `unsupported` | The fact kind is not mapped by the provider profile (`unsupported` or `ambiguous` facts), kept as evidence and not evaluated. |

### Scope failures

A query-level failure in one project (`CO-OBS-001`, `CO-OBS-010`) marks that project `failed` (`CO-OBS-018`), and the run goes on. A rule that depends on the project is `unknown` with the reason `CO-OBS-018`; a rule that depends on other projects keeps its result. A mandatory rule (`check.unknown: fail`) that is `unknown` gives exit code 4. A failure of the connection to the provider (`CO-OBS-003`, `CO-OBS-004`, `CO-OBS-008`, a crash) or a failure of every project still stops the run with exit code 3 or 5.

## Areas

`areas` has six keys. Each is `{verdict, counts}`. The report shows them separately, so one overall pass cannot hide an area that is `unknown`.

| Key | Area | Filled by |
|---|---|---|
| `conformance` | Rules of the as-built families and typed rules | `check` (always configured) |
| `deploymentTrace` | Deployment trace completeness | `check` (configured when the workspace declares an environment; see [Deployment trace](#deployment-trace)) |
| `adrGovernance` | ADR governance | `check` (configured when the workspace has an `adr` register, or a policy lists the pack `adr-governance`; see [Decision summary](#decision-summary)) |
| `diagramCurrency` | Diagram presence, freshness, and correspondence | `check` (configured when the workspace declares `views`; see [Views](#views)) |
| `assessmentCurrency` | Assessment currency | `check` (configured when the workspace has required assessments; see [Assessment currency](#assessment-currency)) |
| `qualityEvaluation` | Quality evaluation | `check` (configured when the workspace declares quality scenarios or required quality areas; see [`quality`](#quality)) |

- `verdict` is a rule verdict (`pass`, `violation`, `unknown`, `not-applicable`, `error`) or `not-configured`. An area that the workspace does not configure shows `not-configured` with empty `counts`, never `pass`.
- `counts` are integers with fixed names for each area. The `conformance` area counts its rules by verdict (`pass`, `violation`, `unknown`, `notApplicable`, `error`) and its `findings`. Its verdict is the worst verdict of its rules (`not-applicable` when there are no rules).
- The `deploymentTrace` area counts its traces by status (`subjects`, `complete`, `partial`, `unresolved`, `unknown`). Its verdict is the worst of `pass` (all complete), `unknown` (partial or unknown), and `violation` (unresolved).
- The `diagramCurrency` area has the counts `views`, `pass`, `violation`, `unknown`, `notApplicable`, `error`, and `findings`. `views` is the number of declared views. `pass`, `violation`, `unknown`, `notApplicable`, and `error` count the results of the three checks (presence, freshness, and correspondence) of all views, so a workspace with two views has six results to count. `findings` is the number of findings with the area `diagramCurrency`. The verdict is the worst verdict of all those results. Without declared views the area is `not-configured` with empty `counts`. Assessment currency is not part of this area.
- The `adrGovernance` area has the counts `pass`, `violation`, `unknown`, `notApplicable`, `error`, and `findings`: the verdicts of the rules of the pack `adr-governance` that the run selected, and the number of findings with the area `adrGovernance`. Its verdict is the worst of those rule verdicts. A governance finding is never counted in `conformance`. A workspace that lists the pack and has no register has six `not-applicable` rules: the area is configured with `notApplicable` counts and is never `pass`. A workspace with a register and no pack has no rule to count: the area is `not-configured`, and the run has only the informational diagnostic `CO-ADR-007`. Waivers and baselines change the disposition of a finding, never the verdict of its rule or of the area.
- The `qualityEvaluation` area has the counts `pass`, `violation`, `unknown`, `notApplicable`, `error`, and `findings`: the verdicts of the rules that quality scenarios make (`quality:<scenario id>`, and `quality:area:<area>` for a required area that no scenario covers) and the number of findings with the area `qualityEvaluation`. Its verdict is the worst of those verdicts, so a mandatory scenario without evidence makes the area `unknown`, never `pass`. A workspace with no scenario and no required area has no such rule: the area is `not-configured`. The pretty report names an area that is `unknown` or `violation` before the summary line. The detail of each scenario is in [`assurance.quality`](#quality).
- `summary.verdict` is the worst of the rule verdicts and of the verdicts of the configured areas. If `conformance` passes and a configured area is `unknown`, the summary verdict is `unknown`. The exit code is still computed from the mandatory rules and findings; see [Exit code calculation](#exit-code-calculation).
- The `area` field of a finding names the area that it belongs to.

## Assessment currency

`assessmentCurrency`, `governance`, and `analysis` are top-level objects that `check` writes when the workspace has an `assessments` key. They are additive and every object is closed, so `schemaVersion` stays `1.1`. A run without an `assessments` key has none of them and no `inputs.assessments`, `inputs.approvals`, `inputs.authorityDigest`, or `inputs.trustAnchor`: its result is the one of before. The shapes are `$defs/assessmentCurrency`, `$defs/governance`, and `$defs/analysis` in `internal/conformance/schemas/results.schema.json`. How to approve an assessment and fix a finding: [assessments](assessments.md).

The `assessmentCurrency` area has the counts `pass`, `violation`, `unknown`, `notApplicable`, `error`, and `findings` (the review rules and their findings), and `assessments`, `required`, `current`, `reviewRequired`, `expired`, `revoked`, and `currencyUnknown` (the assessment versions and the required entries by currency). Its verdict is the worst of the verdicts of the required assessments and of the review rules. Without required assessments it is `not-configured`, never `pass`; one `review-required` assessment is `violation`.

| Key | Description |
|---|---|
| `assessmentCurrency.evaluatedAt`, `contractVersion`, `capabilityProfile` | The evaluation time that expiry is judged at (the run clock; `CONCORDIA_NOW` fixes it), the digest contract, and the capability profile that supplied the evidence. `evaluatedAt` is not part of `summary.resultDigest`. |
| `assessmentCurrency.assessments` | One entry for each assessment version, sorted by id and version: `id`, `version`, `type`, `environment`, `outcome`, `state`, `currency` (`current`, `review-required`, `expired`, `revoked`, or `unknown`), `unknownReason`, `reasons`, `details`, `causes`, `approval` (`id`, `decision`, `reviewer`, `authority`, `mechanism`, `verified`, `decidedAt`, `expiresAt`, `conditions`, `reason`; `null` when none applies), `notApplied` (decision records that failed verification, with the reason), `triggers`, `required`, `file`, `line`, `digest`, `latest`, and `analysis` (`complete`, `visited`, `closureSize`, `widened`, and `incomplete`; `widened` is `true` when an unresolved selector widened the scope). |
| `assessmentCurrency.required` | One entry for each required assessment: `id`, `assessment`, `type`, `environment`, `authority`, `version`, `currency`, `unknownReason`, `reasons`, and the `verdict` of `REV-APR-001`. |
| `triggers[]` | `class`, `change` (`added`, `removed`, or `modified`), `dependency` (`kind:identity`), `assumption`, `assessment`, `path` (the shortest path), `evidence`, `related`, and `action`, which is always `get review`. The text asks for a review and never says that the system is vulnerable or compliant. |
| `governance.relaxations` | Each change of the review rules, the enforcement entries for review rules, or an authority against the trust anchor: `kind`, `subject`, `change`, `comparison` (`stricter`, `equal`, `looser`, or `incomparable`), `relaxation`, `digest`, `detail`. |
| `governance.dispositions` | Each removed item of a required assessment: `assessment`, `kind`, `identity`, `state` (`disposed` or `undisposed`), `disposition` (`removed-intentionally`, `retired`, or `moved`), `movedTo`. |
| `analysis` | `mode` (`full` or `incremental`), `complete`, `visited`, `closureSize`, `invalidations`, `cacheHits`, `cacheMisses`, `incompleteAnalyses`, and `incomplete` (`limit`: `maxDepth`, `maxVisited`, or `maxSeconds`; `configured`; `visited`) for the first limit that stopped an analysis. The run manifest has the same object. |

`inputs.assessments` lists the assessment and assumption files with their digests, and `inputs.approvals` the approval and revocation files. `inputs.authorityDigest` is the digest of the authority configuration that verification used (the trust anchor's), and `inputs.trustAnchor` is the revision of the trust anchor.

The findings of the review rules have the area `assessmentCurrency`, the location of the assessment file, the next action `get review` (or `supply evidence` and `fix analysis configuration` for an `unknown` result), and `related: [{kind: "assessment", id}]`. SARIF has one result for each blocking or unknown review finding, located at the assessment file. JUnit has one testcase for each required assessment, in the suite `assessments/review-currency`. The pretty report shows an "Assessment currency" section with one row for each assessment version, its triggers, and the names of the approvals that were not applied; it names the area before the summary line when it is `unknown` or `violation`. The `export bundle` files are listed in [CLI](cli.md#export-bundle).

## Decision summary

`decisionSummary` is the detail of the `adrGovernance` area. It is present, with `inputs.decisions`, only when the workspace has a decision register and the `adr-governance` pack runs. Both are left out when either is missing: the pack without a register gives `not-applicable` rules and no summary, and a register without the pack gives only the informational `CO-ADR-007`. A run with neither is the one of before. The shape is `$defs/decisionSummary` in `internal/conformance/schemas/results.schema.json`; every object is closed. How to register a decision and fix a finding: [ADR governance](adr-governance.md).

| Field | Description |
|---|---|
| `providerSupport` | `supported`, or `unsupported` when the capability profile has no ADR evidence. |
| `decisions` | One entry for each registered decision, sorted by canonical ID. Every decision ID in the summary and in the findings is in canonical form (`ADR-3`, whatever the register spelling, for example `ADR-0003`). Fields: `id`, `title`, `path`, `line`, `project`, `status`, `statusText`, `mappedStatus`, `expect`, `categories`, and the three separate states `presence`, `applicability`, and `enforcement` (each with `state` and `reason`; `applicability` also has `statusLags`, `true` when the status text still says active but a supersession link is effective; `enforcement` also has `required` and the linked `rules`), `governs`, `released`, `successors`, and `predecessors`. |
| `history` | `links`: the normalized supersession links, each with `from`, `to`, `fromPath`, `toPath`, `provenance` (`edge`, `metadata`, or `both`), and `effect` (`effective` or `pending`). |
| `ruleJustification` | The result of `GOV-ADR-003` for each linked rule: `rule` (`<rule-id>@<pack-id>/<pack-version>`), `mandatory`, `linkedBy`, and `result`. |
| `triggers` | The result of `GOV-ADR-006` for each configured trigger: `id`, `category`, `raised`, `raisedBy`, `satisfiedBy`, `result`, and `reason`. |
| `candidates` | Candidates that are never a verdict: an `unregistered-decision`, a `mention`, or a `calm-adrs-entry`. |
| `findings` | The fingerprints of the governance findings, sorted. |

`inputs.decisions` lists the register files and the lifecycle profile (`<workspace file>#adr.lifecycle`, for example `concordia.yaml#adr.lifecycle`) with their digests, sorted by path. A finding of the area carries a `decision` block with the decision `id`, `title`, `status`, `statusText`, `path`, `line`, `successors`, `predecessors`, `governs`, `enforcedBy`, and `candidateSuccessors` (each `{id, path, applied}` with `applied` always `false`: governance never moves to a successor without a register edit).

With `--changed`, the governance rules run on the full scope and `CO-CON-027` lists them. Governance never writes a file.

## Trust crossings

`trust.crossings` lists the boundary crossings that Concordia derives from the declared trust context (see [Trust context reference](trust-context.md)). The object `trust` is additive under `schemaVersion` `1.1`. It is left out when no node, relationship, or binding declares a trust value, so the result of such a run is the one of before. The shape is `$defs/trust` in `internal/conformance/schemas/results.schema.json`; every object is closed.

Each entry has one dimension of one flow:

| Field | Description |
|---|---|
| `flow` | The flow identity: the relationship ID, `<flow>/<sequence>` for a flow step, or `observed:<source>-><destination>`. |
| `kind` | `relationship`, `flow-step`, or `observed`. |
| `relationship` | The relationship that a `flow-step` uses. Left out for the other kinds. |
| `source`, `destination` | The two ends of the flow in the direction of the flow. |
| `dimension` | A built-in or declared dimension name. |
| `from`, `to` | The values of the source and the destination, or `unknown` when an end declares none. |
| `direction` | `from → to`. A reverse call is a separate entry. |
| `state` | `crossing` (known, different values), `same` (known, equal values), or `unknown` (an end has no value). |
| `classification` | The data classification of the flow, or `unknown`. `unknown` is never a default level. |
| `externalDestination` | `true` for an observed destination that no binding maps; its trust is `unknown`. Left out otherwise. |

The entries are sorted by `flow`, then `dimension`, with UTF-16 code unit order, and are equal in two runs over equal inputs. `concordia explain` shows the crossings of the flow of a finding.

## Deployment trace

`deploymentTrace` is always present. It follows each CALM service to its workloads, container image references, build definitions, and source evidence in every selected environment. It shows the **declared level** only: the manifests and Dockerfiles as written. It never says that a workload runs, that a deployment happened, or that the image of a cluster comes from a Dockerfile. `concordia trace <architecture-id>` prints one subject of this section, and `concordia explain INFRA-…` prints the excerpt of a finding subject.

| Field | Description |
|---|---|
| `status` | `configured`, or `not-configured` when the workspace declares no environment. A `not-configured` section has no environment and its `summary` counts are all zero. |
| `levels` | The four levels in order: `declared` (status `evaluated`), `rendered-or-evaluated` (`evaluated` when a rendered source or plan of any selected environment holds evidence, complete or partial, else `not-available`), `deployment-execution`, and `runtime-observation` (each `not-available`). Each is `{level, status}`. |
| `environments` | One entry for each selected environment (`check --environment` limits it), sorted by `name`. |
| `unresolvedEnvironmentWorkloads` | The workloads whose environment is `unresolved` (a manifest in no environment source): `{identity, kind, locations}`. They take part in no environment trace. A rule that needs one is `unknown` with the reason `environment-unresolved`. |
| `unresolvedEnvironmentActions` | The deployment actions whose location is in no environment source, as entities of the kind `deployment-action`. |
| `summary` | `{subjects, byStatus, environments}`. `subjects` counts every trace, one for each subject and environment, including `unknown` and `unresolved` ones. `byStatus` has the four status keys, always present. |

An environment entry has:

| Field | Description |
|---|---|
| `name` | The environment ID. |
| `cluster` | The declared cluster, omitted when the environment declares none. The scoped identity of a workload uses it. |
| `evaluated` | The state of the level `rendered-or-evaluated` in the environment: `{level, status, sources}`. `status` is `evaluated` when a source is `complete` or `partial` and `not-available` when every source is `failed`. Each source is `{kind (`render` or `plan`), tool, path, coverage, digest?}`. Left out when the environment has no rendered source or plan. |
| `coverage` | The `deployment-workload-set` record of the environment: `{status, lossModes, reasons}`. |
| `subjects` | One trace for each trace subject: a node tagged `deployable`, a node that a `deployed-in` relationship lists, or a node with an active `workload` or `image` binding. Sorted by `architectureId`. |
| `unboundWorkloads` | The workloads that no trace subject owns, with their file and line, their image references, and the build definitions of those images: `{workload, images, buildDefinitions}`. Concordia never attaches one to a service by name. |
| `actions` | The deployment actions (`deploys-artifact` facts) in the environment sources: the tool, the location, and the file (`kubectl apply -f`) or chart (`helm`) that the action names. An action is never a placement, and it does not say which workloads it applies. |

A subject entry (one trace) has:

| Field | Description |
|---|---|
| `architectureId` | The CALM node ID. |
| `environment` | The environment of the trace. No step uses an entity of another environment. |
| `level` | Always `declared`: the steps are the declared chain. |
| `evaluated` | The rendered workloads and the planned resources that a binding maps to the subject, at the level `rendered-or-evaluated`. They are not part of the declared chain and never change its `status`. Left out when there are none. |
| `status` | `complete`: every step has a resolved entity for every bound workload and no gap is open. `partial`: a step is resolved and a gap is open. `unresolved`: `deployment-workload-set` is complete and no workload is found. `unknown`: the coverage is not enough to decide. The status comes from the evidence and the coverage records, never from a rule result. |
| `steps` | Five steps, always in this order: `service`, `workload`, `container-image`, `build-definition`, `source-evidence`. |
| `gaps` | The gaps of all steps, each with the `step` that it belongs to. |
| `limits` | The limits of all steps. |
| `rules` | The obligation results of the `infrastructure` pack for this subject and environment: `{ruleId, obligationId, verdict}`. Use `concordia explain <rule-id>` to go on. |

A step has `step`, `role`, `resolution`, `entities`, `gaps`, and `limits`. The evidence `role` and the `resolution` are never merged into one score:

| `step` | `role` | `resolution` | Entities |
|---|---|---|---|
| `service` | `declared-intent` | `exact` | The CALM node (`service`). |
| `workload` | `parsed-source-fact` | `explicitly-bound` (the binding that links the service to the workload), `ambiguous` when non-exclusive bindings disagree, `unresolved` with no workload | The workloads of the service in the environment (`workload`), their hosting targets (`hosting-target`, from the derived `placed-in` fact), and the actions that name their manifests (`deployment-action`). |
| `container-image` | `parsed-source-fact` | `exact` | One entity (`container-image`) for each container occurrence, with `repository`, `registry`, `tag`, `digest`, and the `container` name. |
| `build-definition` | `inferred-association` | `inferred` for one candidate, `ambiguous` for several, `unresolved` for none | The Dockerfile candidates (`dockerfile`, with the build command as a second location), their base images (`base-image`), and their external copy sources (`external-copy-source`); an image with no link is `unlinked-image`. |
| `source-evidence` | `inferred-association` | `explicitly-bound` with a bound path, `unresolved` without | The paths of the `sourceRoot` and `package` bindings (`source-path`), the manifests (`manifest`), and the Dockerfiles (`dockerfile`). |

An entity has `identity`, `kind`, `locations` (`{path, line}`), `properties`, and when they apply `resolution`, `bindings` (the binding IDs), `lineage`, and `consumers`. `lineage` is `candidate` for a Dockerfile that a build command links to the image repository, and `unknown` for an image with no link (`verified` belongs to a later change). `consumers` is the number of workloads of all declared environments that use the image repository: a trace lists only its own workloads, and the count shows that the same image has others. `properties` is an allow-list: kind, namespace, name, container name, image parts, stage names, and the structure of an action or a binding. Environment variable values, `env` and `envFrom` entries, secret data, and command arguments are never read into the section.

A gap is something that supplying evidence or configuration can fix. It counts in the status. It has a `reason`, the foundation `lossModes` of a `coverage-incomplete` gap, and a `detail`. The reason is one of the closed `unknown` reasons (`coverage-incomplete`, `unsupported-family`, `capability-regression`, `environment-unresolved`, `ambiguous-binding`, `binding-gap`, `evidence-conflict`, `selector-property-unknown`, `limit-exceeded`, and the four review-currency reasons that follow the next table) or one of:

| Reason | Meaning |
|---|---|
| `absent` | A definite absence: the extraction is complete and shows no entity. |
| `ambiguous-build-definition` | Several Dockerfiles are linked to one image repository; all are candidates. |
| `mutable-tag` | The image reference has no immutable digest, so the tag is shown as written and counts as no version. |

The four review-currency reasons have fixed meanings. The `reason` of an obligation and the `reasons` of a rule show the identifier, and the schema (`deploymentGapReason` in `internal/conformance/schemas/results.schema.json`) lists it. Other causes of an unknown review result use an existing reason: an unmapped dependency kind is `unsupported-family`, a traversal limit is `limit-exceeded`, partial coverage is `coverage-incomplete`, an unbound endpoint is `binding-gap`, and two sources that disagree are `evidence-conflict`. The causes `no approval`, `environment mismatch`, and `digest contract changed` are details of a `review-required` currency, not unknown reasons.

| Reason | Meaning |
|---|---|
| `target-not-declared` | A committed reference names a target that the provider did not declare, so the reference edge is missing. The dependency is `unknown`, never an empty match. |
| `trust-anchor-unreadable` | The trust anchor is missing, shallow, or not an ancestor of `HEAD` (`CO-ASM-007`). Governance and approval reads are `unknown`. |
| `verification-unavailable` | Signature verification cannot run, for example a signed approval checked with `--offline` (`CO-ASM-011`). It never gives exit code 6. |
| `reference-unresolved` | An implementation or verification path that an assessment names does not exist at the revision. |

The six reasons of the assurance adapters (`claim-not-enabled`, `no-evidence-supplied`, `no-evaluator-available`, `rendered-evidence-missing`, `enforcement-not-declared`, `reviewed-mode-not-available`) have fixed meanings that [assurance.md](assurance.md#reasons) lists. Admissibility details are never reasons.

A limit is a permanent boundary of the declared level. It never changes the status, and it always shows: `declared-level-only`, `provider-may-merge-occurrences` (the provider keeps one reference for each workload and repository, so a container that shares a repository can be missing; the gap `merged-occurrences` names each missing container), `image-provenance-unverified` (no verified link between the image and the Dockerfile), `other-consumers-belong-to-other-subjects`, and `source-provenance-unverified` (no provenance links the image to the source paths).

`areas.deploymentTrace` has the counts `subjects`, `complete`, `partial`, `unresolved`, and `unknown` of the `summary`, and the worst of: `pass` (all complete), `unknown` (a `partial` or `unknown` trace), and `violation` (an `unresolved` trace). `not-applicable` means that the workspace declares environments and has no trace subject, and `not-configured` that it declares none. The area joins `summary.verdict`; it never changes the exit code.

## Assurance

`assurance` is a top-level object that `check` writes when the workspace has an `assurance` key. It is additive and closed, so `schemaVersion` stays `1.1`. A run without the key has no `assurance` object, no `toolchain.assurance`, and no assurance entries in the manifest: its result is the one of before. When the key is present, all nine lists exist, and a list with no entry is `[]`. Every list is sorted. The shapes are in `internal/conformance/schemas/results.schema.json` (`$defs/renderedEntry`, `planEntry`, `statementSummary`, `lineageEntry`, `evaluationEntry`, `checkResultEntry`, `testCandidate`, `qualityEntry`, and `qualityAreaEntry`). What each kind of evidence claims and does not claim: [assurance](assurance.md). How to declare it: [add stronger evidence](../how-to/add-stronger-evidence.md).

An entry holds digests, counts, names, and reasons, never a rendered value, a secret, or a temporary path. Where an entry has a level, it is `rendered-or-evaluated`: this evidence says what a render or an evaluation gives, never what runs in a cluster.

| Field | Sorted by | Content |
|---|---|---|
| `rendered` | Environment, source, tool | One entry for each rendered source. See [`rendered`](#rendered). |
| `plans` | Environment, file, tool | One entry for each declared plan. See [`plans`](#plans). |
| `statements` | | One entry for each statement file that was read. See [`statements`](#statements). |
| `lineage` | | One entry for each rendered container occurrence: the exact-lineage claim with the deployment claims beside it. See [`lineage`](#lineage). |
| `evaluations` | Environment, kind, query | One entry for each effective-access query that a rule asked. See [`evaluations`](#evaluations). |
| `checkResults` | | One entry for each check result that was read, admissible or not. See [`checkResults`](#checkresults). |
| `candidates` | Kind, test, tested | The `TESTS` and `TESTS_FILE` edges of the code graph that name a CALM element. See [`candidates`](#candidates). |
| `quality` | | One entry for each quality scenario, with the verdict of its own mode. See [`quality`](#quality). |
| `qualityAreas` | | The coverage of each of the six quality areas. See [`qualityAreas`](#qualityareas). |

Three shared values:

- `coverage` is `complete`, `partial`, or `failed`. A source over a limit is `partial` or `failed`; Concordia never cuts the output to fit.
- `integrity` in `plans`, `statements`, `lineage`, and `checkResults` is `content-not-verified`, `content-verified-by-digest`, or `producer-authenticated`, in this order. See [Statements](assurance.md#statements). The `integrity` of an entry in [`evaluations`](#evaluations) uses the same levels as text with spaces: `content not verified`, `content verified by digest`, or `producer authenticated`.
- A gap is `{kind, detail}` with an optional `path`. It says what the evidence lacks, for example an unknown value after apply.

### `rendered`

| Field | Description |
|---|---|
| `environment`, `source` | The declared environment and the declared source path (a chart directory, an overlay directory, or plain manifests). |
| `tool` | `helm`, `kustomize`, or `plain`. |
| `toolVersion`, `rendererDigest` | The version and the `sha256:` digest of the renderer executable. Left out for `plain`, which starts no program. |
| `confinement` | `none`, `process`, or `process+os`. See [Rendering](assurance.md#rendering). |
| `inputDigest`, `outputDigest` | `sha256:` digests of what the render read and of what it gave. `outputDigest` is left out when the render failed. |
| `resourceCount` | The number of resources that the render gave. |
| `coverage`, `level`, `gaps` | The coverage, the level `rendered-or-evaluated`, and the gaps. |

### `plans`

| Field | Description |
|---|---|
| `environment`, `tool`, `file` | The environment, `terraform` or `opentofu`, and the declared plan file. |
| `digest`, `formatVersion` | The digest of the file and the plan format version, when the file was read. |
| `integrity`, `admissible`, `details` | How far the plan statement binding is verified, whether a mandatory claim may rest on the plan, and every reason when it may not. |
| `resourceCount`, `coverage`, `level`, `gaps` | As for `rendered`. A plan is shown whatever its binding says, and is never evidence of a deployment. |

### `statements`

| Field | Description |
|---|---|
| `kind` | `build-provenance`, `deployment-record`, `workload-observation`, `check-result`, `measurement`, or `plan`. |
| `path`, `digest` | The path of the statement file and the `sha256:` digest of its bytes. |
| `subjectDigests` | The subject digests that the statement names. |
| `producer` | The producer whose signature verified. Left out when none did. |
| `integrity` | The integrity that the statement reached. |
| `signature` | `none`, `no-producer`, `unavailable`, `failed`, or `verified`. |
| `details` | Every reason that the statement is not admissible. |
| `admissible` | Whether a claim may rest on the statement. An inadmissible statement stays listed and never gives a verdict. |

### `lineage`

| Field | Description |
|---|---|
| `environment`, `occurrence` | The environment and the container occurrence: `workload` (`environment`, `cluster`, `group`, `kind`, `namespace`, `name`), `container`, `kind` (`container`, `init`, or `ephemeral`), `image`, `repository`, `tag`, and `digest`. |
| `verdict`, `reason` | `pass`, `violation`, or `unknown`, and the closed reason of an `unknown`. |
| `missing`, `detail` | What the claim lacks and a sentence about it. |
| `builder`, `source`, `statement` | When the lineage is exact: the builder, the source (`id`, `uri`, `revision`), and the provenance statement (`path`, `digest`, `producer`, `integrity`). |
| `trustContract` | The `id` and the `digest` of the trust contract that the lineage was judged against. |
| `rejected` | The statements that concern the claim and cannot support it, each with `path`, `digest`, `producer`, `integrity`, and every reason in `details`. |
| `deployment` | `deployedDigest` (`claim` `deployed-digest`), `deploymentSucceeded` (`deployment-succeeded`), and `runtimeDrift` (`runtime-drift`), three claims, and `rejected`. A claim has `claim`, `verdict`, `reason`, `detail`, and, when known, `statement`, `digest`, `level` (`deployment-execution` or `runtime-observation`), and `time`. A `runtimeDrift` claim lists its `findings`: for each difference, the `observed` and the `expected` digest with their `source` (`rendered`, `deployment-record`, or `workload-observation`). |

No SLSA level is derived or shown.

### `evaluations`

| Field | Description |
|---|---|
| `environment`, `kind`, `query` | The environment, `network-policy`, `rbac`, or `cloud-firewall`, and the query as text. A `cloud-firewall` query has the form `<protocol>/<port> from <source address> to <destination address>`, with plan addresses, for example `tcp/5432 from aws_lb.public to aws_db_instance.ledger`. The protocol is `tcp` for a rule with `ports`; a rule without `ports` can also give `udp/<port>` queries. |
| `verdict`, `reason` | `allowed`, `denied`, `granted`, `not-granted`, or `unknown`, and the reason of an `unknown`. |
| `policies` | The policies, bindings, and roles that decided the answer. For `cloud-firewall`, each entry is `<address> <rule>`, where `<rule>` names the rule in the resource: `rule` for a standalone AWS rule resource, `ingress[i]` or `egress[i]` for an inline rule of an AWS security group, and the forms in [cloud-firewall](assurance.md#cloud-firewall) for Azure and GCP. For example, the open path of the payments example lists `aws_security_group.gateway egress[0]`, `aws_vpc_security_group_egress_rule.gateway_out rule`, and `aws_vpc_security_group_ingress_rule.ledger_from_gateway rule`. |
| `assumptions`, `declaredAssumptions` | The enforcement assumptions that the answer relies on, and those that the workspace declares. |
| `limits`, `gaps` | The limits of the evaluator and the gaps of its input. |
| `evaluator`, `evaluatorVersion`, `inputDigest` | The evaluator that answered and the digest of its input. The same rendered input gives the same answer and digest. For `cloud-firewall`, `inputDigest` is the SHA-256 of the plan bytes, or the digest of the sorted plan digests when the environment has more plans. |
| `integrity` | Optional. The integrity of the evidence that the evaluator read: `content not verified`, `content verified by digest`, or `producer authenticated`. A `cloud-firewall` entry always has it: `content not verified` for a plan without a statement, `producer authenticated` for a plan that a verified statement binds, and the lowest level when the environment has more plans. |

### `checkResults`

| Field | Description |
|---|---|
| `check`, `outcome`, `revision`, `environment` | What the result says: the check ID, its outcome, the revision, and the environment. |
| `evaluator`, `evaluatorVersion`, `time`, `producer` | Who produced it and when. `time` and `producer` are left out when unknown. |
| `integrity`, `admissible`, `reasons` | The integrity, whether the result may give a verdict, and every reason when it may not. |
| `covers`, `coversNothing` | The elements that the result covers. `coversNothing` is `true` for an explicitly empty `covers` list, which differs from a missing field. |
| `path`, `statementDigest` | The statement file and its digest. |

An inadmissible result stays listed with every reason and never gives a verdict.

### `candidates`

`kind` is `tests` or `tests-file`, `test` and `tested` name the two nodes, and `nodes` is a suggested mapping to CALM elements. A candidate is never evidence that a check ran, and no verdict uses it.

### `quality`

| Field | Description |
|---|---|
| `scenario`, `area`, `mode`, `environment` | The scenario ID, its quality area, its mode (`structural`, `measured`, or `reviewed`), and the environment. |
| `enforcement` | `mandatory` or `advisory`. |
| `verdict`, `reason`, `details` | The verdict of the scenario in its own mode, the closed reason of an `unknown`, and the details. The three modes are never merged into one score. |
| `evidence` | The references that gave the verdict: each has `kind` (`rule`, `measurement`, or `assessment`) and `ref`. |
| `ruleId` | The rule that carries the verdict in `rules`, `quality:<scenario id>`. |
| `links`, `reviewRationale` | The `addressedBy` decisions of the scenario (`adr`, `canonical`, `status` (`active`, `not-active`, or `unknown`), `state` (`active`, `inactive`, `superseded`, `ambiguous`, or `unknown`), `detail`, `reviewRationale`, `location`), and whether an active decision is its rationale. |
| `findings`, `location` | The fingerprints of the findings of the scenario (empty for a pass), and its file and line. |

### `qualityAreas`

| Field | Description |
|---|---|
| `area` | `availability`, `modifiability`, `security`, `performance`, `deployment-safety`, or `operability`. |
| `verdict` | `pass`, `violation`, `unknown`, `error`, or `no-scenario`. An area with no scenario is `no-scenario`, never a pass. |
| `required` | Whether `assurance.quality.requiredAreas` lists the area. A required area with no scenario is a violation. |
| `detail` | Why a required area without a scenario is a violation. |
| `reported` | The fingerprints of the findings of the run that the area has itself. |
| `scenarios` | Each scenario of the area: `id`, `mode`, and `verdict`. |
| `findings` | The findings of the area: `id`, `type` (`sensitivity-point`, `risk`, or `tradeoff`), `status` (`open`, `accepted`, or `resolved`), `statement`, `owner`, `scenarios`, and `links`. |

### `toolchain.assurance`

`toolchain.assurance` records what the assurance evidence depended on, so that a run after an upgrade shows what changed. The manifest has the same object.

| Field | Description |
|---|---|
| `renderers` | The renderers that rendered a source: `name`, `version`, and `sha256` of the executable. `[]` when none ran. |
| `cosign` | The version of the cosign that verified a statement. Left out when none ran. |
| `evaluators` | The evaluators that an environment enables: `id`, `version`, and `contractDigest`, sorted by ID. |
| `claimsRegistryDigest` | The digest of the claim registry of this build. |
| `trustContractDigest` | The digest of the producer trust contract of the workspace. Left out when the workspace declares no provenance. |

### Checks of a control

A control that lists `check-ids` (see [Control configuration](architecture.md#control-configuration)) has `checkIds` in its entry in `controls`. Its `verdict` is the worst of the verdicts of its rules and of its checks. A check with no admissible result is `unknown`, and `reasons` lists `{code, message}` for each check that is `unknown` (the closed reason, with the check ID in the message) or an `error`. Below `minimumPolicyCoverage`, the verdict is at least `unknown` with the reason `coverage-incomplete`.

### In the reports

A finding of a check-result rule or of a quality scenario is a normal finding: it has an owner, a fingerprint, and a disposition, and the SARIF and JUnit reports show it like any other finding. A scenario finding has the kind `quality` and the area `qualityEvaluation`. In SARIF, a finding about rendered evidence that has no file of its own points to the declared `source` of its environment when that is a path inside the repository. The `assurance` object itself is not part of SARIF or JUnit.

## Views

`views` is an array with one entry for each view of the workspace, sorted by view ID. It is left out when the workspace declares no view. It holds the result of the three checks of a view and the values that a later assessment needs. The shape is `$defs/view` in `internal/conformance/schemas/results.schema.json`. How to declare and fix a view: [keep diagrams current](../how-to/keep-diagrams-current.md).

### Rules and findings of the views

The three checks are rules of the built-in pack `views` (version `1.0.0`, family `view-currency`). They are not part of `core`. You do not list them in `policies.packs`: `builtin:views` in `policies.packs` is refused with `CO-POL-001`. A run that has views has them in `rules`.

| Rule | Check | Finding kind | Finding for |
|---|---|---|---|
| `VIEW-PRES-001` | Presence | `view-absent` | A source, output, export, or manifest file that is not in the tracked revision, or a manifest that names another view ID or scope. |
| `VIEW-FRESH-001` | Freshness | `view-stale` | An artifact or manifest that differs from the regenerated one, input digests that differ, or a digest contract that changed. |
| `VIEW-CORR-001` | Correspondence | `view-drift` | A `missing`, `extra`, `incorrect`, or `unmapped` difference between the diagram and the declared or observed architecture, or a result that is `unknown`. |

A view finding:

- has the area `diagramCurrency` and the severity of `comparison.severity` of the view (default `error`);
- has `related: [{kind: "view", id}]`, `source.architectureId` equal to the view ID, and `target.architectureId` equal to the subject (an element ID, `<from>><to>` for a relationship, or a file role or path);
- has the location of the declared source for an authored view, otherwise the output path, with a `line` for a difference that has one;
- has a next action (see [Next actions](#next-actions));
- follows baselines, waivers, `check.failOn`, `check.unknown`, and `check.enforcement` (family `view-currency`) as any other finding: a violation at or above `failOn` gives exit code 1, and an `unknown` result gives exit code 4 when `check.unknown` is `fail`.

### Fields of a view

| Field | Description |
|---|---|
| `id`, `kind`, `origin`, `owner`, `abstraction` | The values of the definition. |
| `environment` | The declared environment, or `null`. |
| `scope` | The CALM node IDs of the definition. |
| `severity` | `comparison.severity`: the severity of the findings of the view. |
| `nextAction` | The next action of the worst check that does not pass. Left out when every check passes. |
| `presence` | The presence check; see below. |
| `freshness` | The freshness check; see below. |
| `correspondence` | The correspondence check; see below. |
| `assessmentCurrency` | `{state, assessments}`. Without evaluated assessments, or when no assessment covers the subject that the view depicts, the state is `not-evaluated` and `assessments` is left out. Otherwise `assessments` lists each covering assessment (`id`, `version`, `type`, `state`, `reasons`, and the `approval` id when one applies) from the evaluation of the run, without a second evaluation, and `state` is the most severe of their states in the order `revoked`, `expired`, `review-required`, `unknown`, `current`. It never changes the verdicts of the view checks. See [assessments](assessments.md#currency). |
| `semanticDigest` | `sha256:<hex>` of the semantic projection of this run, or `null` when it could not be computed. |
| `digestContract`, `projectionContract` | The versions of the digest algorithm and of the projection (`concordia-view-semantic/1`, `concordia-view-projection/1`). |
| `contractChanged` | `true` when the committed manifest records other contract versions than this Concordia. Freshness is then a violation with the reason `digest contract changed`. |
| `depicts` | `{elements, relationships}`: the sorted CALM element and relationship IDs that the view must show (the effective coverage). |
| `generatedFrom` | For a generated view `{artifactPath, artifactDigest?, semanticDigest?, manifestPath}`: the artifact, its committed digest, the semantic digest that the committed manifest names, and the manifest. `null` for an authored view. |

`presence`:

| Field | Description |
|---|---|
| `status` | A verdict: `pass`, `violation`, `unknown`, `not-applicable`, or `error`. `unknown` when the tracked revision cannot be listed (no Git repository, no commit, or a failing Git command). |
| `revision` | The tracked revision that was listed. Left out when none could be. |
| `reasons` | Sorted phrases: `not committed`, `only in the index workspace`, `missing`, `view ID mismatch`, `scope mismatch`, `manifest not valid`, `no tracked revision`. |
| `files` | One entry for each file that the view needs, in declaration order: `path`, `role` (`source`, `output`, `export`, or `manifest`), `committed` (the file is in the tracked revision), and, when there is a problem, `reason` and `detail`. A file that exists only in the working tree, or only in the index of the provider, is not committed. |

`freshness`:

| Field | Description |
|---|---|
| `status` | A verdict. `not-applicable` for an authored view. |
| `verification` | `regenerated` when `check` recomputed the inputs and regenerated the artifacts. Left out for an authored view. A stored digest alone never gives `pass`. |
| `reasons` | Sorted phrases: `artifact differs from the regenerated bytes`, `artifact digest differs from the manifest`, `input digests differ`, `manifest differs from the recomputed manifest`, `digest contract changed`, `committed artifact not available`, `generator cannot run`, `generation failed`, `include not available`, `no tracked revision`. |
| `differingDigests` | The input digests that differ from the manifest, for example `modelProjection`, `observedProjection`, `bindings`, `controls`, `policies`, and `semantic`. |
| `manifestDifferences` | The other manifest fields that differ from the recomputed manifest. The revision and the dirty-tree digest are provenance and are not compared. |
| `artifacts` | One entry for each artifact: `path`, `format` (`dsl`, an export format, or the format of an authored source), `digest` (of the committed bytes), `regeneratedDigest` (of the regenerated canonical bytes; left out when they could not be made), `status`, `reasons`, and `detail`. An authored source has the digest and the status `not-applicable`. |

`correspondence`:

| Field | Description |
|---|---|
| `status` | A verdict. |
| `method` | `generated-source` (a generated view), `structured-source` (an authored view with provider diagram evidence), `supplemental-mapping` (a raster image with `mapping.depicts`), or `presence-only` (a raster image without it). `reviewed` is reserved and never written in this release. |
| `basis` | For a generated view: `committed-artifact` when freshness passes, `regenerated-projection` when it does not. |
| `nextAction` | The next action of the correspondence check. |
| `source` | `{path, line?}`: the diagram file. |
| `section`, `qualifiedName` | The diagram block that the selector chose. `qualifiedName` holds a provider ordinal, so it is for reading only and never a key. |
| `against` | The projections of `comparison.against`. |
| `compared` | The projections that the check compared. A projection that could not be built is missing here and appears in `unknown`. |
| `byConstruction` | The projections that hold by construction: `declared` for a generated view. |
| `differences` | The differences, each `{kind, subject, basis?, from, to?, detail?, locations}`. `kind` is `missing` (in the projection, not in the diagram), `extra` (in the diagram, in no projection), `incorrect` (same ends, another kind, direction, or protocol), or `unmapped` (a diagram element with no mapping). `subject` is `element` or `relationship`. `basis` (`declared` or `observed`) is set for `missing` and `incorrect`. `locations` are places in the diagram. |
| `unknown` | The reasons that a subject has no definite answer, each `{reason, subject?, candidates?, locations}`. The reasons are `structure not available`, `diagram selector is ambiguous`, `ambiguous mapping`, `ambiguous mapping key`, `mapping gap`, `no binding for necessary element`, `ambiguous binding`, `unbound evidence for necessary element`, `coverage lost`, `observed projection not available`, `declared projection not available`, `raster image without supplemental mapping`, `image digest not available`, `depicted relationship not in model`, and `supplemental mapping stale`. |
| `lost` | The necessary subjects that the observed projection cannot show: `[{kind, subject}]`. A lost binding or edge family never leaves a view current silently. |
| `reasons` | Sorted phrases of the differences and of the unknown reasons. |

### `toolchain.structurizr`

`toolchain.structurizr` is the version of the Structurizr CLI that regenerated a view export in the run (for example `2025.11.09`). It is in `results.json` and in the run manifest, and it is left out when no export was verified (no export declared, or the CLI did not run). Only the pinned version is accepted: another version makes the export `unknown`.

## Next actions

Every finding has one next action from this closed list. The as-built findings get it from their kind; the findings of typed obligations get it from the result.

| Next action | Used for |
|---|---|
| `fix implementation` | A violation of a rule: forbidden, layer, cycle, path, disallowed, peer, non-public API, budget, missing edge or entity, and the obligation kinds `obligation-violation` and `obligation-absent`. |
| `correct intent` | A difference between the declared and the observed architecture: `undeclared`, `unobserved`, `contradiction`, and `ownership-violation`; and a difference between a diagram and the architecture (`missing`, `extra`, `incorrect`, or `unmapped`): a `view-drift` finding with the result `violation`. |
| `resolve binding` | A `binding-gap` finding, and an unknown result with the reason `binding-gap` or `ambiguous-binding`. For a view: a correspondence that is `unknown` because of `no binding for necessary element`, `ambiguous binding`, or `unbound evidence for necessary element`. |
| `supply evidence` | An unknown result for a gap in the coverage or the evidence: `coverage-incomplete`, `environment-unresolved`, `evidence-conflict`, `selector-property-unknown`, `target-not-declared`, `reference-unresolved`. For a view: presence of an authored view that fails (the source is not committed), and a correspondence that is `unknown` because of `structure not available`, a mapping problem (`mapping gap`, `ambiguous mapping`, `ambiguous mapping key`), a raster image (`raster image without supplemental mapping`, `supplemental mapping stale`), or an ambiguous diagram selector. |
| `fix analysis configuration` | An unknown result that a limit or the provider causes: `limit-exceeded`, `unsupported-family`, `capability-regression`, `trust-anchor-unreadable`, `verification-unavailable`. For a view: a check that is `unknown` or `error` because of the tools or the capability: no tracked revision, a Structurizr CLI that cannot run or is not the pinned version, an include that is not available, `coverage lost`, or a projection that is not available. |
| `regenerate view` | A view finding of a generated view that fails presence, and a freshness result that fails (`view-stale`): the committed files do not equal the regenerated ones. Run `concordia render --views` and commit. |
| `get review` | Used by the change that owns reviews. |

## Observability counters

`observability` (in `results.json` and in the run manifest) has eight integers. They contain no text, path, name, or value from the repository.

| Counter | Counts |
|---|---|
| `ingestedFacts` | The facts that the provider returned: the evidence edges and the unsupported facts of all projects. |
| `normalizedFacts` | The evidence edges with a canonical kind (not `ambiguous`, not `unsupported`). |
| `unresolvedFacts` | The edges with an unbound endpoint or an endpoint that the query did not return (`coverage.skipped.unbound` and `endpointMissing`), and the edges whose resolution is `ambiguous` or `unresolved`. |
| `unsupportedFacts` | The native edges of types that Concordia does not map: the sum of the counts of `CO-OBS-016`. |
| `coverageGaps` | The predicate coverage records whose status is not `complete`. |
| `conflicts` | The groups of competing declarations of one resource identity, the entities that several non-exclusive bindings map to different CALM elements, and the evidence conflicts of obligation results. |
| `cacheHits` | The remote resources that the run took from the resource cache. |
| `stageFailures` | The projects whose query failed (`coverage.scopeFailures`). |

## Result digest

`summary.resultDigest` is `sha256:` and the SHA-256 of the canonical JSON of the run in which these values are zeroed: `run.id`, `run.startedAt`, `run.finishedAt`, `run.host`, `summary.resultDigest` itself, and `observability.cacheHits` (whether a resource was cached is a state of the machine, not of the inputs). `toolchain.concordia` is the version string and has no build time. Every other field takes part: findings, areas, coverage, counters, and diagnostics.

Two runs with the same recording or graph, policies, bindings, and workspace have the same `resultDigest`, and the same canonical JSON once the volatile fields are removed. People see timestamps that differ. Compare `resultDigest` to check that a run reproduces another one. The run manifest has the digest in its `summary`.

## Verdicts

| Verdict | Meaning |
|---|---|
| `pass` | The rule ran, evidence was sufficient, and nothing violated it. |
| `violation` | The rule produced one or more findings. |
| `unknown` | Concordia cannot decide. The `reasons` give the cause, for example `CO-CON-021` (coverage below the requirement), `CO-CON-023`, `CO-CON-024`, `CO-POL-009`, `CO-CON-030` (a limit was exceeded), `CO-CON-031` (the family is unsupported), `CO-CON-032` (a predicate is not complete), or `CO-OBS-018` (a project failed). |
| `not-applicable` | The rule does not apply (reason `CO-CON-022`: the rule selects no subject). |
| `error` | Rule evaluation failed. |

## Exit code calculation

The first true condition wins (`ComputeExitCode` in `internal/conformance/exitcode.go`):

| Order | Condition | Exit code |
|---|---|---|
| 1 | A rule has the verdict `error`. | 7 |
| 2 | A live finding (disposition `active` or `expired-waiver`) has a severity at or above `failOn`. | 1 |
| 3 | A rule is `unknown` and its effective `onUnknown` is `fail`. | 4 |
| 4 | None of the above. | 0 |

Severity order is `info` < `warning` < `error`. Baselined and waived findings never count. A rule with obligations (typed rules and review rules) is `unknown` for condition 3 when one of its obligations that counts is `unknown` and its own mode is `mandatory`; the mode of each obligation is the mode of its environment, so an `advisory` violation in one environment does not hide a `mandatory` unknown in another, although the rule verdict shows the worse of them. Input, adapter, toolchain, and lock errors stop the run before a result exists and use exit codes 2, 3, 5, and 6; see [CLI reference](cli.md).

## Reconciliation classes

Each `reconciliation` entry compares one `(from, to)` pair of architecture IDs with the observed graph. Fields: `from`, `to`, `relationshipId` (when a relationship is involved), `classification`, `observedKinds` (sorted canonical edge kinds), `edgeCount`.

| Class | Meaning |
|---|---|
| `conforming` | A declared relationship permits the observed edges (kind and confidence). |
| `undeclared` | Edges are observed between the pair, but no relationship is declared between them. |
| `contradiction` | A relationship is declared between the pair, but no observed edge matches its allowed kinds or confidence. |
| `unobserved` | A declared `connects` or `interacts` relationship has no observed edge. |
| `unknown` | Coverage of either endpoint is below `check.minimumCoverage`, so the class cannot be decided. This overrides the other classes. |

The [diagram projection](../how-to/render-diagrams.md) maps these classes to relationship status tags.

## Pretty output

The default `--format pretty` writes one block for each rule that has a `violation`, `error`, or shown `unknown` verdict. Passing and not-applicable rules are omitted. An `unknown` rule with `onUnknown` `ignore` is hidden.

```text
FAIL <ruleId> <name>[ (advisory: does not change the exit code)]
  Why it matters: <rationale of the rule>
  Owner: <owner>
  [Coverage: <ratio> (required <ratio>)]
  <source> -> <target>: <n> finding(s)
    <path>[:<line>]  <verb> <target>[ in <target path>][  [<disposition>]]
                     then <node> <verb> <target>
    [Next action: <action>]
  Fix: <remediation of the rule>
  [<n> baselined finding(s) not shown (use --verbose)]
```

- The status word is `FAIL`, `UNKNOWN`, or `ERROR`; color is never the only signal.
- `Why it matters` and `Fix` are the `rationale` and `remediation` of the rule, shown when the policy sets them. `Owner` is the owner of the rule, else the one owner that all shown findings share.
- Findings are grouped by their two ends: `<source> -> <target>` (CALM element IDs), `<source>` alone when the finding has no target, `cycle through <a>, <b>` for a cycle, `required <id>, assessment <id>` for a review finding, or `scope <ids>` for a quality scenario. The groups are sorted by this text, then by next action. In a group, findings are sorted by path, then line, then message; a finding without a location comes last. The order depends only on what the block shows, so two copies of a workspace with different project names print the same block. `results.json` keeps its own order (rule ID, then fingerprint). Each finding is one line with its location and what the code does there: the edge kind in lower case (`imports`, `calls`) and the target by its short name. A longer evidence path adds one `then` line for each further edge. A finding that is not active (an expired waiver, or with `--verbose` a baselined or waived finding) ends with its disposition in brackets.
- `Next action` closes a group when the rule has no remediation. The coverage line shows when coverage is below the required ratio.
- An `unknown` block shows `Why it matters`, one `Cannot judge: <message> (<code>)` line for each reason, and the coverage line. With `onUnknown` `warn`, it adds `On unknown: warn`. An `error` block uses `Error:`.
- `--verbose` adds, under each finding, its full fingerprint and each edge of its evidence with the native kind and confidence, shows baselined and waived findings, and always shows the coverage line. Otherwise a line states how many accepted findings are not shown. `concordia explain` and `results.json` always have every detail.
- The last line is the summary: `<n> active error(s)`, then active warnings and infos when present, then baselined, waived, expired waivers, and unknown counts.
- `--no-color` and `NO_COLOR` turn off ANSI color.

The terminal output shows each [area](#areas) in a section of its own. A section starts with the title of the area, its verdict, and its counts (`Conformance: violation (1 violation, 2 unknown, 1 findings)`) and holds the rules of the area that do not pass. The areas that the run does not configure are named in one line (`Not configured: Deployment trace, ADR governance, ...`). An area that is `unknown` is named again in a line `Unknown area(s): <title>` before the summary line, so a passing `conformance` section never hides it.

The area of the views has the title `Diagram presence, freshness, and correspondence` and one row for each view, before the blocks of the rules that do not pass:

```text
Diagram presence, freshness, and correspondence: violation (1 violation, 4 pass, 1 notApplicable, 2 findings, 2 views)
  payments-container (container, generated): presence: pass, freshness: pass, correspondence: pass (method: generated-source), assessment currency: not-evaluated
  payments-context (system-context, authored): presence: pass, freshness: not-applicable, correspondence: violation (method: structured-source), assessment currency: not-evaluated; next action: correct intent
```

A row has the four check results, the correspondence method, and the next action of the view when one applies. The block of a view finding names the file (with the line of a difference), the view ID, the owner, and the next action. The area is absent from the output when the workspace declares no view.

## JUnit mapping

`--format junit` writes JUnit XML (`internal/reporting/junit.go`): one `<testsuite>` for each policy pack (`name="<id>@<version>"`) and one `<testcase>` for each rule (`classname` is the pack ID, `name` is `<ruleId> <name>`). The root `<testsuites name="concordia">` carries the totals.

A workspace with views adds one `<testsuite>` for each check of each view, with `name="views/<view id>/<check>"` (`presence`, `freshness`, or `correspondence`) and one `<testcase>` with `classname="views/<view id>"` and the rule as `name`. The view rules have no suite of the pack. The outcomes map as in the table below: a check that is `not-applicable` (freshness of an authored view) is `<skipped>`.

| Rule outcome | Test case |
|---|---|
| `pass` | Empty test case. |
| `violation` with at least one finding at or above `failOn` | `<failure type="violation">` listing fingerprint, location, and message of each failing finding. |
| `violation` with only baselined or waived findings | Passing; the accepted findings are in `<system-out>`. |
| `unknown` with `onUnknown` `fail` | `<failure type="unknown">` with the reasons. |
| `unknown` with `warn` or `ignore` | Passing; `<system-out>` shows `unknown (<policy>): <reasons>`. |
| `error` | `<error type="error">`. |
| `not-applicable` | `<skipped>` with the reasons. |

## SARIF mapping

`--format sarif` writes SARIF 2.1.0 (`internal/reporting/sarif.go`).

| SARIF element | Source |
|---|---|
| `tool.driver` | `Concordia`, with the toolchain version. |
| `tool.driver.rules[]` | One descriptor for each rule: `id`, `name`, `shortDescription`, `defaultConfiguration.level`, `fullDescription` (rationale), `help` (remediation), `properties` (`family`, `policy`). |
| `level` | Severity `error` → `error`, `warning` → `warning`, `info` → `note`. A result of enforcement `advisory` is always `note` (it is shown and never blocks); the same holds for the `defaultConfiguration.level` of the rule. |
| `results[]` | One result for each finding. `partialFingerprints["concordia/v1"]` is the fingerprint. |
| `locations`, `relatedLocations` | Repository-relative URIs with `uriBaseId` `SRCROOT`; `region.startLine` when a line is known. |
| `codeFlows` | The evidence path as one thread flow. |
| `baselineState` | `unchanged` for `baselined`, otherwise `new`. |
| `suppressions` | `external` / `accepted` for `baselined` (justification `baseline`) and `waived` (justification: the waiver reason). |
| `properties` | `sourceArchitectureId`, `targetArchitectureId`, `findingKind`, `disposition`, `dispositionRef`, `owner`, `confidence`, `nextAction`, `area`, and, when the finding has them, `enforcement`, `obligationId`, `subject`, `environment`, `result`, `reason`, and, for a view finding, `viewId`. |
| `invocations[0]` | `executionSuccessful` (false only for exit code 7), `exitCode`, start and end time, and a notification for each `unknown` or `error` rule. |
| run `properties` | `project`, `graphGeneration`, `sourceRevision`, `queryPlanDigest`, and, when the workspace declares views, `diagramCurrency`: `{verdict, views}` with one entry for each view (`id`, `kind`, `origin`, the verdicts `presence`, `freshness`, and `correspondence`, `correspondenceMethod`, `assessmentCurrency`, and `nextAction` when one applies). |

A view finding points to the declared `source.path` of an authored view, otherwise to the output path of a generated view, with `region.startLine` for a difference that has a line.

A serialization failure raises `CO-REP-002`.

## Fingerprint

A fingerprint is `sha256:` followed by the SHA-256 of a version tag and these seven inputs, joined by the unit separator `U+001F` (`internal/conformance/fingerprint.go`):

1. rule ID
2. source architecture ID
3. target architecture ID
4. canonical edge kind (the semantic evidence kind, for example `imports` or `routes-to`; never the native kind)
5. source qualified name
6. target qualified name
7. normalized repository path of the source file (POSIX)

The version tag is the first hashed input. For a finding of a typed obligation, the hash uses another version tag and two more inputs after the seven: the environment and the occurrence discriminator (empty when the obligation has none). Line and column numbers are never inputs. Three special forms exist:

| Finding kind | Inputs |
|---|---|
| `cycle` | Rule ID, the sorted cycle member IDs (first as source, comma-joined list as target), edge kind `cycle`; other inputs empty. |
| `unobserved` | Rule ID, source ID, comma-joined target IDs, edge kind `unobserved`, the relationship ID as source qualified name; other inputs empty. |
| view findings | Rule ID (`VIEW-PRES-001`, `VIEW-FRESH-001`, or `VIEW-CORR-001`); source architecture ID: the view ID; target architecture ID: the subject (an element ID, `<from>><to>`, or the file role or path); edge kind `view:<check>:<difference>` (for example `view:freshness:artifact` or `view:correspondence:missing`); the normalized path of the source or of the output. No provider ordinal, timestamp, or digest is an input, so a regenerated artifact keeps the identity of its finding and baselines and waivers keep working. Renaming a view changes the fingerprints of its findings. |

Baselines and waivers store these values; see [Exceptions reference](exceptions.md) and [Fingerprints, baselines, and waivers](../explanation/fingerprints-baselines-waivers.md).

## Run manifest

`.concordia/runs/<runId>/manifest.json` is canonical JSON with five members copied from the result: `run`, `toolchain`, `inputs`, `summary`, and `observability`, and a `status`. It records what the run read and decided without the findings. `summary` holds the `resultDigest`, so a manifest and a result of one run can be matched without the findings.

`status` is `incomplete` when `check` starts to write its files and is replaced atomically by `complete` after the reports, the canonical JSON, and the evidence files are written. A run that is interrupted (exit code 130) or fails while writing keeps `incomplete`. `explain`, `diff`, and `export bundle` refuse a run whose manifest status is not `complete` with `CO-REP-004` (exit code 2).

## Related

- [Diagnostics reference](diagnostics.md)
- [Trust context reference](trust-context.md): dimensions, classifications, and crossings.
- [Policies reference](policies.md): rule IDs, evidence requirements, and edge kinds.
- [Workspace reference](workspace.md#limits): the `limits` key.
- [Verdicts and coverage](../explanation/verdicts-and-coverage.md): why `unknown` exists.
- [Run in CI](../how-to/run-in-ci.md): using JUnit, SARIF, and exit codes in a pipeline.
