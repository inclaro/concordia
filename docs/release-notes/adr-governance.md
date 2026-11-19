# ADR governance release notes

This release adds a model of the decisions behind your architecture intent, and a follow-up that extends `check.rollback` to it and fixes the form of decision IDs. This page lists what a user can see change. It covers `add-adr-governance` and `refine-adr-governance-rollback`.

## Decision register and workspace key

- A decision register is a closed-schema YAML file with `kind: DecisionRegister`. For each decision it declares a stable ID, an optional path pin, the expected lifecycle (`active` or `historical`), the subjects it governs, the policy rules that enforce it (pinned to a pack version), whether enforcement is required, decision-scope categories, and explicit `released` links. See [ADR governance reference](../reference/adr-governance.md#decision-register).
- A new workspace key `adr` has `files` (register globs), `lifecycle`, and `triggers`. A profile can set it. See [`adr`](../reference/workspace.md#adr).
- The code graph supplies the decision facts: title, status, and supersession. A document mention and the CALM `adrs` array never create a governance link.

## What Concordia checks

- Committed presence: an ADR counts as present only when its file is tracked in the evaluated revision and the indexed text equals the committed text.
- Lifecycle profile: only `accepted` is active by default. `proposed`, `rejected`, `deprecated`, and `superseded` cannot be made active. The original status stays on every result.
- Supersession history: edges and superseded-by metadata become one directed history. Cycles, missing targets, duplicate IDs, contradictory status, and competing successors are findings. A `proposed` or `draft` successor is pending. An ambiguous history selects no active decision.
- Replacement review: a replaced decision raises a finding that lists its governed subjects and enforcement rules. Governance moves to a successor only when the register declares it.
- Presence, applicability, and enforcement coverage are separate results for each decision.
- Triggers: `adr.triggers` names changes that need a decision. No trigger exists by default.

## The `adr-governance` pack

Built-in pack `adr-governance`, version `1.0.0`. Add `- use: builtin:adr-governance` to `policies.packs`.

| Rule | What it checks |
|---|---|
| `GOV-ADR-001` | Required decisions are committed in the evaluated revision. |
| `GOV-ADR-002` | A registered decision is active for the architecture it governs. |
| `GOV-ADR-003` | A mandatory rule is justified by an active decision. |
| `GOV-ADR-004` | A decision that requires enforcement links a mandatory rule. |
| `GOV-ADR-005` | The supersession history of decisions is consistent. |
| `GOV-ADR-006` | An architecturally significant change has a decision. |

- The pack is opt-in, but it is not advisory by default. No rule declares `enforcement`, so a violation gates like a code rule: exit code 1 at or above `check.failOn`. Set a mode with `check.enforcement` for the family `decision-governance`.
- Without a register, every rule is `not-applicable`. `CO-ADR-007` says that a register is configured and the pack is not enabled.
- The rules are not typed obligations. The family `decision-governance` maps to the area `adrGovernance`.

## Results and commands

- `areas.adrGovernance` has `verdict` and `counts`. A new optional top-level `decisionSummary` has the per-decision detail, and `inputs.decisions[]` lists the register files and the lifecycle profile. Both are omitted when the feature is unused. `schemaVersion` stays `1.1`. See [Decision summary](../reference/results.md#decision-summary).
- `concordia adr validate [--profile <name>]` checks the declarations offline. See [adr validate](../reference/cli.md#adr-validate).
- New diagnostics `CO-ADR-001` to `CO-ADR-009`. See [Diagnostics](../reference/diagnostics.md).
- `pretty` has an ADR governance section, and `explain` covers governance findings.
- With `--changed`, the governance rules run on the full scope.

## Rollback covers governance

`check.rollback` now runs the rules of the `decision-governance` family as `shadow`, as it does for typed obligations and review rules. Code rules and `view-currency` rules are not affected. Findings and bundles are still recorded. The rollback message and the `pretty` notice say that typed and governance checks were disabled, and output under a rollback is not a successful governance check. A rollback needs a reason, an authority, and an expiry, and it fails with `CO-WRK-018` after the expiry. See [`check.rollback`](../reference/workspace.md#checkrollback) and [Roll out new obligations](../how-to/roll-out-new-obligations.md).

## Canonical decision IDs (breaking for some registers)

- A register ID must be `ADR-<number>`. The prefix can be in any case, and leading zeros are allowed. Any other form (`ADR 7`, `ADR_0007`, `ADR7`) is `CO-ADR-001` (exit 2) in `adr validate` and `check`, and names the ID.
- Every decision is named in canonical form (`ADR-7`) in the summary, findings, messages, `pretty`, and `explain`, whatever the register spelling.
- Values in `results.json` change for a register that writes leading zeros or lower case: `decisionSummary.decisions[].id`, `ruleJustification[].linkedBy`, the finding `decision.id`, `subject`, and messages. A consumer that matched the register spelling must match the canonical form.
- Fingerprints, baselines, and waivers are unaffected.

## What needs your action

- Rewrite register IDs that are not `ADR-<number>`.
- Update any consumer of `results.json` that compares decision IDs by the register spelling.
- To use the checks, add `adr.files` and the pack. See [Govern architecture decisions](../how-to/govern-architecture-decisions.md).
