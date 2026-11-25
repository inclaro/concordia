# A tour of Concordia

*2026-11-23T17:46:43Z by Showboat 0.6.1*
<!-- showboat-id: 58cc8ee9-5dca-47f6-8abe-a6f36749ec5a -->

A retail bank runs its payments on a set of services. The payments service authorizes each card payment, and it asks the fraud service for a risk score first. Last quarter, the fraud team refactored its risk model, and payment authorization stopped in production. Every pull request had a review. Nobody saw that the payments service used a class inside the fraud service.

In this tutorial, you run Concordia on a copy of the bank's code and ask the questions that the incident raised. Would Concordia have caught it? What else is waiting to break? Can the internet reach the ledger database? Is the decision about the ledger enforced? And when the ledger code changes, does the threat model still hold?

You need `concordia`, Codebase Memory 0.12.1 (`codebase-memory-mcp`), `git`, and `jq` on your `PATH`. Run every command from the root of a Concordia checkout. The tour takes about ten minutes.

```bash
concordia --version
```

```output
concordia dev
calm-schema 1.2,1.3
cbm-adapter concordia-cbm/v2
cbm 0.12.1
```

## 1. Copy the bank's code

The example at `docs/examples/payments` holds the bank's code, its architecture as a CALM model, the bindings that map each part of the model to code and infrastructure, the Kubernetes manifests, a Terraform plan, and the bank's architecture rules. You copy it to a scratch directory.

