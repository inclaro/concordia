# How to roll out new obligations

This guide shows you how to introduce typed obligations (infrastructure and runtime rules) so that they gate your pipeline only when you trust them. You move a rule family through `shadow` and `advisory` to `mandatory`, per environment, and you can switch new enforcement off with `check.rollback`.

## Prerequisites

- A workspace that passes `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- Declared `environments` and bindings for the subjects of your rules. See [bind code to architecture](bind-code-to-architecture.md).
- A policy file with at least one typed rule (a rule with an `obligation` block). See [write policies](write-policies.md).

## Know the enforcement modes

| Mode | Shown in pretty, SARIF, and JUnit | In `results.json` and the manifest | Counts in the exit code |
| --- | --- | --- | --- |
| `mandatory` | Yes | Yes | Yes |
| `advisory` | Yes, with enforcement `advisory` (SARIF level `note`) | Yes | No |
| `shadow` | No | Yes | No |

A typed rule without a mode is `advisory`. A rule that uses only the code evidence families is `mandatory`. Concordia never changes a mode itself: only your workspace does.

## Follow the MIG-03 sequence

1. **Start in shadow.** Declare the rule family in `check.enforcement` with `mode: shadow`. The findings are written to `results.json` and nothing appears in the reports or the exit code. Nobody is blocked and nobody sees new noise.

   ```yaml
   check:
     enforcement:
       - family: deployment-security
         mode: shadow
   ```

2. **Review the results.** Read `build/concordia/results.json`. Each finding has `enforcement: shadow`, the `environment`, and the `obligationId`. Fix the `unknown` results first: they mean that a binding, an environment, or a coverage guarantee is missing. See [resolve unknown verdicts](resolve-unknown-verdicts.md).

3. **Move to advisory.** Change the mode to `advisory`. The findings now appear in the reports with the status `advisory` and the exit code stays 0. Teams see what would block.

   ```yaml
   check:
     enforcement:
       - family: deployment-security
         mode: advisory
   ```

4. **Gate one environment.** Make the family `mandatory` where you are ready, and keep it `advisory` elsewhere. The first entry that matches wins, so write the narrow entries first.

   ```yaml
   check:
     enforcement:
       - family: deployment-security
         environment: staging
         mode: mandatory
       - family: deployment-security
         environment: production
         mode: advisory
   ```

   A violation in `staging` now gives exit code 1. The same violation in `production` does not. See [run Concordia in CI](run-in-ci.md#gate-by-environment).

5. **Gate the rest.** When the other environments pass in `advisory`, change them to `mandatory` too.

## Review with the rollout report

Before you change a mode, ask for the rollout report. It reads the run that `check` just made and changes nothing: no mode, no verdict, no exit code.

```sh
concordia check --rollout-report
```

The command writes `build/concordia/rollout-report.json` (canonical JSON; use `--rollout-report=<path>` for another path) and prints a summary on stderr:

```text
rollout report (build/concordia/rollout-report.json)
  deployment-security / staging: advisory; pass 3, violation 1, unknown 2, not-applicable 0, error 0
    unknown CO-DEP-010 billing-service: binding-gap; missing binding
    unknown CO-DEP-010 ledger-service: coverage-incomplete; missing coverage uses-image
    would block if mandatory: CO-DEP-010 3f9a1c0d22b4 billing-service runs an image with a mutable tag
  deployment-security / production: shadow; pass 4, violation 0, unknown 0, not-applicable 0, error 0
