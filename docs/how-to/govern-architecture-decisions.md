# How to govern architecture decisions

This guide shows you how to register architecture decision records (ADRs) in a decision register, link them to the rules that enforce them, replace a decision, require a decision for a change, and roll the checks out without blocking anyone. The example is the workspace in `test/fixtures/adr` in the Concordia repository: an orders service and its database, with `ADR-0003` (the orders service owns `orders-db`) and `ADR-0007` (a managed database; it supersedes `ADR-0003`).

## Prerequisites

- A workspace that passes `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- ADR files in the repository, committed to Git. Concordia reads the committed revision, not the working tree.
- An index of the project (`concordia index`). The decision facts (title, status, supersession) come from the `ADR` nodes that CBM indexes. Include the ADR directory in `observation.projects[].include`.
- The rule that enforces a decision is in a loaded policy pack. The fixture links `CORE-DB-001` of `builtin:core`.

## Register a decision

ADR governance needs three things: a register file, the `adr` key in `concordia.yaml`, and the built-in pack that holds the rules.

1. Write the register. It says what a decision governs and which rules enforce it. The ADR text never does; see the [register reference](../reference/adr-governance.md#decision-register). `governance/decisions.yaml` in the fixture:

   ```yaml
   apiVersion: concordia.io/v1alpha1
   kind: DecisionRegister
   decisions:
     - id: ADR-0003
       path: docs/adr/0003-orders-own-database.md
       categories: [system-boundaries]
       governs:
         - ref: calm:node:orders-db
         - ref: calm:relationship:orders-to-db
         - ref: calm:interface:orders-db/sql
         - select: {tags: [orders-data]}
       enforcement: required
       enforcedBy:
         - rule: CORE-DB-001
           pack: {id: core, version: 1.0.0}
   ```

   Write the `id` as `ADR-<number>`, with or without leading zeros and in any case: `ADR-0003`, `adr-003`, and `ADR-3` are the same decision. Reports and `results.json` always show the canonical form, `ADR-3`, which is also how CBM labels the file. Any other spelling (`ADR 3`, `ADR_3`, `ADR3`) is rejected with `CO-ADR-001`.

2. Point the workspace at the register and enable the pack:

   ```yaml
   policies:
     packs:
       - use: builtin:core
       - use: builtin:adr-governance
   adr:
     files: [governance/decisions.yaml]
   ```

   Without the `adr` key the rules have nothing to evaluate. With the key and without the pack, no rule runs and `CO-ADR-007` says so. See [workspace: adr](../reference/workspace.md#adr).

3. Commit the register and the ADR files, then validate:

   ```bash
   concordia adr validate
   ```

   The command is offline. It checks the lifecycle profile and triggers, the register schema and duplicate IDs, that every `ref` and `select` exists in the architecture, and that every rule link names a loaded pack, version, and rule. The first problem gives a `CO-ADR-001` to `CO-ADR-005` diagnostic and exit code 2. See [diagnostics](../reference/diagnostics.md) and the [CLI reference](../reference/cli.md#adr-validate).

4. Index and check:

   ```bash
   concordia index
   concordia check --rule GOV-ADR-001
   ```

   Each registered decision has three separate states: **presence** (the file is committed and the indexed text equals it), **applicability** (the decision is active, not superseded), and **enforcement** (a mandatory rule is linked). One does not imply another. See [presence](../reference/adr-governance.md#presence).

## Link a rule to a decision

The `enforcedBy` list of an entry links a rule to the decision that justifies it. A rule has no version of its own, so the link names the pack and its version:

```yaml
enforcedBy:
  - rule: CORE-DB-001
    pack: {id: core, version: 1.0.0}
```

`concordia adr validate` reports an unknown rule, pack, or version with `CO-ADR-004` and names the version that is loaded. After you upgrade a pack, validate again and update the version.

With `enforcement: required`, `GOV-ADR-004` fails when no mandatory rule is linked, and `GOV-ADR-003` fails when a mandatory rule is linked only by decisions that are no longer active. See [obligations](../reference/adr-governance.md#obligations).

## Replace a decision

`ADR-0007` says that it supersedes `ADR-0003`, and `ADR-0003` still says `Accepted`. Run:

```bash
concordia index
concordia check --rule GOV-ADR-002
```

`GOV-ADR-002` reports a `violation` with the reason `superseded` on `ADR-3` (`ADR-0003` in the register). The status text of `ADR-3` lags, and the report says so without changing it. The finding lists the subjects that `ADR-0003` governs, the rule it links, and `ADR-7` as a candidate successor with `applied: false`. Concordia never moves governance to a successor by itself: you do that in the register, and the review is the point.

Edit the register so that the successor is registered, and the old decision is expected to be `historical`:

```yaml
decisions:
  - id: ADR-0003
    path: docs/adr/0003-orders-own-database.md
    expect: historical
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    released:
      - ref: calm:interface:orders-db/sql
        reason: The managed database exposes no direct SQL interface to the orders service.
  - id: ADR-0007
    path: docs/adr/0007-orders-managed-database.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
```

Every subject and rule of a `historical` decision must be transferred: declared by another active decision, or listed under `released` with a reason. Here `ADR-0007` takes over the node, the relationship, and the rule, and `released` ends the interface. A rule is released with `rule: CORE-DB-001@core/1.0.0`. `released` is accepted only with `expect: historical`.

Commit the register, validate, index again (a commit makes the graph stale, `CO-OBS-008`), and check:

```bash
concordia adr validate
concordia index
concordia check --rule GOV-ADR-002
```

`GOV-ADR-002` now passes for both decisions. An untransferred subject or rule gives a `historical-link` finding that names it. A contradictory history (a cycle, two successors that do not supersede each other, a `superseded` ADR with no successor) makes every registered decision in that set `ambiguous`, and the result is `unknown` until a person fixes the ADRs. See [supersession history](../reference/adr-governance.md#supersession-history).

## Require a decision for a change

A trigger names a change that needs a decision. Add it to `adr.triggers`:

```yaml
adr:
  files: [governance/decisions.yaml]
  triggers:
    - id: TRG-ORDERS-CODE
      category: system-boundaries
      paths: ["services/orders/**"]
    - id: TRG-DB-LINK
      category: system-boundaries
      architecture: [relationship-added, relationship-removed]
      nodeTypes: [database]