```bash
rm -rf /tmp/concordia-docs/tour && mkdir -p /tmp/concordia-docs/tour && cp -R docs/examples/payments/. /tmp/concordia-docs/tour && ls /tmp/concordia-docs/tour
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

The workspace file, `concordia.yaml`, names the Codebase Memory (CBM) project that holds the code graph. You give the copy its own project name, so that it does not touch any other copy. The `observation` section also shows which code CBM indexes. The legacy core banking system is Kotlin, and this index does not read it.

```bash
cd /tmp/concordia-docs/tour && sed -i 's/payments-example/payments-tour/' concordia.yaml && sed -n '/^observation:/,/^environments:/p' concordia.yaml
```

```output
observation:
  provider: codebase-memory-mcp
  projects:
    - name: payments-tour
      root: .
      include: [services/**, jobs/**]
      exclude: [services/legacy-core-banking/**]
  indexMode: full
environments:
```

Concordia reads committed code, so you make the copy a Git repository. The branch is `main`, because the bank's reviews count only when they are on `main`. Section 6 uses this.

```bash
cd /tmp/concordia-docs/tour && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Payments platform" && git log --format=%s
```

```output
Payments platform
```

`concordia index` builds the code graph. Its output also has a generation ID that changes with every commit. You drop that line with `grep -v`, so that this page stays the same on every run.

```bash
cd /tmp/concordia-docs/tour && concordia index | grep -v '^generation'
```

```output
project: payments-tour
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

CBM covers 14 of the 15 files. The one excluded file is the Kotlin service.

## 2. Would Concordia have caught the outage?

The bank already has a rule for this, `PAY-API-001`: the payments service may use only the published API of the fraud service, the files under `services/fraud-service/src/api`. You run that one rule. Every `check` also reports the other areas of the workspace (the deployment trace and the assessments). You read those later, so `sed` stops the output where they start.

```bash
cd /tmp/concordia-docs/tour && concordia check --rule PAY-API-001 --no-color 2>&1 | sed -n '/^Deployment trace/q;p'
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (1 violation, 3 findings)

FAIL PAY-API-001 Payments uses only the published fraud API
  Why it matters: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
  Owner: team-payments
  payments-service -> fraud-service: 3 finding(s)
    services/payments-service/src/PaymentService.ts    imports services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:6  calls RiskModel in services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:7  calls RiskModel.score in services/fraud-service/src/internal/RiskModel.ts
  Fix: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.

```

Yes, Concordia catches it. `PaymentService.ts` imports `RiskModel`, a class in the `internal` folder of the fraud service, and calls it on lines 6 and 7. A refactor of that class stopped payment authorization, exactly as the rule says. Look at the code:

```bash
cd /tmp/concordia-docs/tour && sed -n '1,9p' services/payments-service/src/PaymentService.ts
```

```output
import { RiskModel } from "../../fraud-service/src/internal/RiskModel";
import { postEntry } from "../../ledger-service/src/api/index";

export class PaymentService {
  async authorize(paymentId: string, amountCents: number): Promise<boolean> {
    const model = new RiskModel();
    if (model.score(paymentId, amountCents) > 0.8) {
      return false;
    }
```

The `warning CO-OBS-016` line is harmless. CBM reported a Git branch edge that no rule uses.

`build/concordia/results.json` holds the same result for CI. The exit code is 1, so a pull request that added the import fails:

```bash
cd /tmp/concordia-docs/tour && jq .summary.exitCode build/concordia/results.json
```

```output
1
```

[Integrate through published APIs](../use-cases/integrate-through-published-apis.md) explains this rule in detail.

## 3. What else is waiting to break?

The same kind of mistake can hide in other places. Three more rules of the bank look for the next outage: a critical service must not depend on a non-critical one (`PAY-PATH-001`), every dependency in the code must be in the model (`CORE-DECL-001`), and services must be free of cycles (`PAY-CYC-001`). `check` accepts more than one `--rule`.

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color --rule PAY-PATH-001 --rule CORE-DECL-001 --rule PAY-CYC-001 | sed -n '/^Deployment trace/q;p'
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (3 violation, 14 findings)

FAIL CORE-DECL-001 Dependencies between services, databases, and web clients are declared
  Why it matters: An architecture model is only a source of truth when it shows every dependency that exists in the code. A hidden dependency is a coupling that nobody decided on.
  channels-api -> customer-profile: 3 finding(s)
    services/channels-api/src/accounts.ts     imports services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:9   calls ProfileStore in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:10  calls ProfileStore.load in services/customer-profile/src/internal/ProfileStore.ts
  fraud-service -> notifications-service: 2 finding(s)
    services/fraud-service/src/alerts.ts    imports services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts:4  calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
  ledger-service -> payments-service: 2 finding(s)
    services/ledger-service/src/settlement.ts    imports services/payments-service/src/api/index.ts
    services/ledger-service/src/settlement.ts:4  calls services.payments-service.src.api.paymentStatus in services/payments-service/src/api/index.ts
  payments-service: 1 finding(s)
    services/payments-service/src/PaymentService.ts:5  http_calls __route__ANY__http://card-network.example.com/authorize
  reporting-job -> ledger-db: 3 finding(s)
    jobs/reporting/src/nightly.ts    imports services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:4  calls LedgerTable in services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:5  calls LedgerTable.totals in services/ledger-service/src/db/schema.ts
  Fix: Remove the dependency from the code, or declare it as a relationship in the architecture model. Edges of kind HTTP_CALLS and ASYNC_CALLS are heuristic in the CBM adapter, so lower evidence.minimumConfidence to heuristic with a rule override to check them.

FAIL PAY-CYC-001 Services are free of cycles
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other. A cycle between payments and the ledger means that a ledger incident can stop payments.
  Owner: team-architecture
  cycle through ledger-service, payments-service: 1 finding(s)
    services/ledger-service/src/settlement.ts:4  calls services.payments-service.src.api.paymentStatus in services/payments-service/src/api/index.ts
                                                 then PaymentService.authorize calls services.ledger-service.src.api.postEntry in services/ledger-service/src/api/index.ts
  Fix: Break the cycle. For example, let payments publish a settled event that the ledger consumes, instead of the ledger calling payments.

FAIL PAY-PATH-001 Critical services do not depend on non-critical services
  Why it matters: Payment authorization must keep working when a non-critical service such as notifications is down. A path from a critical service to a non-critical one, direct or through other code, lets a minor outage stop payments.
  Owner: team-architecture
  fraud-service -> notifications-service: 2 finding(s)
    services/fraud-service/src/alerts.ts    imports services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts:4  calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
  Fix: Remove the dependency, or decouple it with a queue so that the critical service does not wait for the non-critical one.

```

Each finding names the file and the line. Read them from the bottom:

- `PAY-PATH-001`: the fraud service is `critical`, and `alerts.ts` calls the notifications service, which is `non-critical`. When notifications is down, fraud scoring waits, and payments stop again.
- `PAY-CYC-001`: the ledger calls back into payments in `settlement.ts`, and payments calls the ledger. A ledger incident can then stop payments, and neither service can roll back on its own.
- `CORE-DECL-001`: five dependencies are in the code but not in the model. A threat model that reads the model does not see them. The `reporting-job -> ledger-db` finding is the same access that `PAY-DB-001` reports in section 5. The `http_calls` finding is a call to a URL that Concordia cannot match to a node of the model.

See [Keep critical paths isolated](../use-cases/keep-critical-paths-isolated.md), [Keep the model in sync with code](../use-cases/keep-the-model-in-sync-with-code.md), and [Keep services free of cycles](../use-cases/keep-services-free-of-cycles.md).

## 4. Can the internet reach the ledger database?

After the outage, the security team asks a second question. The ledger database holds the bank's record of every movement of money. Can anything on the internet side reach it? Rule `PAY-NET-002` says that the public load balancer must have no network path to the ledger database on port 5432.

Concordia does not answer this from the model. The bindings map the load balancer to `aws_lb.public` and the database to `aws_db_instance.ledger` in a Terraform plan, the output of `terraform show -json`. The plan compares the Terraform code with the estate that is already deployed. Concordia reads the security groups and their rules from that plan.

```bash
cd /tmp/concordia-docs/tour && concordia check --rule PAY-NET-002 --no-color 2>&1 | sed -n '/^Deployment trace/q;p'
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (1 violation, 1 findings)

FAIL PAY-NET-002 The public load balancer has no network path to the ledger database (advisory: does not change the exit code)
  Why it matters: The ledger database must be reachable only from the application tier. An open path from the internet-facing load balancer puts the bank's ledger one misconfiguration away from the internet.
  Owner: team-platform
  public-load-balancer -> ledger-db: 1 finding(s)
    infra/plans/production.json
      open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_security_group.gateway egress[0], aws_vpc_security_group_egress_rule.gateway_out rule, aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)
  Fix: Remove the security group rule that admits the load balancer group to the ledger database.

```

The path is open. The finding names the plan file, `infra/plans/production.json`, and the rules that open the path. On the load balancer side, the egress rule `gateway_out` allows all traffic out. It shows twice, because the plan lists the deployed rule both as its own resource and in the `egress` list of the `gateway` security group. On the database side, the ingress rule `ledger_from_gateway` admits the load balancer's security group on 5432.

The plan also shows which change opens the path. Everything else in the estate is already deployed (`no-op`). Only the debug rule is new:

```bash
cd /tmp/concordia-docs/tour && jq -r '.resource_changes[] | "\(.change.actions | join(",")) \(.address)"' infra/plans/production.json
```

```output
no-op aws_db_instance.ledger
no-op aws_lb.public
no-op aws_security_group.gateway
no-op aws_security_group.ledger_db
no-op aws_subnet.public
no-op aws_vpc.main
no-op aws_vpc_security_group_egress_rule.gateway_out
no-op aws_vpc_security_group_ingress_rule.ledger_from_app
create aws_vpc_security_group_ingress_rule.ledger_from_gateway
```

This is the new rule in the Terraform code:

```bash
cd /tmp/concordia-docs/tour && grep -n -A7 '"ledger_from_gateway"' infra/terraform/main.tf
```

```output
72:resource "aws_vpc_security_group_ingress_rule" "ledger_from_gateway" {
73-  security_group_id            = aws_security_group.ledger_db.id
74-  description                  = "temporary debug access"
75-  referenced_security_group_id = aws_security_group.gateway.id
76-  ip_protocol                  = "tcp"
77-  from_port                    = 5432
78-  to_port                      = 5432
79-}
```

Somebody added "temporary debug access". No code review of the services could see it. Concordia reports it from the plan, before Terraform applies it.

The rule is `advisory`: it reports, but it does not change the exit code. The contract of the effective-access rules is not yet stable, so they cannot be `mandatory` in this release. The exit code of this run is 0:

```bash
cd /tmp/concordia-docs/tour && jq .summary.exitCode build/concordia/results.json
```

```output
0
```

`concordia explain` shows the rule and the finding again, with the evidence location. The middle of its output is the compiled plan of the rule, which `sed` leaves out here.

```bash
cd /tmp/concordia-docs/tour && concordia explain PAY-NET-002 --no-color 2>&1 | sed -n '1,7p;/^Finding/,$p'
```

```output
PAY-NET-002 The public load balancer has no network path to the ledger database
Verdict: violation   Severity: error   Family: effective-access   Policy: payments-architecture@1.0.0
Rationale: The ledger database must be reachable only from the application tier. An open path from the internet-facing load balancer puts the bank's ledger one misconfiguration away from the internet.
Remediation: Remove the security group rule that admits the load balancer group to the ledger database.
Rule owner: team-platform
Subjects: calm:node:public-load-balancer
Targets: calm:node:ledger-db
Finding 0375…de4
  Fingerprint: sha256:03752564dacfb15d175792e9bb0bc88d8766a97460efc2171a5df5c31b1f8de4
  Kind: obligation-violation
  Message: effective network access from public-load-balancer to ledger-db (environment production): open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_security_group.gateway egress[0], aws_vpc_security_group_egress_rule.gateway_out rule, aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)
  Source: calm:node:public-load-balancer
  Target: calm:node:ledger-db
  Owner: team-platform
  Disposition: active
  Location: infra/plans/production.json
```

`results.json` also records how Concordia decided. The evaluation entry keeps the query, the rules that decided it, the SHA-256 digest of the plan that Concordia read, and the integrity of that plan. Nobody signed this plan, so its integrity is `content not verified`.

```bash
cd /tmp/concordia-docs/tour && jq '.assurance.evaluations[] | select(.kind == "cloud-firewall") | {query, verdict, policies, inputDigest, integrity}' build/concordia/results.json
```

```output
{
  "query": "tcp/5432 from aws_lb.public to aws_db_instance.ledger",
  "verdict": "allowed",
  "policies": [
    "aws_security_group.gateway egress[0]",
    "aws_vpc_security_group_egress_rule.gateway_out rule",
    "aws_vpc_security_group_ingress_rule.ledger_from_gateway rule"
  ],
  "inputDigest": "sha256:f39e6f816f9c0ce49e79dc6e909338b23cb34ce331a371c8145fb2d2d6539e0e",
  "integrity": "content not verified"
}
```

`verdict: allowed` is the answer of the evaluator: the traffic can flow. The rule expects `denied`, so the rule fails.

The plan is a statement of what Terraform would apply. Concordia trusts that the plan came from this revision, and that no other firewall rule for these resources exists outside the plan. The workspace declares that second assumption as `firewall-rules-complete-in-plan`. [Block network paths in the cloud](../use-cases/block-network-paths-in-the-cloud.md) explains each step and each limit.

## 5. Record the decision, and prove it is enforced

The bank decided that only the ledger service reads and writes the ledger database. An ADR records the decision, but a Markdown file cannot show that anything enforces it. A decision register links the ADR to the rule that does, `PAY-DB-001`.

```bash
cd /tmp/concordia-docs/tour && cat governance/decisions.yaml
```

```output
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

`enforcement: required` says that a rule must check this decision on every run. `concordia adr validate` checks the register and the rule link:

```bash
cd /tmp/concordia-docs/tour && concordia adr validate
```

```output
adr: 1 decision(s) in 1 register file(s) valid; 1 rule link(s) checked; active statuses: accepted
```

Rule `GOV-ADR-004` checks that a decision that requires enforcement links a mandatory rule. You run it and read the ADR governance area of the output. The area lists the decision as `ADR-1`.

```bash
cd /tmp/concordia-docs/tour && concordia check --rule GOV-ADR-004 --no-color 2>&1 | sed -n '/^ADR governance/,/^$/p'
```

```output
ADR governance: pass (1 pass)
  ADR-1 accepted (docs/adr/0001-ledger-database-owned-by-ledger-service.md:1)
    presence committed; applicability active; enforcement enforced (required; rules: PAY-DB-001@payments-architecture/1.0.0)

```

The decision is committed, active (`accepted`), and enforced by `PAY-DB-001`. Now prove that the check means something. You remove the rule link from the register in the working tree, and run the rule again:

```bash
cd /tmp/concordia-docs/tour && sed -i '/enforcedBy:/,$d' governance/decisions.yaml && concordia check --rule GOV-ADR-004 --no-color 2>&1 | sed -n '/^ADR governance/,/^Assessment currency/p' | sed '$d'
```

```output
ADR governance: violation (1 violation, 1 findings)
  ADR-1 accepted (docs/adr/0001-ledger-database-owned-by-ledger-service.md:1)
    presence committed; applicability active; enforcement unlinked (required)

FAIL GOV-ADR-004 A decision that requires enforcement links a mandatory rule
  Why it matters: A decision that is marked `enforcement.required` but has no mandatory rule behind it is not checked by any run, so a violation of it passes unnoticed.
  ADR-1: 1 finding(s)
    docs/adr/0001-ledger-database-owned-by-ledger-service.md:1
      Decision: ADR-1 (status: accepted)
      Governs: calm:node:ledger-db, calm:relationship:ledger-to-database
  Fix: Add an `enforcedBy` link to a mandatory rule, make the linked rule mandatory, or set the enforcement of the decision to none.

```

Without the link, `GOV-ADR-004` fails. If somebody deletes the ADR, marks it superseded, or drops the rule, the ADR governance area fails in the same way. You restore the register:

```bash
cd /tmp/concordia-docs/tour && git checkout -q governance/decisions.yaml && tail -n 3 governance/decisions.yaml
```

```output
    enforcedBy:
      - rule: PAY-DB-001
        pack: {id: payments-architecture, version: 1.0.0}
```

Enforced does not mean obeyed. `PAY-DB-001` runs on every check, and today it fails: the nightly reporting job reads the ledger tables directly. That is the `reporting-job -> ledger-db` finding of section 3. See [Enforce architecture decisions](../use-cases/enforce-architecture-decisions.md) and [Protect database ownership](../use-cases/protect-database-ownership.md).

## 6. Approve the threat model

The security team keeps a threat model of the ledger. In Concordia, the threat model is an assessment, `ASM-LEDGER`. An approval counts only if it is a verified record that binds the exact subject that the reviewer examined. The subject here is the ledger API file `services/ledger-service/src/api/index.ts` and the trust context of `ledger-db` in the model.

`concordia assessment status` evaluates the assessment as `check` does. `--at` fixes the evaluation time, so that this page reads the same on any day.

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color
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

The document says `outcome satisfactory`, but that alone counts for nothing. Without a verified approval, the assessment is `review-required`.

Alice from security may approve it. The workspace lists her under the authority `security-review`. Her approval counts only when it is on the trust anchor, the protected branch: `origin/main` in a real repository, `main` here. First, she asks what exactly she would approve. Concordia prints the subject with a digest of each part, and writes nothing until she confirms the subject digest. The command also refuses to run when `CI` is `true`: a person records a decision, not a pipeline.

```bash
cd /tmp/concordia-docs/tour && concordia assessment approve ASM-LEDGER --reviewer alice@example.com --no-color
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

She confirms the subject digest, and Concordia writes the approval record. The record digest includes the time of the decision and differs on every run, so you drop that one line.

```bash
cd /tmp/concordia-docs/tour && concordia assessment approve ASM-LEDGER --reviewer alice@example.com --confirm-subject sha256:3cbb1c2620bea1bb97af54ca7f18527ca7a7a506cf90682b5c32eeac021ee43e --no-color 2>&1 | grep -v '^Record digest'
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

In a real repository, the record goes to the protected branch in a small pull request. Here you commit it to `main`. The status shows the time of the decision, which you replace with a placeholder for the same reason.

```bash
cd /tmp/concordia-docs/tour && git add assessments/approvals && git commit -q -m "Approve ASM-LEDGER v1" && concordia index | grep -v '^generation'
```

```output
project: payments-tour
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color 2>&1 | sed 's/decided [^;]*;/decided <time of the decision>;/'
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-LEDGER v1: current
  security, production, outcome satisfactory, review state approved, assessments/ledger-threat-model.yaml:2
  approval ASM-LEDGER-v1-1 by alice@example.com (security-review, protected-review), decided <time of the decision>; no expiry time

Required assessments
  ledger-threat-model: ASM-LEDGER v1 is current
```

The threat model is `current`. A listed reviewer approved it, the record is on the protected branch, and it binds the subject as it is now.

## 7. Fix the cause, and see who must look again

Now the payments team does what `PAY-API-001` asked. `PaymentService.ts` calls `assessPayment`, the published fraud API, instead of the internal risk model:

```bash
cat > /tmp/concordia-docs/tour/services/payments-service/src/PaymentService.ts <<'EOF'
import { assessPayment } from "../../fraud-service/src/api/index";
import { postEntry } from "../../ledger-service/src/api/index";

export class PaymentService {
  async authorize(paymentId: string, amountCents: number): Promise<boolean> {
    if (assessPayment(paymentId, amountCents).blocked) {
      return false;
    }
    const response = await fetch("http://card-network.example.com/authorize", {
      method: "POST",
      body: JSON.stringify({ paymentId, amountCents }),
    });
    if (!response.ok) {
      return false;
    }
    postEntry(paymentId, amountCents);
    return true;
  }
}
EOF
```

```output
```

During the outage, the payments service retried failed authorizations, and the ledger posted some payments twice. So the ledger team changes the ledger API in the same release: `postEntry` now ignores a payment that it already posted. It is the right fix, and it changes the exact file that Alice reviewed.

```bash
cat > /tmp/concordia-docs/tour/services/ledger-service/src/api/index.ts <<'EOF'
import { LedgerTable } from "../db/schema";

const table = new LedgerTable();
const posted = new Set<string>();

export function postEntry(paymentId: string, amountCents: number): number {
  if (posted.has(paymentId)) {
    return 0;
  }
  posted.add(paymentId);
  return table.insert(paymentId, amountCents);
}
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && git add services && git commit -q -m "Use the fraud API; ignore repeated ledger entries" && concordia index | grep -v '^generation'
```

```output
project: payments-tour
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

You run the rule of the outage and the review rule `REV-APR-001` together:

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color --rule PAY-API-001 --rule REV-APR-001 | sed -n '1,2p;/^Assessment currency/,/^Not configured/p' | sed '$d'
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: pass (1 pass)

Assessment currency: violation (1 violation, 1 findings, 1 assessments, 1 required, 1 reviewRequired)
  ASM-LEDGER v1 (security): review-required (subject digest mismatch)
    outcome satisfactory; approval ASM-LEDGER-v1-1 by security-review (protected-review); assessments/ledger-threat-model.yaml:2
    changed: modification of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification; next action: get review

FAIL REV-APR-001 A required assessment has a current verified approval (advisory: does not change the exit code)
  Why it matters: An assessment that nobody with authority approved for the subject as it is now, or whose approval expired, was revoked, or was bound to an earlier subject, is not review evidence for the architecture it covers.
  required ledger-threat-model, assessment ASM-LEDGER: 1 finding(s)
    assessments/ledger-threat-model.yaml:2
  Fix: Get review of the assessment version and record a verified approval, or write a new assessment version for the changed subject. When currency is unknown, supply the missing evidence or fix the analysis configuration.

```

`Conformance: pass (1 pass)` is `PAY-API-001`. The payments service no longer depends on the internals of the fraud service, so the refactor that caused the outage cannot stop payments again. Concordia prints only the rules that do not pass, so the passed rule has no block of its own.

`REV-APR-001` now fails. `assessment status` says why:

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color 2>&1 | sed 's/decided [^;]*;/decided <time of the decision>;/'
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

Alice's approval still exists and still verifies, but it no longer covers the subject: the ledger API file that she reviewed changed. The status names that file and the next action, `get review`. Nobody had to remember to ask her.

`REV-APR-001` is `advisory` in the example, so it shows on every pull request without failing the build. [Keep security reviews current](../use-cases/keep-security-reviews-current.md) shows how to make it `mandatory`.

## 8. What you learned

| Question from the incident | Where Concordia answered it |
|---|---|
| Would Concordia have caught the outage? | `PAY-API-001` names the import and the two calls into the fraud service's internals. |
| What else is waiting to break? | `PAY-PATH-001`, `PAY-CYC-001`, and `CORE-DECL-001` name the next dependencies that can stop payments. |
| Can the internet reach the ledger database? | `PAY-NET-002` reads the Terraform plan and names the security group rule that opens port 5432. |
| Is the decision about the ledger enforced? | ADR governance: `ADR-0001` is committed, active, and linked to `PAY-DB-001`. `GOV-ADR-004` fails when the link goes. |
| Does the threat model still hold? | Assessment currency: it did, until the reviewed ledger API changed. |

## Next steps

- [Getting started](getting-started.md) and [Brownfield adoption](brownfield-adoption.md) cover rules, bindings, and baselines that accept today's findings and fail only on new ones.
- The [use cases](../README.md#use-cases) show each rule of this tour on the same example: what Concordia reads, how it decides, and what you still declare.
- [Trace a deployment](../how-to/trace-a-deployment.md) follows a service from its source to the workloads in an environment.
- [Add stronger evidence](../how-to/add-stronger-evidence.md) adds rendered manifests and signed build and deployment records.
- [Govern architecture decisions](../how-to/govern-architecture-decisions.md) and [Review and approve assessments](../how-to/review-and-approve-assessments.md) do sections 5 to 7 on a real repository.
- [Evidence and trust](../explanation/evidence-and-trust.md) explains why Concordia reports `unknown` instead of guessing.

To clean up:

```bash
cd /tmp/concordia-docs/tour && concordia clean --all --yes && cd /tmp && rm -rf /tmp/concordia-docs/tour
```

```output
removed: .concordia/, build/concordia/, CBM project 'payments-tour'
```
