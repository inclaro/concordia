# How to resolve unknown verdicts

This guide shows you how to find out why a rule returned `unknown` and how to fix it. By default, an `unknown` verdict makes `concordia check` exit with code 4.

## Prerequisites

- A workspace that you can check. See [set up a workspace](set-up-a-workspace.md).
- A current graph. Run `concordia index` first.

## Find the reason

1. Run the check for the rule:

   ```bash
   concordia check --rule <rule-id> --no-color
   ```

   Use the ID of the rule that reports `unknown`. In the payments example (`docs/examples/payments/`), `PAY-LEG-001` reports `unknown`, because the workspace excludes the Kotlin code of `legacy-core-banking` from the graph:

   ```console
   $ concordia check --rule PAY-LEG-001 --no-color | grep -A4 '^UNKNOWN'
   UNKNOWN PAY-LEG-001 Legacy core banking must not call the ledger service
     Why it matters: The legacy core banking system is being retired. If it still calls the ledger, switching it off breaks the bank's books.
     Cannot judge: minimum coverage not met: 0.750 < 0.9 (CO-CON-021)
     Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
     Coverage: 75.0% (required 90.0%)
   ```

2. Read the reason codes in the rule block. To see the rule's coverage numbers and evidence, run:

   ```bash
   concordia explain PAY-LEG-001
   ```

