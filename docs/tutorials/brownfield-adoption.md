# Adopting Concordia on an existing codebase

*2026-11-23T17:49:21Z by Showboat 0.6.1*
<!-- showboat-id: 2ae15dc8-ae34-4ee0-8d26-db56622cb0b9 -->

A bank cannot fix years of architecture debt before it turns on a new check. In this tutorial you adopt Concordia on the payments platform of a retail bank, which already breaks several of its rules. You accept the findings that exist today, and you fail the build only on new ones. This tutorial assumes that you finished [Getting started](getting-started.md).

First, you create a fresh repository from the payments example, with the CBM project name `payments-brownfield`. Run the commands from the root of a Concordia checkout.

```bash
rm -rf /tmp/concordia-docs/brownfield && mkdir -p /tmp/concordia-docs/brownfield && cp -R docs/examples/payments/. /tmp/concordia-docs/brownfield && cd /tmp/concordia-docs/brownfield && sed -i.bak 's/name: payments-example/name: payments-brownfield/' concordia.yaml && rm concordia.yaml.bak && ls
```

```output
architecture
assessments
bindings
concordia.yaml
deploy
docs
exceptions
governance
infra
jobs
policies
services
```

```bash
cd /tmp/concordia-docs/brownfield && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && git log --format=%s
```

```output
Initial payments example
```

Next, you index the code. As in [Getting started](getting-started.md), the output leaves out the generation ID, which changes with every commit.

```bash
cd /tmp/concordia-docs/brownfield && concordia index | grep -v '^generation'
```

```output
project: payments-brownfield
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

The index covers 14 of 15 files. The one excluded file is the Kotlin code of the legacy core banking system, which the workspace leaves out on purpose.

Now you run the check. The full report is long, so the command keeps only two lines: the overall verdict and the count of findings. `--quiet` hides baselined findings, which do not exist yet. The last command reads the exit code from `build/concordia/results.json`, the machine-readable results that every check writes.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet | grep -E '^(Conformance|[0-9]+ active)'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 36 findings)
33 active error(s), 4 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 5 unknown
exit code: 1
```

Notice the last summary line: the code has 33 active errors. The exit code is 1, so this check fails every build. This is the state of a typical brownfield codebase on the first day. To see where the errors are, you count the findings by disposition, severity, and rule:

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | "\(.disposition) \(.severity) \(.ruleId)"' build/concordia/results.json | sort | uniq -c
```

```output
      6 active error CORE-BND-001
      1 active error CORE-CYC-001
     10 active error CORE-DECL-001
      3 active error CORE-SEC-001
      3 active error PAY-API-001
      1 active error PAY-CYC-001
      3 active error PAY-DB-001
      1 active error PAY-NET-001
      1 active error PAY-NET-002
      2 active error PAY-PATH-001
      1 active error PAY-RBAC-001
      1 active error REV-APR-001
      1 active info ASR-PRV-001
      1 active info ASR-PRV-002
      1 active info CORE-DECL-001
      1 active info PAY-TLS-001
```

The 33 errors come from twelve rules, for example the import of fraud internals (`PAY-API-001`) and the open network path to the ledger database (`PAY-NET-002`). The four `info` findings report evidence that Concordia could not read. They never fail the build, because the workspace fails only on errors (`check.failOn: error`).

## Accept the existing findings

You record the errors as a baseline. A baseline lists the fingerprint of each accepted finding, with the time when you accepted it.

```bash
cd /tmp/concordia-docs/brownfield && concordia baseline create
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
baseline written: 33 finding(s) -> /tmp/concordia-docs/brownfield/exceptions/concordia.baseline.json
```

The baseline holds the 33 error findings. The warning line says that the CBM index has a kind of edge that Concordia does not use for rules. It does not change the result.

You keep a copy of the baseline outside the repository. Later, you use it to prove that new code never rewrites the baseline.

```bash
cd /tmp/concordia-docs/brownfield && cp exceptions/concordia.baseline.json /tmp/concordia-docs/brownfield-baseline-before.json
```

```output
```

You check again, with the same two summary lines.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet | grep -E '^(Conformance|[0-9]+ active)'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 36 findings)
0 active error(s), 4 active info(s), 33 baselined, 0 waived, 0 expired waiver(s), 5 unknown
exit code: 4
```

