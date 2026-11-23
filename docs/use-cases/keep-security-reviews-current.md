# Keep security reviews current

A security review covers the code that the reviewer saw. At a bank, the security team approves the threat model of the ledger service. Two months later a developer changes the ledger API, and the approval still looks valid. The audit finds a reviewed system that nobody reviewed.

## The rule

`REV-APR-001` is in the built-in pack `builtin:review-currency`. This is the rule as the pack defines it:

```yaml
- id: REV-APR-001
  name: A required assessment has a current verified approval
  severity: error
  family: assessment-currency
  assessmentCurrency: approval
  rationale: An assessment that nobody with authority approved for the subject as it is now, or whose approval expired, was revoked, or was bound to an earlier subject, is not review evidence for the architecture it covers.
  remediation: Get review of the assessment version and record a verified approval, or write a new assessment version for the changed subject. When currency is unknown, supply the missing evidence or fix the analysis configuration.
```

The rule checks the required assessments of the workspace. In the example, `concordia.yaml` requires the ledger threat model and names who may approve it:

```yaml
assessments:
  paths: [assessments]
  trustAnchor: { ref: main }
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [alice@example.com]
      paths: [assessments/approvals/**]
  required:
    - id: ledger-threat-model
      assessment: ASM-LEDGER
      type: security
      scope: { calmNodes: [ledger-service, ledger-db] }
      environment: production
      authority: security-review
```

The assessment `ASM-LEDGER` (`assessments/ledger-threat-model.yaml`) depends on two items: the implementation file `services/ledger-service/src/api/index.ts`, and the trust context of `ledger-db` in the model.

## What Concordia reads

- **The assessment document.** Concordia reads its version, scope, and dependencies. The `outcome` and `state` keys are what the author wrote. They are never an approval.
- **The current subject.** Concordia computes a digest of each dependency item from the committed code and the model: the content of `index.ts`, and the trust declaration of `ledger-db`.
- **Approval records** under `assessments/approvals/**`. A record binds the subject digest that the reviewer confirmed.
- **The trust anchor** (`main`). A record counts only when it is on the anchor branch. Concordia reads the authority configuration from the anchor, not from your working tree, so a change that adds a reviewer does not approve itself.

## How Concordia decides

1. The required entry `ledger-threat-model` names the assessment `ASM-LEDGER` and the authority `security-review`.
2. Concordia computes the current digest of each dependency item, and from them the subject digest.
3. Concordia looks for approval records of `ASM-LEDGER` version 1 on the trust anchor. The reviewer must be in the `reviewers` list of the authority, as the anchor states it.
4. No verified approval gives `review-required`, reason `no approval`.
5. An approval whose subject digest differs from the current one gives `review-required`, reason `subject digest mismatch`. The status names each item that changed.
6. An approval that binds the current subject, has not expired, and is not revoked gives `current`.
7. `REV-APR-001` is a `violation` for a required assessment that is not `current`. It is `unknown` when Concordia cannot compute an item, for example when the evidence for a dependency kind is missing.

## What you still declare

- **What the review covers.** You list the dependencies in the assessment. A change to a file that is not listed does not make the review stale.
- **The judgement.** Concordia records who approved which subject. It does not check that the review was good.
- **The authority.** You name the reviewers and the paths of their records.
- **The protection of the trust anchor.** Concordia reads local Git data. It does not check branch protection, `CODEOWNERS`, or required reviewers on the forge. If anyone can push to `main`, anyone can approve.

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch Git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/keep-security-reviews-current && mkdir -p /tmp/concordia-usecases/keep-security-reviews-current && cp -R docs/examples/payments/. /tmp/concordia-usecases/keep-security-reviews-current && cd /tmp/concordia-usecases/keep-security-reviews-current && sed -i.bak 's/payments-example/payments-uc-keep-security-reviews-current/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-security-reviews-current
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check the rule. The `sed` command keeps the assessment area, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia check --rule REV-APR-001 --no-color 2>/dev/null | sed -n '/^Assessment currency/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Assessment currency: violation (1 violation, 1 findings, 1 assessments, 1 required, 1 reviewRequired)
  ASM-LEDGER v1 (security): review-required (no approval)
    outcome satisfactory; no approval; assessments/ledger-threat-model.yaml:2
    changed: addition of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification; next action: get review