3. Find the code in the table below and apply the fix. If the rule is a typed rule, or comes from a review, decision, or assurance pack, find its reason in [find the fix by reason](#find-the-fix-by-reason).

| Reason | Meaning | Fix |
|---|---|---|
| `CO-CON-021` | File coverage is below the required ratio. | [Raise coverage](#co-con-021-coverage-below-the-minimum) |
| `CO-CON-026` | The files of one language are below the required ratio. | [Raise coverage](#co-con-021-coverage-below-the-minimum) |
| `CO-CON-023` | Some edges have less confidence than the rule requires. | [Lower the threshold](#co-con-023-evidence-below-the-confidence-threshold) |
| `CO-POL-009` | The rule uses an edge kind that CBM cannot observe. | [Change the edge kind](#co-pol-009-unsupported-edge-kind) |
| `CO-CON-024` | The rule scope contains no files. | [Fix the scope](#co-con-024-empty-scope) |
| `CO-CON-032` | A predicate that the rule needs is not `complete` (a loss mode applies). | [Read the loss modes](#co-con-032-predicate-coverage-not-complete) |
| `CO-CON-031` | The provider profile does not support the family that the rule needs. | [Change the rule](#co-con-031-unsupported-family) |
| `CO-OBS-018` | The query for a project that the rule depends on failed. | [Fix the project](#co-obs-018-a-project-failed) |
| `CO-CON-030` | A limit of the workspace was exceeded. | [Raise the limit](#co-con-030-limit-exceeded) |

## Find the fix by reason

A typed obligation, and the rules of the review, decision, and assurance packs, give one reason from a closed set. `results.json` shows it in the `reason` field of the obligation, and `check` prints it with the rule. Find the reason below. The [reasons table](../explanation/verdicts-and-coverage.md#the-reasons-for-unknown) defines each one.

| Reason | What to do |
|---|---|
| `coverage-incomplete` | Read `coverage.predicates` of the rule and fix the loss mode. See [CO-CON-032](#co-con-032-predicate-coverage-not-complete). A rule that reads rendered manifests names the environment whose render is partial: see [add stronger evidence](add-stronger-evidence.md). |
| `unsupported-family` | Use a provider build that supports the family, or keep the rule `advisory`. See [CO-CON-031](#co-con-031-unsupported-family). |
| `capability-regression` | The lock records a family that this run does not support. Restore the CBM version or capability. If the change is intended, review the [digest-impact report](../reference/cli.md#digest-impact-report) and run `concordia lock update`, which records the profile of this CBM build. |
| `environment-unresolved` | Declare the environment under `environments`, and list the source files of the fact under it. See [set up a workspace](set-up-a-workspace.md#declare-environments). |
| `ambiguous-binding` | Make one of the bindings exclusive, or remove the overlap. See [fix binding errors](bind-code-to-architecture.md#fix-binding-errors) (`CO-BND-009`). |
| `binding-gap` | Add a binding for the endpoint that the finding names. The finding has the next action `resolve binding` and the location. See [bind deployed resources](bind-code-to-architecture.md#bind-deployed-resources). |
| `evidence-conflict` | Two sources disagree. Fix the sources so that they agree, or remove the second declaration of a resource (`CO-OBS-021` names both files). |
| `selector-property-unknown` | Supply the property that the selector reads, or change the selector. A dimension with no data source, such as `trustContext`, is always `unknown`. |
| `limit-exceeded` | See [CO-CON-030](#co-con-030-limit-exceeded). |
| `target-not-declared` | Declare the Secret, ConfigMap, or other target in a manifest that the graph indexes, or remove the reference. |
| `trust-anchor-unreadable` | Fetch the trust anchor with its history (a full clone, not a shallow one), and check `assessments.trustAnchor.ref` (`CO-ASM-007`). See [review and approve assessments](review-and-approve-assessments.md). |
| `verification-unavailable` | Signature verification cannot run, for example with `--offline` (`CO-ASM-011`). Run without `--offline`, or use a `protected-review` authority. Check that [cosign](install.md) is installed. |
| `reference-unresolved` | Restore the implementation or verification path that the assessment names, or change the reference. |
| `claim-not-enabled` | Use a claim that a loaded contract enables, or declare the `assurance` input that enables it. See [reasons in the assurance reference](../reference/assurance.md#reasons). |
| `no-evidence-supplied` | Supply a statement or check result that is admissible for the revision and the environment. See [add stronger evidence](add-stronger-evidence.md). |
| `no-evaluator-available` | Enable the evaluator (`network-policy` or `rbac`) under `assurance.evaluators` for the environment. |
| `rendered-evidence-missing` | Declare a rendering source for the environment under `assurance.rendering`, and fix a `partial` or `failed` render. Run `concordia assurance validate`. |
| `enforcement-not-declared` | Check that the cluster enforces the policy, then declare the assumption under `assurance.evaluators[].assumes`. Without it, a `denied` answer is not an isolation claim. |
| `reviewed-mode-not-available` | Add the `assessments` key to the workspace, or change the quality scenario to `structural` or `measured`. |

## CO-CON-021: coverage below the minimum

Coverage is the share of in-scope files that the graph covers. Excluded files stay in the total (see [verdicts and coverage](../explanation/verdicts-and-coverage.md)).

1. Index again, so that the graph covers the current code:

   ```bash
   concordia index
   ```

2. If the ratio is still too low, change `observation.projects[0].include` and `exclude` in `concordia.yaml` so that they match the code you want to check.
3. If you accept the lower ratio, set `check.minimumCoverage` in `concordia.yaml`, or pass a ratio for one run:

   ```bash
   concordia check --minimum-coverage 0.8
   ```

A rule can also set its own `evidence.minimumCoverage`. See [policies](../reference/policies.md).

`CO-CON-026` is the same check for the files of one language, for example `.kt 0.000 < 0.9` when no Kotlin file is in the graph. Include and index the files of that language, or set `evidence.coverage.supportedLanguages` of the rule to a lower ratio. See [the `evidence` block](../reference/policies.md#the-evidence-block).

## CO-CON-023: evidence below the confidence threshold

The rule found edges whose confidence is below `evidence.minimumConfidence` (default `syntactic`). Such edges do not count as evidence.

1. Run `concordia explain <rule-id>` to see which edges are affected.
2. In the policy pack, set a lower `evidence.minimumConfidence` on the rule (or on its CALM control):

   ```yaml
   evidence:
     minimumConfidence: heuristic
   ```

The confidence levels are listed in [policies](../reference/policies.md).

## CO-POL-009: unsupported edge kind

The rule names an edge kind that the observation provider does not support, such as `uses-type` (the `USES_TYPE` edge). The rule cannot find evidence, so its verdict stays `unknown`.

Change the rule to use a supported kind such as `imports` or `calls`. The supported kinds are listed in [policies](../reference/policies.md).

## CO-CON-024: empty scope

The rule's selectors match nodes that have no files in the observed scope.

1. Run `concordia bind validate --show-unbound` to see which architecture nodes have no binding. See [bind code to architecture](bind-code-to-architecture.md).
2. Check that `include` and `exclude` do not remove the bound directories.

## CO-CON-032: predicate coverage not complete

A rule that requires something (`requireEdge`, `observedRequired`, `requireEntity`) cannot report it missing unless the provider extracted everything that it needs. The coverage of the rule lists the predicates and their loss modes.

1. Open `results.json` and read `coverage.predicates` of the rule. Each record has a `status`, `lossModes`, and `reasons`. The gaps of a loss mode name a `path` and a `line`.
2. Find the loss mode in the table and apply the fix:

| Loss mode | Fix |
|---|---|
| `default-excluded-directory` | The provider skips the directory. The gap names the line to add to `.cbmignore`, for example `!deploy/`. Add it and run `concordia index` again. Concordia never edits `.cbmignore`. |
| `raster-not-indexed` | The provider does not index raster images. Describe the diagram in a text format such as Mermaid. |
| `parse-partial`, `unrendered-template` | The file is a template that the provider parsed in part or did not render. The facts in it are missing. Commit the rendered manifests, or accept that the predicate stays `partial`. |
| `merged-occurrences` | Two containers use one image repository, and the provider keeps one edge. The gap names the container. Use separate image repositories if you need the version of each container. |
| `target-not-declared` | A manifest refers to a Secret, ConfigMap, claim, service account, role, or gateway that no indexed manifest declares. Declare it in a manifest that is indexed, or accept the gap. |
| `empty-selector`, `unsupported-selector-expression` | A selector is empty or uses `matchExpressions`. The provider gives no edge for it. Use `matchLabels`, or accept the gap. |
| Others | The capability profile states the limit for the whole family. The [results reference](../reference/results.md#loss-modes) lists every loss mode. |

A record with the status `unknown` and the reason `index result unavailable` needs an index result: run `concordia index` so that `check` can read the index result of the current graph.

## CO-CON-031: unsupported family

The provider build has no support for the family that the rule needs. Use a build that has a [capability profile](../reference/capability-profiles.md) for it, or change the rule.

## CO-OBS-018: a project failed

The query for one project returned an error, so its coverage is `failed` and the rules that depend on it are `unknown`. Rules of other projects keep their results.

1. Read `coverage.scopeFailures` in `results.json` for the code and message of the failure.
2. Run `concordia index --project <name>` again and check the project.

## CO-CON-030: limit exceeded

A run went above a `limits` value of `concordia.yaml`. The diagnostic and `coverage.limits.breaches` give the limit, the configured value, and the observed value. Concordia keeps all evidence, and rules without a finding are `unknown`.

1. Raise the limit, or narrow the projects (`include` and `exclude`) so that the graph is smaller. See [limits](../reference/workspace.md#limits).
2. For `limits.traversal.maxVisited`, a deep dependency graph stopped a cycle or path search. A rule with a finding keeps it; a rule without a finding is `unknown`.

## Change what unknown does

The `check.unknown` key in `concordia.yaml` (or `--unknown` on the command line) sets what an `unknown` verdict does:

| Value | Effect |
|---|---|
| `fail` (default) | The run exits with code 4 if no finding already causes exit code 1. |
| `warn` | The verdict stays in the report, and the run does not exit with code 4. |
| `ignore` | The run does not exit with code 4, and the pretty output does not show the unknown rule. `results.json` still records the rule and its verdict. |

```bash
concordia check --unknown warn
```

A rule can override the workspace value with `evidence.onUnknown`.

> **Note:** `warn` and `ignore` hide a gap in your evidence from CI. Use them for a short time, and fix the cause.

## Confirm the result

Run `concordia check`. The rule has the verdict `pass` or `violation`, and the exit code is no longer 4.

## Next steps

- [Verdicts and coverage](../explanation/verdicts-and-coverage.md): why unknown fails by default.
- [Run in CI](run-in-ci.md)
- [Results reference](../reference/results.md) and [diagnostics reference](../reference/diagnostics.md)
