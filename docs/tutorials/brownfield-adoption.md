# Adopting Concordia on an existing codebase

*2026-10-21T01:26:38Z by Showboat 0.6.1*
<!-- showboat-id: f2bc5b18-98cb-47cf-8248-99680971474c -->

We will adopt Concordia on a codebase that already has violations. The goal is to accept today's violations, and to fail the build only on new ones. This tutorial assumes you finished [Getting started](getting-started.md).

First, we create a fresh repository from the sample project, named `commerce-brownfield`, and index it. Run the commands from the root of a Concordia checkout. As in [Getting started](getting-started.md), the index output leaves out the generation ID, which changes with every commit.

```bash
rm -rf /tmp/concordia-docs/brownfield && mkdir -p /tmp/concordia-docs/brownfield && cp -R docs/examples/commerce/. /tmp/concordia-docs/brownfield && cd /tmp/concordia-docs/brownfield && sed -i.bak 's/name: commerce-example/name: commerce-brownfield/' concordia.yaml && rm concordia.yaml.bak && ls
```

```output
architecture
bindings
concordia.yaml
exceptions
packages
policies
services
```

```bash
cd /tmp/concordia-docs/brownfield && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial commerce example" && git log --format=%s
```

```output
Initial commerce example
```

```bash
cd /tmp/concordia-docs/brownfield && concordia index | grep -v '^generation'
```

```output
project: commerce-brownfield
files: 6/7 covered (85.7%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Now we run the check. Notice the summary line: the code has eight active findings, and `CO-COV-001` is `unknown`. This is the state of a typical brownfield codebase on the first day. We use `--quiet` to hide baselined findings, which do not exist yet, and to keep later outputs short.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (4 violation, 1 unknown, 8 findings)

FAIL CO-BND-001 Billing uses Fulfillment contracts only
  Why it matters: When fulfillment refactors its internals, invoicing breaks. The fulfillment contract is the part that fulfillment keeps stable for billing.
  Owner: team-billing
  billing-service -> fulfillment-service: 3 finding(s)
    services/billing/src/InvoiceService.ts    imports services/fulfillment/src/internal/ShipmentStore.ts
    services/billing/src/InvoiceService.ts:7  calls ShipmentStore.reserve in services/fulfillment/src/internal/ShipmentStore.ts
    services/billing/src/InvoiceService.ts:6  calls ShipmentStore in services/fulfillment/src/internal/ShipmentStore.ts
  Fix: Import from packages/fulfillment-contract instead.

UNKNOWN CO-COV-001 Legacy must not call Billing
  Why it matters: The legacy monolith is being retired. If it still calls billing, switching it off breaks invoicing.
  Cannot judge: minimum coverage not met: 0.500 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 50.0% (required 90.0%)

FAIL CO-CYC-001 Services are acyclic
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other.
  Owner: team-billing
  Coverage: 85.7% (required 90.0%)
  cycle through billing-service, fulfillment-service: 1 finding(s)
    services/billing/src/InvoiceService.ts:6  calls ShipmentStore in services/fulfillment/src/internal/ShipmentStore.ts
                                              then notifyShipped calls InvoiceService in services/billing/src/InvoiceService.ts
  Fix: Break the cycle. For example, let fulfillment publish a shipped event that billing consumes, instead of calling billing.

FAIL CO-DECL-001 Dependencies are declared
  Why it matters: Reviews and threat models read the architecture model. A dependency that the model does not show is a data flow that nobody reviewed.
  Owner: team-fulfillment
  Coverage: 85.7% (required 90.0%)
  fulfillment-service -> billing-service: 3 finding(s)
    services/fulfillment/src/notify.ts    imports services/billing/src/InvoiceService.ts
    services/fulfillment/src/notify.ts:4  calls InvoiceService in services/billing/src/InvoiceService.ts
    services/fulfillment/src/notify.ts:5  calls InvoiceService.issue in services/billing/src/InvoiceService.ts
  Fix: Add the relationship to the CALM model if it is intended, or remove the dependency.

FAIL CO-LYR-001 Billing sits above Fulfillment
  Why it matters: Fulfillment is the lower layer. When it calls up into billing, a billing release can stop orders from shipping.
  Owner: team-fulfillment
  fulfillment-service -> billing-service: 1 finding(s)
    services/fulfillment/src/notify.ts  imports services/billing/src/InvoiceService.ts
  Fix: Notify billing through the fulfillment contract (an event or a callback) instead of importing billing code.

Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation
No environment declared: the deployment trace and environment-specific assurance need a declared environment.

8 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 1
```

We record all eight findings as a baseline. A baseline lists the fingerprint of each accepted finding, with the time it was accepted.

```bash
cd /tmp/concordia-docs/brownfield && concordia baseline create
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
baseline written: 8 finding(s) -> /tmp/concordia-docs/brownfield/exceptions/concordia.baseline.json
```