FAIL REV-APR-001 A required assessment has a current verified approval (advisory: does not change the exit code)
  Why it matters: An assessment that nobody with authority approved for the subject as it is now, or whose approval expired, was revoked, or was bound to an earlier subject, is not review evidence for the architecture it covers.
  required ledger-threat-model, assessment ASM-LEDGER: 1 finding(s)
    assessments/ledger-threat-model.yaml:2
  Fix: Get review of the assessment version and record a verified approval, or write a new assessment version for the changed subject. When currency is unknown, supply the missing evidence or fix the analysis configuration.
1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 0
```

The threat model says `outcome: satisfactory`, but nobody approved it, so the assessment is `review-required`. The rule fails, but it is `advisory` by default, so `check` exits with code 0.

`concordia assessment status` shows the same evaluation in more detail. `--at` fixes the evaluation time, so that the output is the same on any day:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia assessment status --at 2030-01-01 --no-color
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-LEDGER v1: review-required
  security, production, outcome satisfactory, review state ready-for-review, assessments/ledger-threat-model.yaml:2
  why: no approval
    no verified approval binds ASM-LEDGER@v1; a reviewed flag or a note in the document is not an approval
  changed: addition of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification
  next action: get review

Required assessments
  ledger-threat-model: ASM-LEDGER v1 is review-required (no approval)
```

Alice from security reviews the threat model. First she asks what exactly she approves. Concordia prints the subject with a digest of each item, and writes nothing until she confirms the subject digest:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia assessment approve ASM-LEDGER --reviewer alice@example.com --no-color
```

```output
Assessment ASM-LEDGER@v1 (security, environment production, outcome satisfactory)
Title: Ledger threat model
Version digest: sha256:84bcfd1e0b9726c58b713629dcbdb290c67986edb07d8bf928cd5454f4683d14
Digest contract: review-digest/1, capability profile: cbm-0.12.1
Dependencies (2):
  implementation file:services/ledger-service/src/api/index.ts sha256:d2cf74efc13b873087d8ed23aebbd3f23e79086d48e961272fba2a94efc40d0a
  trust-context calm:node:ledger-db#trust sha256:dfa04e62291492287ba56c388f2731e8f1e9864aab1e8fae3135356a32f6c49b
Assumptions: 0, evidence: 0
Subject digest: sha256:3cbb1c2620bea1bb97af54ca7f18527ca7a7a506cf90682b5c32eeac021ee43e
error CO-ASM-005: assessment approve or revoke refused: confirm the subject that you examined with --confirm-subject sha256:3cbb1c2620bea1bb97af54ca7f18527ca7a7a506cf90682b5c32eeac021ee43e
```

The command exits with code 2 and `CO-ASM-005`. It also refuses to run when the variable `CI` is set: a person records the decision, not a pipeline. Alice confirms the digest. The record digest contains the time of the decision, so the `grep` command drops that line:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia assessment approve ASM-LEDGER --reviewer alice@example.com --confirm-subject sha256:3cbb1c2620bea1bb97af54ca7f18527ca7a7a506cf90682b5c32eeac021ee43e --no-color | grep -v '^Record digest'
```

```output
Assessment ASM-LEDGER@v1 (security, environment production, outcome satisfactory)
Title: Ledger threat model
Version digest: sha256:84bcfd1e0b9726c58b713629dcbdb290c67986edb07d8bf928cd5454f4683d14
Digest contract: review-digest/1, capability profile: cbm-0.12.1
Dependencies (2):
  implementation file:services/ledger-service/src/api/index.ts sha256:d2cf74efc13b873087d8ed23aebbd3f23e79086d48e961272fba2a94efc40d0a
  trust-context calm:node:ledger-db#trust sha256:dfa04e62291492287ba56c388f2731e8f1e9864aab1e8fae3135356a32f6c49b
Assumptions: 0, evidence: 0
Subject digest: sha256:3cbb1c2620bea1bb97af54ca7f18527ca7a7a506cf90682b5c32eeac021ee43e
approval written: assessments/approvals/ASM-LEDGER-v1-1.yaml
Concordia counts this record only when it is on the trust anchor: merge it into the anchor branch (a change that touches only this file), then rebuild the candidate on the anchor.
```

In a real repository, the record goes to the protected branch in a small pull request. Here, commit it to `main` and index:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && git add -A && git commit -q -m "Approve ASM-LEDGER v1" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-security-reviews-current
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