Now no error is active: all 33 are `baselined`. The exit code is still not 0, though. It is 4, which means that a rule that must not be `unknown` has the verdict `unknown`. You list the rules that Concordia could not judge, with their enforcement:

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.rules[] | select(.verdict=="unknown") | "\(.ruleId) \(.enforcement)"' build/concordia/results.json
```

```output
ASR-PRV-001 advisory
ASR-PRV-002 advisory
CORE-OBS-001 mandatory
PAY-LEG-001 mandatory
PAY-TLS-001 advisory
```

`PAY-LEG-001` and `CORE-OBS-001` are mandatory. Both need the code of the legacy core banking system, which is Kotlin and is not indexed. The advisory rules never change the exit code. A baseline accepts findings, but it never turns an unknown verdict into a pass. See [verdicts and coverage](../explanation/verdicts-and-coverage.md) for the reason.

## Make the gate pass on day one

The example fails on unknown verdicts on purpose: the workspace sets `check.unknown: fail`, and `PAY-LEG-001` sets `onUnknown: fail` in its own `evidence` block. The rule setting wins over the workspace setting. On the first day of adoption, the bank cannot index the legacy Kotlin code yet. So the architecture team decides to report unknown verdicts as warnings until it can. You change both settings to `warn`, and you look at the change:

```bash
cd /tmp/concordia-docs/brownfield && sed -i 's/^  unknown: fail$/  unknown: warn/' concordia.yaml && sed -i 's/onUnknown: fail/onUnknown: warn/' policies/payments.policy.yaml && git diff --stat && git diff -U0 | grep '^[-+] '
```

```output
 concordia.yaml                | 2 +-
 policies/payments.policy.yaml | 2 +-
 2 files changed, 2 insertions(+), 2 deletions(-)
-  unknown: fail
+  unknown: warn
-      onUnknown: fail
+      onUnknown: warn
```

This is a decision, so you commit it, as any other change to the workspace. Then you index again, because Concordia refuses to check against the graph of a different commit.

```bash
cd /tmp/concordia-docs/brownfield && git commit -q -am "Report unknown verdicts as warnings during adoption" && concordia index | grep -v '^generation'
```

```output
project: payments-brownfield
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet | grep -E '^(Conformance|[0-9]+ active)'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 36 findings)
0 active error(s), 4 active info(s), 33 baselined, 0 waived, 0 expired waiver(s), 5 unknown
exit code: 0
```

The exit code is 0. The five unknown verdicts are still in the report, but they no longer fail the build. Later, when the legacy code is indexed, you set both values back to `fail`.

## Catch a new violation

Now you simulate new work. A developer adds refunds to the payments service. The new class imports the internal risk model of the fraud service, as `PaymentService.ts` already does. You write the file, commit it, and index again.

```bash
cd /tmp/concordia-docs/brownfield
cat > services/payments-service/src/Refunds.ts <<'TS'
import { RiskModel } from "../../fraud-service/src/internal/RiskModel";

export class Refunds {
  approve(paymentId: string, amountCents: number): boolean {
    const model = new RiskModel();
    return model.score(paymentId, amountCents) < 0.5;
  }
}
TS

```

```output
```

```bash
cd /tmp/concordia-docs/brownfield && git add -A && git commit -q -m "Add refunds" && git log -1 --format=%s
```

```output
Add refunds
```

```bash
cd /tmp/concordia-docs/brownfield && concordia index | grep -v '^generation'
```

```output
project: payments-brownfield
files: 15/16 covered (93.8%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

You run the check. This time the command also prints the report block of `PAY-API-001`, the rule that keeps payments on the published fraud API.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet | sed -n '/^FAIL PAY-API-001/,/^$/p;/^Conformance/p;/active error/p'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 40 findings)
FAIL PAY-API-001 Payments uses only the published fraud API
  Why it matters: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
  Owner: team-payments
  payments-service -> fraud-service: 3 finding(s)
    services/payments-service/src/Refunds.ts    imports services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/Refunds.ts:5  calls RiskModel in services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/Refunds.ts:6  calls RiskModel.score in services/fraud-service/src/internal/RiskModel.ts
  Fix: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.
  3 baselined finding(s) not shown (use --verbose)

4 active error(s), 4 active info(s), 33 baselined, 0 waived, 0 expired waiver(s), 5 unknown
exit code: 1
```

Notice that `Refunds.ts` has three new `PAY-API-001` findings: one import and two calls. The three old findings in `PaymentService.ts` stay baselined. The summary now has 4 active errors, and the exit code is 1. Only new violations fail the build: this is the ratchet.

You find the fourth error in the results. The command lists the disposition, rule and file of every active error finding:

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | select(.disposition=="active" and .severity=="error") | "\(.disposition) \(.ruleId) \(.location.path)"' build/concordia/results.json | sort
```

```output
active CORE-BND-001 services/payments-service/src/Refunds.ts
active PAY-API-001 services/payments-service/src/Refunds.ts
active PAY-API-001 services/payments-service/src/Refunds.ts
active PAY-API-001 services/payments-service/src/Refunds.ts
```

The fourth error comes from `CORE-BND-001`, a rule of the built-in `core` pack. It forbids a service to import the code of another service. So the same new import breaks two rules.

You compare the baseline with the copy that you kept. The two files are identical: new code never rewrites the baseline.

```bash
cd /tmp/concordia-docs/brownfield && diff -s exceptions/concordia.baseline.json /tmp/concordia-docs/brownfield-baseline-before.json
```

```output
Files exceptions/concordia.baseline.json and /tmp/concordia-docs/brownfield-baseline-before.json are identical
```

## Waive the new findings until a date

