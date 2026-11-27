# Enforce architecture decisions

A decision that no check enforces is a wish. The bank decided that only the ledger service touches the ledger database, and wrote it down in `ADR-0001`. A year later the ADR is marked deprecated in a cleanup, the rule that enforced it still blocks pull requests, and nobody can say which decision is in force.

## The rule

The rules `GOV-ADR-001` to `GOV-ADR-004` are in the built-in pack `builtin:adr-governance`. This is how the pack defines them:

```yaml
- id: GOV-ADR-001
  name: Required decisions are committed in the evaluated revision
  severity: error
  family: decision-governance
  decisions: { obligation: presence }

- id: GOV-ADR-002
  name: A registered decision is active for the architecture it governs
  severity: error
  family: decision-governance
  decisions: { obligation: applicability }

- id: GOV-ADR-003
  name: A mandatory rule is justified by an active decision
  severity: error
  family: decision-governance
  decisions: { obligation: justification }

- id: GOV-ADR-004
  name: A decision that requires enforcement links a mandatory rule
  severity: error
  family: decision-governance
  decisions: { obligation: enforcement }
```

The pack also gives each rule a `rationale` and a `remediation`. The rules read the decision register of the example, `governance/decisions.yaml`:

```yaml
apiVersion: concordia.io/v1alpha1
kind: DecisionRegister
decisions:
  - id: ADR-0001
    path: docs/adr/0001-ledger-database-owned-by-ledger-service.md
    expect: active
    categories: [data-ownership]
    governs:
      - ref: calm:node:ledger-db
      - ref: calm:relationship:ledger-to-database
    enforcement: required
    enforcedBy:
      - rule: PAY-DB-001
        pack: {id: payments-architecture, version: 1.0.0}
```

`concordia.yaml` points at the register with `adr.files: [governance/decisions.yaml]` and enables `builtin:adr-governance`.

## What Concordia reads

- **The committed ADR file** (`docs/adr/0001-ledger-database-owned-by-ledger-service.md`). CBM indexes it as an `ADR` node with its title and status. Concordia compares the indexed text with the committed text.
- **The Git revision.** Only a file that is tracked and unchanged in the evaluated revision counts as present.
- **The decision register** (`governance/decisions.yaml`). It says which architecture elements a decision governs and which rules enforce it.
- **The compiled rules of the run.** Concordia reads the severity and the `onUnknown` setting of each linked rule, after every override.

The register says what a decision governs. The status comes from the committed ADR text, not from the register. A rule counts as enforcing only when the compiled rule is mandatory, not because the register links it.

## How Concordia decides

1. `GOV-ADR-001` (presence): the file at the register `path` is tracked in the evaluated revision, and the indexed text equals the committed text. An untracked or deleted file is a violation. An uncommitted edit is `unknown`.
2. `GOV-ADR-002` (applicability): Concordia maps the status of the committed ADR to a lifecycle class. `Accepted` is active. `Proposed`, `Deprecated`, and `Rejected` are inactive. A decision that the register expects to be `active`, and that is not, is a violation.
3. `GOV-ADR-003` (justification): for each mandatory rule that a decision links, at least one linking decision must be active. A mandatory rule whose decisions are all inactive is a violation.
4. `GOV-ADR-004` (enforcement): a decision with `enforcement: required` must link at least one mandatory rule. A rule is mandatory when its severity is at or above `check.failOn` and its `onUnknown` is `fail`. A decision with only advisory rules, or with no rules, is a violation.
5. A rule with no unit to judge is `not-applicable`. For example, `GOV-ADR-003` has no unit when `--rule` leaves out every linked rule.

## What you still declare

- **The register.** You decide which decision governs which nodes and relationships, and which rules enforce it. The ADR text is never read for this. `concordia adr validate` checks that every `ref` exists in the model and that every rule link names a loaded pack, version, and rule.
- **The status of each ADR.** Concordia reads the status that you write in the file. It does not decide whether the decision is still right.
- **That the linked rule really enforces the decision.** Concordia checks that `PAY-DB-001` is mandatory. It does not check that `PAY-DB-001` tests what `ADR-0001` says. See [protect database ownership](protect-database-ownership.md) for that rule.
- **An index with ADR facts.** Without the `ADR` nodes from CBM, presence and applicability are `unknown`. CBM 0.12.1 extracts Markdown only with `observation.indexMode: full`, which the example sets.

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch Git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/enforce-architecture-decisions && mkdir -p /tmp/concordia-usecases/enforce-architecture-decisions && cp -R docs/examples/payments/. /tmp/concordia-usecases/enforce-architecture-decisions && cd /tmp/concordia-usecases/enforce-architecture-decisions && sed -i.bak 's/payments-example/payments-uc-enforce-architecture-decisions/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-enforce-architecture-decisions
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Validate the register:

```bash
cd /tmp/concordia-usecases/enforce-architecture-decisions && concordia adr validate --no-color
```

```output
adr: 1 decision(s) in 1 register file(s) valid; 1 rule link(s) checked; active statuses: accepted
```

