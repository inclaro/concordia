# Require a decision for significant changes

Some changes need a decision before they ship, and code review does not catch them. At a bank, a pull request lets the nightly report read the ledger database. The reviewer sees one new line in the model and approves it. Nobody records why the ledger lost its single owner, and the next audit asks.

## The rule

`GOV-ADR-006` is in the built-in pack `builtin:adr-governance`. This is the rule as the pack defines it:

```yaml
- id: GOV-ADR-006
  name: An architecturally significant change has a decision
  severity: warning
  family: decision-governance
  decisions: { obligation: scope }
  rationale: A change that raises a configured trigger (paths, node types, tags, or architecture changes) is architecturally significant and should be recorded in a decision.
  remediation: Add or update a decision that governs the changed subjects in the same change set, or adjust the trigger if the change is not significant.
```

The rule has nothing to check until you say which changes are significant. The example declares one trigger in `concordia.yaml`, and raises the severity of the rule to `error`:

```yaml
policies:
  packs:
    - use: builtin:adr-governance
      rules:
        GOV-ADR-006: { severity: error }
adr:
  files: [governance/decisions.yaml]
  triggers:
    - id: TRG-DATA-OWNERSHIP
      category: data-ownership
      architecture: [relationship-added]
      nodeTypes: [database]
```

A new relationship to or from a database raises `TRG-DATA-OWNERSHIP`. A decision with the category `data-ownership` satisfies it.

## What Concordia reads

- **The change set.** With `check --changed <base>`, Concordia reads the list of files that changed between the base reference and the evaluated revision from Git.
- **The model at both revisions.** Concordia reads `architecture/payments.calm.json` at the base reference and at the evaluated revision, and compares them. The difference gives the added and removed nodes and relationships.
- **The decision register** (`governance/decisions.yaml`) and the committed ADR files. Concordia reads the categories of each decision and checks whether its file is in the change set.

The pull request description, the commit message, and the ADR text do not count. Only a registered decision whose committed file changed in the same change set satisfies a trigger.

## How Concordia decides

1. Without `--changed`, there is no change set. The rule is `not-applicable`, and `CO-ADR-009` says that triggers were not evaluated.
2. With `--changed`, Concordia loads the model at the base reference and compares it with the current model.
3. A trigger is raised when a changed file matches its `paths`, or when the model change has a kind in its `architecture` list. `nodeTypes` keeps only changes that touch a node of that type. A relationship matches when one of its ends matches.
4. A raised trigger is satisfied when a registered decision with the same category has its committed file in the change set, added or modified.
5. Raised and not satisfied is a `violation`, reason `trigger-unsatisfied`. A trigger that is not raised passes.
6. When the base model cannot be read and no path raised the trigger, the result is `unknown`.

## What you still declare

- **The triggers.** You decide which changes are significant. With no trigger, the rule never fails.
- **The base reference.** CI passes the target branch of the pull request to `--changed`. A wrong base gives a wrong change set.
- **The decision itself.** Concordia checks that a decision of the right category changed in the same change set. It does not check that the decision explains this change.
- **The category.** A decision with another category does not satisfy the trigger, even when it governs the same nodes.

## Try it on the example

You need `concordia`, CBM, `git`, and `jq` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch Git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/require-a-decision-for-significant-changes && mkdir -p /tmp/concordia-usecases/require-a-decision-for-significant-changes && cp -R docs/examples/payments/. /tmp/concordia-usecases/require-a-decision-for-significant-changes && cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && sed -i.bak 's/payments-example/payments-uc-require-a-decision-for-significant-changes/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-require-a-decision-for-significant-changes
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Run the rule without a change set:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && concordia check --rule GOV-ADR-006 --no-color 2>/dev/null | sed -n '/^ADR governance/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
ADR governance: not-applicable (1 notApplicable)
0 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 0
```

Without `--changed`, the rule is `not-applicable`, and `check` exits with code 0.

Now make the change from the pull request: declare that the reporting job reads the ledger database. Commit and index:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && jq '.relationships += [{"unique-id": "reporting-to-ledger-db", "description": "The nightly report reads the ledger tables.", "relationship-type": {"connects": {"source": {"node": "reporting-job"}, "destination": {"node": "ledger-db"}}}}]' architecture/payments.calm.json > calm.tmp && mv calm.tmp architecture/payments.calm.json && git commit -q -am "Declare the nightly report access to the ledger database" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-require-a-decision-for-significant-changes
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check the change against the previous commit. The `sed` command keeps the area, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && concordia check --changed HEAD~1 --rule GOV-ADR-006 --no-color 2>/dev/null | sed -n '/^ADR governance/p;/^FAIL GOV-ADR-006/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
ADR governance: violation (1 violation, 1 findings)
FAIL GOV-ADR-006 An architecturally significant change has a decision (full scope)
  Why it matters: A change that raises a configured trigger (paths, node types, tags, or architecture changes) is architecturally significant and should be recorded in a decision.
  TRG-DATA-OWNERSHIP: 1 finding(s)
    architecture/payments.calm.json
  Fix: Add or update a decision that governs the changed subjects in the same change set, or adjust the trigger if the change is not significant.
1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

The trigger is raised and no decision satisfies it, so the rule fails and `check` exits with code 1. The finding names the CALM file, `architecture/payments.calm.json`, because a change in the model raised the trigger. `results.json` shows the exact change:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && jq -c '.decisionSummary.triggers[]' build/concordia/results.json
```