```

A change to a file that matches `paths`, or an added or removed relationship of the architecture, raises the trigger. It is satisfied when a registered decision with the same `category` is added or modified in the same change set. Validate and run it on a change:

```bash
concordia adr validate
concordia index
concordia check --changed HEAD~1 --rule GOV-ADR-006
```

Without `--changed`, `GOV-ADR-006` is `not-applicable` and `CO-ADR-009` says that triggers were not evaluated. The rules never use the change set to reduce their scope: the other governance rules run in full, and `CO-CON-027` lists them. See [trigger evaluation](../reference/adr-governance.md#trigger-evaluation) and [run in CI](run-in-ci.md).

## Link a quality scenario to a decision

A quality scenario or a scenario finding can name the decision that explains its design with `addressedBy`. The entries are ADR ids and compare by canonical form, so `ADR-0007` and `ADR-7` are the same decision:

```yaml
scenarios:
  - id: orders-db-failover
    addressedBy: [ADR-7]
```

The scenario file is declared under `assurance.quality.scenarios`; see [declare quality scenarios](add-stronger-evidence.md#declare-quality-scenarios). The link reads the decision state of the run:

- With the pack `builtin:adr-governance` enabled and the decision registered, an `active` decision gives an active link. A `superseded` or `inactive` decision gives a link that is not active and flags the scenario for a review of its rationale. An `ambiguous` or `unknown` decision gives an `unknown` link.
- An id that is not a registered decision stops `check` with `CO-QUA-002` (exit code 2).
- Without a register or without the pack, every link is `unknown` with the detail `adr-governance-not-available`, and the file still loads.

The link never changes the verdict of the scenario. When you replace a decision (see above), update or review the scenarios that name it.

## Roll out in shadow, then advisory

The governance rules are `mandatory` by default: once the pack is enabled, a governance violation fails `check`. To introduce them without breaking builds, move them to a gate in the same sequence as other new obligations; see [roll out new obligations](roll-out-new-obligations.md). The family of the rules is `decision-governance`:

1. **Shadow.** Nothing appears in the reports or the exit code. The findings are in `results.json` only.

   ```yaml
   check:
     enforcement:
       - family: decision-governance
         mode: shadow
   ```

2. **Review the area.** Read the verdict, the counts, and the per-decision states:

   ```bash
   concordia check
   jq '.areas.adrGovernance, (.decisionSummary.decisions[] | {id, presence: .presence.state, applicability: .applicability.state, enforcement: .enforcement.state})' build/concordia/results.json
   ```

   Fix the `unknown` results first: a decision that is not committed, a provider without ADR evidence, or an ambiguous history. Add register entries for decisions that `decisionSummary.candidates` lists as `unregistered-decision`. A candidate is never a verdict.

3. **Advisory.** Change `mode: shadow` to `mode: advisory`. The findings are now in the reports (SARIF level `note`) and the exit code stays 0.

4. **Gate.** When the area is clean, remove the entry (the rules return to their default) or set `mode: mandatory`. `mandatory` needs the guarantees in [know what mandatory needs](roll-out-new-obligations.md#know-what-mandatory-needs); `CO-POL-018` names an absent one. Use `concordia check --rollout-report` to see what would block first.

The reference for the keys is [workspace: check.enforcement](../reference/workspace.md#checkenforcement). A baseline or a waiver changes the disposition of a finding, never the verdict of its rule; see [manage baselines and waivers](manage-baselines-and-waivers.md).

## Roll back

If the governance checks block work and you cannot fix the cause now, use one of these. Findings and bundles that were already produced keep their identity and are never shown as a pass of a new check.

- Declare `check.rollback` with a reason, who authorized it, and an expiry date. Until that day the `GOV-ADR-*` rules run as `shadow`, also over a `mandatory` entry in `check.enforcement`. So do every typed rule, every review rule, and every quality scenario rule; the findings stay in `results.json`, the run records the rollback, and after the expiry `check` fails with `CO-WRK-018`. This is the audited emergency switch; see [roll back](roll-out-new-obligations.md#roll-back).

  ```yaml
  check:
    rollback:
      reason: "register migration in progress (ARCH-42)"
      authorizedBy: "jane.doe@example.com"
      expires: "2026-12-15"
  ```

- Set the family to `shadow` or `advisory` under `check.enforcement` (`family: decision-governance`), as in the rollout above. The rules keep running and the exit code no longer depends on them, but the setting records no reason and does not expire: use it for a planned rollout, not as a rollback.
- Remove `builtin:adr-governance` from `policies.packs`. No rule runs and `CO-ADR-007` says that the register is not evaluated.
- Remove the `adr` key. ADR governance is off and the result has no `decisionSummary`.

## Next steps

- [ADR governance reference](../reference/adr-governance.md): the register, lifecycle profile, presence, history, obligations, and the reasons of every finding.
- [Results reference](../reference/results.md#decision-summary): the `adrGovernance` area and `decisionSummary`.
- [Policies reference](../reference/policies.md#decision-governance-rules-the-decisions-key): the `decisions` rule key and the pack.
- [How Concordia works](../explanation/how-concordia-works.md#how-the-decision-history-picks-an-active-decision): why the history never picks a winner.