We keep a copy of the file outside the repository, so that we can prove later that new code never rewrites it.

```bash
cd /tmp/concordia-docs/brownfield && cp exceptions/concordia.baseline.json /tmp/concordia-docs/brownfield-baseline-before.json
```

```output
```

We check again. Notice that the eight findings are now `baselined`, and no finding is active. The exit code is still not 0, though. It is 4, because the verdict of `CO-COV-001` is `unknown`: the rule watches excluded legacy code, and Concordia fails on unknown verdicts by default. A baseline accepts findings. It never turns an unknown verdict into a pass. See [verdicts and coverage](../explanation/verdicts-and-coverage.md) for the reason.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (4 violation, 1 unknown, 8 findings)

FAIL CO-BND-001 Billing uses Fulfillment contracts only
  Why it matters: When fulfillment refactors its internals, invoicing breaks. The fulfillment contract is the part that fulfillment keeps stable for billing.
  Owner: team-billing
  Fix: Import from packages/fulfillment-contract instead.
  3 baselined finding(s) not shown (use --verbose)

UNKNOWN CO-COV-001 Legacy must not call Billing
  Why it matters: The legacy monolith is being retired. If it still calls billing, switching it off breaks invoicing.
  Cannot judge: minimum coverage not met: 0.500 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 50.0% (required 90.0%)

FAIL CO-CYC-001 Services are acyclic
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other.
  Coverage: 85.7% (required 90.0%)
  Fix: Break the cycle. For example, let fulfillment publish a shipped event that billing consumes, instead of calling billing.
  1 baselined finding(s) not shown (use --verbose)

FAIL CO-DECL-001 Dependencies are declared
  Why it matters: Reviews and threat models read the architecture model. A dependency that the model does not show is a data flow that nobody reviewed.
  Coverage: 85.7% (required 90.0%)
  Fix: Add the relationship to the CALM model if it is intended, or remove the dependency.
  3 baselined finding(s) not shown (use --verbose)

FAIL CO-LYR-001 Billing sits above Fulfillment
  Why it matters: Fulfillment is the lower layer. When it calls up into billing, a billing release can stop orders from shipping.
  Fix: Notify billing through the fulfillment contract (an event or a callback) instead of importing billing code.
  1 baselined finding(s) not shown (use --verbose)

Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation
No environment declared: the deployment trace and environment-specific assurance need a declared environment.

0 active error(s), 8 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 4
```

Now we simulate new work. We add a `Refund` class to the billing service. It imports the internals of the fulfillment service, which the policy forbids. We write the file, commit it, and index again, because Concordia refuses to check against a graph of a different commit.

```bash
cd /tmp/concordia-docs/brownfield
cat > services/billing/src/Refund.ts <<'TS'
import { ShipmentStore } from "../../fulfillment/src/internal/ShipmentStore";