Run the full check and keep the ADR governance area. `GOV-ADR-003` needs `PAY-DB-001` in the run, so do not filter with `--rule` here:

```bash
cd /tmp/concordia-usecases/enforce-architecture-decisions && concordia check --no-color 2>/dev/null | sed -n '/^ADR governance/,/enforcement enforced/p'
```

```output
ADR governance: pass (5 pass, 1 notApplicable)
  ADR-1 accepted (docs/adr/0001-ledger-database-owned-by-ledger-service.md:1)
    presence committed; applicability active; enforcement enforced (required; rules: PAY-DB-001@payments-architecture/1.0.0)
```

`ADR-0001` is committed, active, and enforced by `PAY-DB-001`. Five rules pass. `GOV-ADR-006` is `not-applicable` without `--changed`; see [require a decision for significant changes](require-a-decision-for-significant-changes.md).

Now someone marks the ADR as deprecated, and nobody updates the register. Change the status, commit, and index:

```bash
cd /tmp/concordia-usecases/enforce-architecture-decisions && sed -i.bak 's/^Status: Accepted/Status: Deprecated/' docs/adr/0001-ledger-database-owned-by-ledger-service.md && rm docs/adr/*.bak && git commit -q -am "Deprecate ADR-0001" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-enforce-architecture-decisions
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check again:

```bash
cd /tmp/concordia-usecases/enforce-architecture-decisions && concordia check --no-color 2>/dev/null | sed -n '/^ADR governance/,/^  Fix: Link the rule/p'
```

```output
ADR governance: violation (2 violation, 2 pass, 2 notApplicable, 2 findings)
  ADR-1 deprecated (docs/adr/0001-ledger-database-owned-by-ledger-service.md:1)
    presence committed; applicability inactive; enforcement not-applicable (required; rules: PAY-DB-001@payments-architecture/1.0.0)

FAIL GOV-ADR-002 A registered decision is active for the architecture it governs
  Why it matters: A decision that is proposed, deprecated, rejected, or superseded no longer governs the subjects it names. Rules and nodes that still depend on it rest on a decision that is not in force.
  ADR-1: 1 finding(s)
    docs/adr/0001-ledger-database-owned-by-ledger-service.md:1
      Decision: ADR-1 (status: deprecated)
      Governs: calm:node:ledger-db, calm:relationship:ledger-to-database
      Enforced by: PAY-DB-001@payments-architecture/1.0.0
  Fix: Accept the decision, or move its links and subjects to the decision that replaces it, or release them in the register. Resolve an ambiguous status with an explicit status mapping.

FAIL GOV-ADR-003 A mandatory rule is justified by an active decision
  Why it matters: A mandatory rule whose linking decisions are all inactive enforces a choice that the team has not currently accepted.
  PAY-DB-001: 1 finding(s)
    governance/decisions.yaml:13
      Decision: ADR-1 (status: deprecated)
      Governs: calm:node:ledger-db, calm:relationship:ledger-to-database
      Enforced by: PAY-DB-001@payments-architecture/1.0.0
  Fix: Link the rule from an active decision, or activate the decision that justifies it. If the rule no longer has a decision behind it, review the rule.
```

Two rules fail. `GOV-ADR-002` says that the register expects `ADR-1` to be active, and it is deprecated. `GOV-ADR-003` says that `PAY-DB-001` is still mandatory, but no active decision justifies it. `GOV-ADR-004` is now `not-applicable`, because the decision is no longer active. Reports show the canonical ID `ADR-1` for `ADR-0001`.

Each rule fails the run on its own:

```bash
cd /tmp/concordia-usecases/enforce-architecture-decisions && concordia check --rule GOV-ADR-002 --no-color 2>/dev/null | sed -n '/^ADR governance/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
ADR governance: violation (1 violation, 1 findings)
1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

To fix it, accept the decision again, or register the decision that replaces it and move the links. See [replace a decision](../how-to/govern-architecture-decisions.md#replace-a-decision).

## Make it block a merge

The rules fail the build as shipped. Their severity is `error`, and the pack declares no `enforcement`, so a violation gates like the violation of a code rule: `check` exits with code 1.

To introduce the rules without blocking anyone, set the family `decision-governance` to `shadow` or `advisory` under `check.enforcement`, and remove the entry when the area is clean:

```yaml
check:
  enforcement:
    - family: decision-governance
      mode: advisory
```

See [roll out in shadow, then advisory](../how-to/govern-architecture-decisions.md#roll-out-in-shadow-then-advisory).

## Related

- [Govern architecture decisions](../how-to/govern-architecture-decisions.md)
- [ADR governance reference](../reference/adr-governance.md)
- [Obligations](../reference/adr-governance.md#obligations)
- [Policies reference: the `decisions` key](../reference/policies.md#decision-governance-rules-the-decisions-key)
- [Require a decision for significant changes](require-a-decision-for-significant-changes.md)
- [Protect database ownership](protect-database-ownership.md)