Suppose that the refund work is part of a planned migration, and the payments team needs the import for one release. You waive the four new findings instead of baselining them. A waiver records an owner, a reason, and an expiry date, and it names one finding by its fingerprint. You list the fingerprints of the active errors:

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | select(.disposition=="active" and .severity=="error") | "\(.fingerprint) \(.ruleId)"' build/concordia/results.json
```

```output
sha256:c5bd2b0bfe8cb6abbf973f6830d7cc020f0547d22ca53c06bd63a3bd770fbf12 CORE-BND-001
sha256:5db432403167c6d1d583f69ca60e569eeeeda338186b09af5a8d61d8868c3f87 PAY-API-001
sha256:8f80cee53faab69ba74b2e520b4129995c5ca723fbe38e1fa3555e53afeabe16 PAY-API-001
sha256:9ca10bfa401c4422f833b4f7109ee241f98e25bd72ab9b8fc2809ab519e6b449 PAY-API-001
```

You add one waiver for each fingerprint. The loop reads the fingerprints with the same filter, so you do not copy them by hand. In a real project, the expiry is the planned end of the migration. Here you pick a date far in the future, so that this page works on any day. The example ships an empty `exceptions/waivers.yaml`, and each command adds a waiver to it.

```bash
cd /tmp/concordia-docs/brownfield
for fingerprint in $(jq -r '.findings[] | select(.disposition=="active" and .severity=="error") | .fingerprint' build/concordia/results.json); do
  concordia waiver add --finding "$fingerprint" --owner team-payments --reason "Refund migration in progress" --expires 2099-12-31
done

```

```output
waiver WV-1 added to exceptions/waivers.yaml
waiver WV-2 added to exceptions/waivers.yaml
waiver WV-3 added to exceptions/waivers.yaml
waiver WV-4 added to exceptions/waivers.yaml
```

```bash
cd /tmp/concordia-docs/brownfield && sed -n '1,7p' exceptions/waivers.yaml
```

```output
waivers:
  - id: WV-1
    finding: sha256:c5bd2b0bfe8cb6abbf973f6830d7cc020f0547d22ca53c06bd63a3bd770fbf12
    rule: CORE-BND-001
    owner: team-payments
    reason: Refund migration in progress
    expires: 2099-12-31
```

Each entry has the rule, the owner, the reason, and the expiry. `waiver add` reads the rule from the results. The command shows only the first entry; the other three have the same form. You commit the waivers, and you check again.

```bash
cd /tmp/concordia-docs/brownfield && git add -A && git commit -q -m "Waive the refund findings until the migration ends" && concordia index | grep -v '^generation'
```

```output
project: payments-brownfield
files: 15/16 covered (93.8%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet | grep -E '^(Conformance|[0-9]+ active)'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 40 findings)
0 active error(s), 4 active info(s), 33 baselined, 4 waived, 0 expired waiver(s), 5 unknown
exit code: 0
```

The four new findings are `waived`, and the exit code is 0 again. The waiver covers only the four findings that you named. Another new violation still fails the build.

## See a waiver expire

A waiver is in force until the end of its `expires` day, in UTC. After that day, `check` reports the finding as `expired-waiver`, and the finding fails the build again. To see this today, you set the clock of Concordia with the `CONCORDIA_NOW` environment variable to the day after the expiry:

```bash
cd /tmp/concordia-docs/brownfield && CONCORDIA_NOW=2100-01-01T00:00:00Z concordia check --no-color --quiet | grep -E '^(Conformance|[0-9]+ active)'; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (11 violation, 5 unknown, 1 pass, 4 notApplicable, 40 findings)
0 active error(s), 4 active info(s), 33 baselined, 0 waived, 4 expired waiver(s), 5 unknown
exit code: 1
```

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | select(.location.path=="services/payments-service/src/Refunds.ts") | "\(.disposition) \(.ruleId)"' build/concordia/results.json | sort
```

```output
expired-waiver CORE-BND-001
expired-waiver PAY-API-001
expired-waiver PAY-API-001
expired-waiver PAY-API-001
```

The four findings are now `expired-waiver`, and the exit code is 1. The migration must end, or a person must extend the waiver on purpose. To get a warning before that day, run `concordia waiver validate --fail-expiring-within <days>` in CI.

Finally, you remove the local state and the CBM project.

```bash
cd /tmp/concordia-docs/brownfield && concordia clean --all --yes
```

```output
removed: .concordia/, build/concordia/, CBM project 'payments-brownfield'
```

You adopted Concordia on a payments platform with existing violations. You baselined the findings that exist, you made the gate pass on day one, you let the ratchet catch a new import of fraud internals, and you waived the new findings until a fixed date. Next steps:

- [Manage baselines and waivers](../how-to/manage-baselines-and-waivers.md) covers `baseline update`, pruning, and expiry warnings.
- [Fingerprints, baselines and waivers](../explanation/fingerprints-baselines-waivers.md) explains why findings survive edits, and why waivers expire.
- [Integrate through published APIs](../use-cases/integrate-through-published-apis.md) explains the `PAY-API-001` rule that caught the new import.