```output
{"category":"data-ownership","id":"TRG-DATA-OWNERSHIP","raised":true,"raisedBy":["relationship-added:reporting-to-ledger-db"],"reason":"trigger-unsatisfied","result":"violation","satisfiedBy":[]}
```

The added relationship `reporting-to-ledger-db` ends at a database, so it raised `TRG-DATA-OWNERSHIP`. `satisfiedBy` is empty.

The teams write down the decision in the same pull request. Add `ADR-0002` with the category `data-ownership` to the register, commit, and index:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && cat > docs/adr/0002-nightly-report-reads-the-ledger.md <<'EOF'
# ADR-0002: The nightly report reads the ledger database until the ledger read API exists

Status: Accepted

Date: 2026-11-20

Deciders: Team Ledger, Team Finance, Team Architecture

Context: the nightly report needs the totals of the day. The ledger service has no read API for them yet.

Decision: the reporting job may read the ledger tables until the ledger read API is in production. It never writes them.

Consequences: PAY-DB-001 still reports the access, and a waiver records the exception.
EOF
cat >> governance/decisions.yaml <<'EOF'
  - id: ADR-0002
    path: docs/adr/0002-nightly-report-reads-the-ledger.md
    categories: [data-ownership]
    governs:
      - ref: calm:relationship:reporting-to-ledger-db
EOF
git add -A && git commit -q -m "Record ADR-0002" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-require-a-decision-for-significant-changes
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

The pull request now has two commits. Check both against the base:

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && concordia check --changed HEAD~2 --rule GOV-ADR-006 --no-color 2>/dev/null | sed -n '/^ADR governance/p;/^FAIL GOV-ADR-006/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
ADR governance: pass (1 pass)
0 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 0
```

```bash
cd /tmp/concordia-usecases/require-a-decision-for-significant-changes && jq -c '.decisionSummary.triggers[]' build/concordia/results.json
```

```output
{"category":"data-ownership","id":"TRG-DATA-OWNERSHIP","raised":true,"raisedBy":["relationship-added:reporting-to-ledger-db"],"result":"pass","satisfiedBy":["ADR-2"]}
```

The trigger is still raised, and `ADR-0002` satisfies it, so the rule passes.

## Make it block a merge

The rule needs two settings before it can block a merge:

- Run `concordia check --changed <base>` in CI, with the target branch of the pull request as the base. Without `--changed`, the rule is `not-applicable` and never fails. See [gate on ADR governance](../how-to/run-in-ci.md#gate-on-adr-governance).
- Raise the severity to `error`. The pack gives the rule the severity `warning`, which is below `check.failOn: error`. The example sets the override `GOV-ADR-006: { severity: error }` under `builtin:adr-governance` in `policies.packs`.

With both settings, a raised trigger without a decision makes `check` exit with code 1, as the example shows. `--changed` does not reduce the scope of the other governance rules. They still run on the full scope.

## Related

- [Require a decision for a change](../how-to/govern-architecture-decisions.md#require-a-decision-for-a-change)
- [Trigger evaluation](../reference/adr-governance.md#trigger-evaluation)
- [Triggers](../reference/adr-governance.md#triggers)
- [Enforce architecture decisions](enforce-architecture-decisions.md)
- [Keep the model in sync with code](keep-the-model-in-sync-with-code.md)