```

Each row is one rule family in one environment. Read it in this order:

1. **The `unknown` list.** Each entry has the reason and what is missing. A missing `binding` means that an observed entity has no binding to a CALM element: bind it, see [resolve unknown verdicts](resolve-unknown-verdicts.md). A missing `coverage` names the evidence family or predicate that is not complete in that scope: index the files, or accept that the family cannot give a definite answer yet. An `unknown` with `wouldBlock: true` fails the run (exit code 4) once the family is `mandatory`.
2. **The `wouldBlock` list.** These are the live violations of a family that is `advisory` or `shadow`. If the family were `mandatory`, each one would give exit code 1. Fix them, or waive them with a reason and an owner, before you change the mode. Waived and baselined findings are not listed.
3. **The verdict counts.** A family with no `unknown` and an empty `wouldBlock` list is ready for the next step of the sequence.

Concordia does not turn a gate to `mandatory` by itself. You make that change in `check.enforcement`.

## Know what `mandatory` needs

A `mandatory` rule needs these guarantees. If one is absent, `check` stops with `CO-POL-018` and exit code 2. The diagnostic names the rule, the environment, and the absent guarantee, and no result is produced.

| Guarantee | What to do when it is absent |
| --- | --- |
| Environment attribution: the environment is declared under `environments` | Declare the environment with the `sources` that belong to it. |
| Family maturity `stable`: the provider profile marks the evidence family of the rule `stable` | Keep the rule `advisory` until the profile marks the family `stable`. |
| Coverage predicate: the run has a coverage record for the family in the environment | Index the repository and check that the environment sources include the files. |

The rules of the `assurance` pack and the `require.effectiveAccess`, `require.checkResult`, and `require.claim` rules cannot be `mandatory` yet: their families have the maturity `advisory`, and a `mandatory` mode gives `CO-POL-018` with the guarantee `family ... has maturity advisory`. Keep them `advisory` or `shadow`. To block a merge on a quality outcome, declare a quality scenario with `enforcement: mandatory` or list its area in `assurance.quality.requiredAreas`. See [add stronger evidence](add-stronger-evidence.md#declare-quality-scenarios).

Concordia never lowers a `mandatory` rule to `advisory` or `shadow` to make the error go away. If you cannot provide the guarantee, set `advisory` or `shadow` in `check.enforcement`: an explicit `advisory` runs without an error.

## Roll out review and quality rules

The review rules start `advisory`, like typed rules. To move them on, use the same sequence with `pack: review-currency` in the `check.enforcement` entry. A `mandatory` review rule also needs one authority and a trust anchor, or `check` stops with `CO-ASM-004` (exit code 2). See [review and approve assessments](review-and-approve-assessments.md) and [run Concordia in CI](run-in-ci.md#gate-on-review-currency).

```yaml
check:
  enforcement:
    - pack: review-currency
      mode: shadow
```

A quality scenario has its own `enforcement` key, `advisory` by default. Start a scenario as `advisory`, read its verdict in `areas.qualityEvaluation`, and set `mandatory` when its verdict is stable. A `check.enforcement` entry can still set another mode for the scenario rule. See [declare quality scenarios](add-stronger-evidence.md#declare-quality-scenarios).

## Roll back

If new obligations block work and you cannot fix the cause now, declare a rollback:

```yaml
check:
  rollback:
    reason: "false positives on the new ingress rules (INC-1234)"
    authorizedBy: "jane.doe@example.com"
    expires: "2026-11-15"
```

While the rollback is active:

- Every typed rule runs as `shadow`, whatever `check.enforcement` and the rule say. The same holds for these rules:
  - the rules of the `assurance` pack and your own `require.effectiveAccess`, `require.checkResult`, and `require.claim` rules;
  - the rules of the `decision-governance` family (`GOV-ADR-001` to `GOV-ADR-006`);
  - every review rule (`REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, and `REV-GOV-001` of the `review-currency` pack);
  - the quality scenario rules (`quality:<id>` and `quality:area:<area>`), including a scenario that you set to `mandatory`.

  A review rule that you set to `mandatory` is `shadow` too, and so is a `mandatory` quality rule, so neither gives exit code 1 while the rollback is active. Rules that use only the code evidence families, and `view-currency` rules, are not changed.
- The findings and the evidence bundles are still recorded in `results.json`.
- The run manifest and the `summary.rollback` object of `results.json` record the reason, who authorized it, and the expiry date, and the summary says that new enforcement was disabled. Do not treat the output as a successful typed, governance, or review check.

`expires` is a date (`YYYY-MM-DD`) and the rollback is active through the end of that day (UTC). After that day `check` fails with `CO-WRK-018` and exit code 2, so a rollback cannot stay in place by accident. Remove the `rollback` key to enforce again, or set a later date with a new reason.

## Check the impact of an upgrade

A new CBM build, capability profile, CALM release, or normalization version can change which evidence Concordia sees. `concordia lock update` pins the new values in `.concordia.lock` and writes `build/concordia/digest-impact.json`. The report lists:

- the capability changes: the families that the new profile adds, improves, or no longer supports, and the recordings (`fixtures`) that validate them;
- for the latest run, the digests of the normalized graph, the findings, the architecture, and the policy before and after, with the findings whose fingerprints are removed or added;
- whether equivalence is shown. If a regression exists, the latest run cannot be evaluated again, or a digest or verdict changed, the report says `equivalence not shown`.

Treat `equivalence not shown` as a reason to move the affected families back to `shadow` or `advisory`, run `concordia check --rollout-report`, and review the new `unknown` and `wouldBlock` lists before the next gate. See the [`lock update` reference](../reference/cli.md#lock-update).