export class Refund {
  refund(orderId: string): void {
    const store = new ShipmentStore();
    store.reserve(orderId);
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
project: commerce-brownfield
files: 7/8 covered (87.5%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

We run the check. Notice that the three findings in `Refund.ts` are `active`, and the exit code is 1. The eight old findings stay baselined. Only new violations fail the build: this is the ratchet.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --no-color --quiet; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
Conformance: violation (4 violation, 1 unknown, 11 findings)

FAIL CO-BND-001 Billing uses Fulfillment contracts only
  Why it matters: When fulfillment refactors its internals, invoicing breaks. The fulfillment contract is the part that fulfillment keeps stable for billing.
  Owner: team-billing
  billing-service -> fulfillment-service: 3 finding(s)
    services/billing/src/Refund.ts:5  calls ShipmentStore in services/fulfillment/src/internal/ShipmentStore.ts
    services/billing/src/Refund.ts    imports services/fulfillment/src/internal/ShipmentStore.ts
    services/billing/src/Refund.ts:6  calls ShipmentStore.reserve in services/fulfillment/src/internal/ShipmentStore.ts
  Fix: Import from packages/fulfillment-contract instead.
  3 baselined finding(s) not shown (use --verbose)

UNKNOWN CO-COV-001 Legacy must not call Billing
  Why it matters: The legacy monolith is being retired. If it still calls billing, switching it off breaks invoicing.
  Cannot judge: minimum coverage not met: 0.667 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 66.7% (required 90.0%)

FAIL CO-CYC-001 Services are acyclic
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other.
  Coverage: 87.5% (required 90.0%)
  Fix: Break the cycle. For example, let fulfillment publish a shipped event that billing consumes, instead of calling billing.
  1 baselined finding(s) not shown (use --verbose)

FAIL CO-DECL-001 Dependencies are declared
  Why it matters: Reviews and threat models read the architecture model. A dependency that the model does not show is a data flow that nobody reviewed.
  Coverage: 87.5% (required 90.0%)
  Fix: Add the relationship to the CALM model if it is intended, or remove the dependency.
  3 baselined finding(s) not shown (use --verbose)

FAIL CO-LYR-001 Billing sits above Fulfillment
  Why it matters: Fulfillment is the lower layer. When it calls up into billing, a billing release can stop orders from shipping.
  Fix: Notify billing through the fulfillment contract (an event or a callback) instead of importing billing code.
  1 baselined finding(s) not shown (use --verbose)

Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation
No environment declared: the deployment trace and environment-specific assurance need a declared environment.

3 active error(s), 8 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 1
```

We confirm this in the machine-readable results. `check` writes `build/concordia/results.json`. We list the disposition, rule and file of every finding, then compare the baseline with the copy that we kept. Notice that the two files are identical: adding code never rewrites the baseline.

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | "\(.disposition) \(.ruleId) \(.location.path)"' build/concordia/results.json | sort
```

```output
active CO-BND-001 services/billing/src/Refund.ts
active CO-BND-001 services/billing/src/Refund.ts
active CO-BND-001 services/billing/src/Refund.ts
baselined CO-BND-001 services/billing/src/InvoiceService.ts
baselined CO-BND-001 services/billing/src/InvoiceService.ts
baselined CO-BND-001 services/billing/src/InvoiceService.ts
baselined CO-CYC-001 services/billing/src/InvoiceService.ts
baselined CO-DECL-001 services/fulfillment/src/notify.ts
baselined CO-DECL-001 services/fulfillment/src/notify.ts
baselined CO-DECL-001 services/fulfillment/src/notify.ts
baselined CO-LYR-001 services/fulfillment/src/notify.ts
```

```bash
cd /tmp/concordia-docs/brownfield && diff -s exceptions/concordia.baseline.json /tmp/concordia-docs/brownfield-baseline-before.json
```

```output
Files exceptions/concordia.baseline.json and /tmp/concordia-docs/brownfield-baseline-before.json are identical
```

Suppose the refund work is a planned migration. We waive the new import finding instead of baselining it. A waiver names one finding by its fingerprint, so first we read that fingerprint from the results:

```bash
cd /tmp/concordia-docs/brownfield && jq -r '.findings[] | select(.disposition=="active" and .edge.kind=="imports") | .fingerprint' build/concordia/results.json
```

```output
sha256:a26922d29928409536231c074ca000638bd358c73f2111f68f7e2a36d4a326c6
```

We copy that fingerprint into `waiver add`. A waiver records an owner, a reason and an expiry date. In a real project the expiry is the planned end of the migration; we pick a date far in the future so that this page works on any day.

```bash
cd /tmp/concordia-docs/brownfield && concordia waiver add --finding sha256:a26922d29928409536231c074ca000638bd358c73f2111f68f7e2a36d4a326c6 --owner team-billing --reason "Refund migration in progress" --expires 2099-12-31
```

```output
waiver WV-1 added to exceptions/waivers.yaml
```

```bash
cd /tmp/concordia-docs/brownfield && cat exceptions/waivers.yaml
```

```output
waivers:
  - id: WV-1
    finding: sha256:a26922d29928409536231c074ca000638bd358c73f2111f68f7e2a36d4a326c6
    rule: CO-BND-001
    owner: team-billing
    reason: Refund migration in progress
    expires: 2099-12-31
```

Now we check again. Notice that the import finding is `waived`, and the two call findings are still `active`: the waiver covers only the finding that we named.

```bash
cd /tmp/concordia-docs/brownfield && concordia check --format json | jq -r '.findings[] | select(.location.path=="services/billing/src/Refund.ts") | "\(.disposition) \(.edge.kind)"'
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
active calls
waived imports
active calls
```

A waiver is in force until the end of its `expires` day, in UTC. After that day, `check` reports the finding as `expired-waiver` and it fails the build again. `waiver validate` checks the waiver file itself, and `--at` evaluates it at another time. The day after the expiry, it reports the expired waiver as an error (`CO-EXC-006`) and exits with code 2:

```bash
cd /tmp/concordia-docs/brownfield && concordia waiver validate --at 2100-01-01
```

```output
error CO-EXC-006: waiver expired: WV-1 (expired 2099-12-31) [exceptions/waivers.yaml]
1 waiver(s) checked
```

We clean up the local state and the CBM project.

```bash
cd /tmp/concordia-docs/brownfield && concordia clean --all --yes
```

```output
removed: .concordia/, build/concordia/, CBM project 'commerce-brownfield'
```

You have adopted Concordia on a codebase with existing violations: you baselined what exists, you let the ratchet catch new violations, and you waived one finding with an expiry date. Next steps:

- [Manage baselines and waivers](../how-to/manage-baselines-and-waivers.md) covers `baseline update`, pruning, and expiry warnings.
- [Fingerprints, baselines and waivers](../explanation/fingerprints-baselines-waivers.md) explains why findings survive edits, and why waivers expire.