The status shows the time of the decision. The `sed` command replaces it with a placeholder:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia assessment status --at 2030-01-01 --no-color | sed 's/decided [^;]*;/decided <time of the decision>;/'
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-LEDGER v1: current
  security, production, outcome satisfactory, review state approved, assessments/ledger-threat-model.yaml:2
  approval ASM-LEDGER-v1-1 by alice@example.com (security-review, protected-review), decided <time of the decision>; no expiry time

Required assessments
  ledger-threat-model: ASM-LEDGER v1 is current
```

The review is `current`. Now a developer changes the ledger API, the file that Alice reviewed:

```bash
cat > /tmp/concordia-usecases/keep-security-reviews-current/services/ledger-service/src/api/index.ts <<'EOF'
import { LedgerTable } from "../db/schema";

export function postEntry(paymentId: string, amountCents: number): number {
  if (amountCents <= 0) {
    throw new Error("a ledger entry needs a positive amount");
  }
  const table = new LedgerTable();
  return table.insert(paymentId, amountCents);
}
EOF
```

```output
```

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && git add -A && git commit -q -m "Reject ledger entries without a positive amount" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-security-reviews-current
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia assessment status --at 2030-01-01 --no-color | sed 's/decided [^;]*;/decided <time of the decision>;/'
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-LEDGER v1: review-required
  security, production, outcome satisfactory, review state review-required, assessments/ledger-threat-model.yaml:2
  approval ASM-LEDGER-v1-1 by alice@example.com (security-review, protected-review), decided <time of the decision>; no expiry time
  why: subject digest mismatch
    1 dependency item(s) differ from the subject that approval ASM-LEDGER-v1-1 binds: modified implementation file:services/ledger-service/src/api/index.ts
  changed: modification of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification
  next action: get review

Required assessments
  ledger-threat-model: ASM-LEDGER v1 is review-required (subject digest mismatch)
```

Alice's approval still exists and still verifies, but it no longer covers the code. The status names the changed file and the next action. Nobody had to remember to ask her.

## Make it block a merge

The rule has severity `error`, but the family `assessment-currency` is `advisory` by default. A violation shows in the reports, and `check` exits with code 0.

To block a merge, make the family `mandatory` under `check.enforcement`. Continue in the same copy:

```bash
cat >> /tmp/concordia-usecases/keep-security-reviews-current/concordia.yaml <<'EOF'
  enforcement:
    - family: assessment-currency
      mode: mandatory
EOF
```

```output
```

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && git commit -q -am "Make review currency mandatory" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-security-reviews-current
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && concordia check --rule REV-APR-001 --no-color 2>/dev/null | sed -n '/^FAIL/,/finding(s)/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
FAIL REV-APR-001 A required assessment has a current verified approval
  Why it matters: An assessment that nobody with authority approved for the subject as it is now, or whose approval expired, was revoked, or was bound to an earlier subject, is not review evidence for the architecture it covers.
  required ledger-threat-model, assessment ASM-LEDGER: 1 finding(s)
1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

Now the same finding makes `check` exit with code 1.

A `mandatory` review rule needs one authority and a trust anchor. Without them, nobody can give a verified approval, so the rule could never pass. Concordia refuses to run. Remove the trust anchor to see it, and then restore the file:

```bash
cd /tmp/concordia-usecases/keep-security-reviews-current && sed -i.bak '/trustAnchor/d' concordia.yaml && rm concordia.yaml.bak && concordia check --rule REV-APR-001 --no-color 2>&1; echo "exit code: $?"; git checkout -q concordia.yaml
```

```output
error CO-ASM-004: mandatory review rules need an authority and a trust anchor: rule REV-APR-001 is mandatory in production, but no trust anchor is configured under `assessments.trustAnchor`
exit code: 2
```

The run stops with exit code 2 and `CO-ASM-004`. A `mandatory` mode also needs the guarantees of the enforcement check (`CO-POL-018`). See [know what mandatory needs](../how-to/roll-out-new-obligations.md#know-what-mandatory-needs).

## Related

- [Review and approve assessments](../how-to/review-and-approve-assessments.md)
- [Gate on review currency](../how-to/run-in-ci.md#gate-on-review-currency)
- [Assessments reference](../reference/assessments.md)
- [Currency](../reference/assessments.md#currency)
- [Review rules](../reference/assessments.md#review-rules)
- [Roll out new obligations](../how-to/roll-out-new-obligations.md)
