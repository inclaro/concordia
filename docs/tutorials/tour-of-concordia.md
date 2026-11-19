# A tour of Concordia

*2026-11-02T22:04:14Z by Showboat 0.6.1*
<!-- showboat-id: 24da9e4c-8f6a-4017-b4f5-301fe43c0b22 -->

Acme Shop sells online. Billing issues an invoice for every order, and fulfillment ships it. Last quarter, the fulfillment team refactored how it stores shipments, and month-end invoicing broke in production. Every pull request had been reviewed. Nobody saw that billing reached into fulfillment's internals.

In this tutorial, we run Concordia on a copy of Acme's code and ask the questions that incident raised. Would Concordia have caught it? What actually ships to production? Which decision says billing must not do this, and is it enforced? And when billing's code changes, does the security review still hold?

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

## 1. Copy Acme's code

The example holds Acme's architecture as a CALM model (billing, fulfillment, a ledger, and the legacy monolith), bindings that map each service to its code, and Acme's architecture rules. We copy it to a scratch directory.

```bash
rm -rf /tmp/concordia-docs/tour && mkdir -p /tmp/concordia-docs/tour && cp -R docs/examples/commerce/. /tmp/concordia-docs/tour && ls /tmp/concordia-docs/tour
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

The workspace file, `concordia.yaml`, says where the model, the bindings, and the rules are, and which code Codebase Memory (CBM) indexes. We write our own, so that it already lists the folders that later steps add.

```bash
cat > /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: acme-shop
architecture:
  source: architecture/commerce.calm.json
  urlMapping: architecture/url-mapping.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: commerce-tour
      root: .
      include: [services/**, packages/**, k8s/**, docs/adr/**, Makefile]
      exclude: [services/legacy/**]
  indexMode: full
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: [policies/**/*.policy.yaml]
exceptions:
  baseline: exceptions/concordia.baseline.json
  waivers: exceptions/waivers.yaml
check:
  minimumCoverage: 0.9
  unknown: fail
  failOn: error
EOF
```

```output
```

Concordia reads committed code, so we make the copy a Git repository.

```bash
cd /tmp/concordia-docs/tour && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Acme Shop" && git log --format=%s
```

```output
Acme Shop
```

## 2. Would Concordia have caught the outage?

`concordia index` builds the code graph. Its output also has a generation ID that changes with every commit; we drop that line with `grep -v` so that this page stays reproducible.

```bash
cd /tmp/concordia-docs/tour && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 6/7 covered (85.7%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

`concordia check` compares the code with the model and the rules:

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
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
```

Yes. `CO-BND-001` is the outage: billing imports and calls `ShipmentStore`, a class inside fulfillment, on lines 6 and 7 of `InvoiceService.ts`. A refactor of that class broke invoicing, exactly as the rule says. With Concordia in CI, the pull request that added the import would have failed.

The other findings are the next outage. Fulfillment calls back into billing when an order ships (`notify.ts`), which makes a cycle (`CO-CYC-001`), breaks the layering (`CO-LYR-001`), and is a data flow the model does not show (`CO-DECL-001`). Today a billing release can stop orders from shipping.

`CO-COV-001` is `unknown`, not a pass. The legacy monolith is Kotlin, which this index does not read, so nobody can say whether retiring it will break invoicing. Concordia says so instead of guessing. The `CO-OBS-016` warning is harmless: CBM reported a Git branch edge that no rule uses.

`results.json` holds the same result for CI. The exit code is 1:

```bash
cd /tmp/concordia-docs/tour && jq .summary.exitCode build/concordia/results.json
```

```output
1
```

## 3. What actually ships?

After the outage, the security team asks which image runs billing in production, and whether anything else runs next to it. We add Acme's production manifest, the billing Dockerfile, and the `Makefile` target that builds the image. The Deployment runs the billing app and an audit sidecar, two containers from one image repository at two tags.

```bash
mkdir -p /tmp/concordia-docs/tour/k8s/production && cat > /tmp/concordia-docs/tour/k8s/production/billing.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: billing
  namespace: commerce
spec:
  selector:
    matchLabels: {app: billing}
  template:
    metadata:
      labels: {app: billing}
    spec:
      containers:
        - name: app
          image: registry.example.com/commerce/billing:1.4.0
        - name: audit
          image: registry.example.com/commerce/billing:1.3.2
EOF
```

```output
```

```bash
cat > /tmp/concordia-docs/tour/services/billing/Dockerfile <<'EOF'
FROM node:22-alpine
WORKDIR /app
COPY src ./src
CMD ["node", "src/index.js"]
EOF
```

```output
```

```bash
printf 'billing-image:\n\tdocker build -t registry.example.com/commerce/billing:1.4.0 services/billing\n' > /tmp/concordia-docs/tour/Makefile
```

```output
```

Concordia never matches a workload to a service by a similar name. We declare the `production` environment and bind the `billing` Deployment and its image repository to `billing-service`.

```bash
cat >> /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/production/**"]
EOF
```

```output
```

```bash
cat >> /tmp/concordia-docs/tour/bindings/concordia.bindings.yaml <<'EOF'
  - id: bind-billing-workload
    architectureRef: calm:node:billing-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: billing, namespace: commerce}
  - id: bind-billing-image
    architectureRef: calm:node:billing-service
    match:
      any:
        - {kind: image, repository: registry.example.com/commerce/billing}
EOF
```

```output
```

We commit and index. `check` builds the trace; we skip its report here (`> /dev/null`) and read the trace of billing in production:

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Deploy billing" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 9/10 covered (90.0%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color > /dev/null; concordia trace billing-service --no-color
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Deployment trace: billing-service in production (cluster prod-eu): partial
Evidence: declared configuration only. Not traced: deployment execution and runtime.
Workloads of production: complete
1. service [declared intent, exact]
   billing-service
2. workload [parsed source fact, explicitly bound]
   Deployment commerce/billing  at k8s/production/billing.yaml:1  (bound by bind-billing-workload)
   namespace commerce
3. container image [parsed source fact, exact]
   container app: registry.example.com/commerce/billing:1.4.0  at k8s/production/billing.yaml:1
   gap: container audit of Deployment/billing (k8s/production/billing.yaml:1) has no image occurrence: the provider keeps one reference for each workload and repository (merged-occurrences)
   gap: container app uses registry.example.com/commerce/billing:1.4.0: the tag can point to other content later, and no immutable digest is known (mutable-tag)
4. build definition [inferred association, inferred]
   Dockerfile services/billing/Dockerfile, lineage candidate  at services/billing/Dockerfile:1, Makefile:2
   base image node:22-alpine  at services/billing/Dockerfile:1
5. source evidence [inferred association, explicitly bound]
   Dockerfile services/billing/Dockerfile  at services/billing/Dockerfile:1
   Deployment billing  at k8s/production/billing.yaml:1
   source services/billing/**  (bound by bind-billing)
Limits: declared-level-only, provider-may-merge-occurrences, image-provenance-unverified, source-provenance-unverified (--verbose explains each)
```

Read it from the top. Production runs Deployment `commerce/billing`, and the trace answers the security question only partly. It says so with two gaps.

The audit sidecar is missing. CBM keeps one image reference for each workload and repository, so the two containers were merged into one. Concordia noticed the container without an image and reported the gap, instead of showing a trace that looks complete. The app's tag can also move: `1.4.0` is not a digest, so nothing pins what it points to.

The Dockerfile is only a candidate. The `Makefile` builds this repository from `services/billing`, which suggests the Dockerfile, but nothing proves that the image in production came from it.

## 4. Recover the sidecar

Concordia can read the manifests itself instead of relying on the code graph. We declare a rendering source for production (plain YAML here; Helm charts and Kustomize overlays work the same way) and add the built-in `assurance` pack.

```bash
cat >> /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
assurance:
  rendering:
    sources:
      - {environment: production, tool: plain, path: k8s/production}
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && concordia policy add builtin:assurance
```

```output
added builtin:assurance
```

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Render production" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 9/10 covered (90.0%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

`concordia assurance validate` loads the new input on its own and says what it found:

```bash
cd /tmp/concordia-docs/tour && concordia assurance validate --no-color
```

```output
rendering production k8s/production
  integrity: content verified by digest
  admissible: yes
  coverage: complete

rendering by environment
  production: complete (1 source, 1 resource, 2 container occurrences)

1 input, 1 admissible
```

Two container occurrences, where the code graph had one. We check again, skip the report, and read the trace:

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color > /dev/null; concordia trace billing-service --no-color
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Deployment trace: billing-service in production (cluster prod-eu): partial
Evidence: declared configuration, and rendered or evaluated from 1 source (below). Not traced: deployment execution and runtime.
Workloads of production: complete
1. service [declared intent, exact]
   billing-service
2. workload [parsed source fact, explicitly bound]
   Deployment commerce/billing  at k8s/production/billing.yaml:1  (bound by bind-billing-workload)
   namespace commerce
3. container image [parsed source fact, exact]
   container app: registry.example.com/commerce/billing:1.4.0  at k8s/production/billing.yaml:1
   gap: container app uses registry.example.com/commerce/billing:1.4.0: the tag can point to other content later, and no immutable digest is known (mutable-tag)
4. build definition [inferred association, inferred]
   Dockerfile services/billing/Dockerfile, lineage candidate  at services/billing/Dockerfile:1, Makefile:2
   base image node:22-alpine  at services/billing/Dockerfile:1
5. source evidence [inferred association, explicitly bound]
   Dockerfile services/billing/Dockerfile  at services/billing/Dockerfile:1
   Deployment billing  at k8s/production/billing.yaml:1
   source services/billing/**  (bound by bind-billing)
Rendered or evaluated: evaluated
   render plain k8s/production: complete
   Deployment commerce/billing  at k8s/production/billing.yaml
Limits: declared-level-only, provider-may-merge-occurrences, image-provenance-unverified, source-provenance-unverified (--verbose explains each)
```

The `merged-occurrences` gap is gone. `results.json` records why: the container versions are `partial` at the declared level and `complete` at the rendered level, and the gap for the `audit` container is resolved by the rendered evidence.

```bash
cd /tmp/concordia-docs/tour && jq -r '.coverage.predicates[] | select(.predicate == "container-occurrence-versions" and .levels != null) | (.levels[] | "\(.level): \(.status)"), (.resolved[] | "resolved: \(.id), container \(.gaps[0].container), by \(.resolvedBy)")' build/concordia/results.json
```

```output
declared: partial
rendered-or-evaluated: complete
resolved: merged-occurrences, container audit, by rendered-or-evaluated
```

The `assurance` pack asks whether the stronger evidence is there:

```bash
cd /tmp/concordia-docs/tour && jq -r '.rules[] | select(.ruleId | startswith("ASR-")) | "\(.ruleId) \(.verdict): \(.name)" + (if .reasons[0] then " (" + .reasons[0].code + ")" else "" end)' build/concordia/results.json
```

```output
ASR-PRV-001 unknown: Each container occurrence has exact lineage from source to deployed image (claim-not-enabled)
ASR-PRV-002 unknown: The deployed digest of each container occurrence matches the observed digest (claim-not-enabled)
ASR-REN-001 pass: Each environment has complete rendered evidence
```

Production now has complete rendered evidence. The two provenance questions, whether the image came from this source and whether the cluster runs what was deployed, stay `unknown`: they need signed build and deployment records that Acme does not produce yet, and Concordia will not fill them in. All three rules are `advisory`, so they report without failing the build. [Add stronger evidence](../how-to/add-stronger-evidence.md) shows how to supply signed records.

## 5. Write down the goal behind the rule

The outage was a modifiability failure: a change inside fulfillment should never break billing. A quality scenario states that goal and ties it to the rules that protect it. It is `mandatory`, so it counts in the exit code, and it names the decision behind it, `ADR-0001`, which we write next.

```bash
mkdir -p /tmp/concordia-docs/tour/quality && cat > /tmp/concordia-docs/tour/quality/scenarios.yaml <<'EOF'
apiVersion: concordia.io/v1alpha1
kind: QualityScenarioSet
scenarios:
  - id: QS-INVOICE-CHANGE
    area: modifiability
    qualityAttribute: modifiability
    stimulusSource: developer
    stimulus: the fulfillment team refactors how it stores shipments
    environment: production
    expectedResponse: invoicing keeps working without a billing change
    owner: team-billing
    scope: [billing-service, fulfillment-service]
    mode: structural
    enforcement: mandatory
    criterion:
      rules: [CO-BND-001, CO-LYR-001]
    addressedBy: [ADR-0001]
EOF
```

```output
```

The scenario files belong under `assurance`, the last key of the workspace file:

```bash
cat >> /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
  quality:
    scenarios: [quality/*.yaml]
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "State the modifiability goal" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 9/10 covered (90.0%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color | tail -n 18
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Quality evaluation: violation (1 violation, 1 findings)
  QS-INVOICE-CHANGE (structural, mandatory, modifiability): violation
    QS-INVOICE-CHANGE: decision ADR-1 is unknown (adr-governance-not-available)
  Areas with a scenario: modifiability violation
  Areas without a scenario: availability, deployment-safety, operability, performance, security

FAIL quality:QS-INVOICE-CHANGE Quality scenario QS-INVOICE-CHANGE
  Why it matters: When the fulfillment team refactors how it stores shipments, invoicing keeps working without a billing change.
  Owner: team-billing
  scope billing-service, fulfillment-service: 1 finding(s)
    quality/scenarios.yaml:4
    Next action: fix implementation

Not configured: ADR governance, Diagram currency, Assessment currency

Unknown area(s): Deployment trace
Violation area(s): Quality evaluation
9 active error(s), 2 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 3 unknown
```

The goal fails today, because both rules it depends on fail: Acme is still one fulfillment refactor away from the same outage. The last area line shows that Acme has written no scenario for availability, security, or the other quality areas. The link to `ADR-0001` is `unknown` because no decision is registered yet.

## 6. Record the decision, and prove it is enforced

After the outage, Acme decided that billing uses only the fulfillment contract. An ADR records the decision, but a Markdown file cannot show that anything enforces it. A decision register links the ADR to the rule that does.

```bash
mkdir -p /tmp/concordia-docs/tour/docs/adr && cat > /tmp/concordia-docs/tour/docs/adr/0001-billing-uses-the-fulfillment-contract.md <<'EOF'
# ADR-0001: Billing uses the fulfillment contract

Status: Accepted

Date: 2026-01-01

Deciders: Team Billing

## Context

Billing reached into fulfillment internals, and a fulfillment refactor broke month-end invoicing.

## Decision

Billing depends only on the published fulfillment contract in `packages/fulfillment-contract`.

## Consequences

The rule CO-BND-001 keeps billing away from fulfillment internals.
EOF
```

```output
```

```bash
mkdir -p /tmp/concordia-docs/tour/governance && cat > /tmp/concordia-docs/tour/governance/decisions.yaml <<'EOF'
apiVersion: concordia.io/v1alpha1
kind: DecisionRegister
decisions:
  - id: ADR-0001
    path: docs/adr/0001-billing-uses-the-fulfillment-contract.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:billing-service
      - ref: calm:relationship:billing-to-fulfillment
    enforcement: required
    enforcedBy:
      - rule: CO-BND-001
        pack: {id: commerce-boundaries, version: 1.0.0}
EOF
```

```output
```

```bash
cat >> /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
adr:
  files: [governance/decisions.yaml]
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && concordia policy add builtin:adr-governance
```

```output
added builtin:adr-governance
```

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Record ADR-0001" && concordia adr validate
```

```output
adr: 1 decision(s) in 1 register file(s) valid; 1 rule link(s) checked; active statuses: accepted
```

```bash
cd /tmp/concordia-docs/tour && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 10/11 covered (90.9%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color | tail -n 21
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
ADR governance: pass (5 pass, 1 notApplicable)
  ADR-1 accepted (docs/adr/0001-billing-uses-the-fulfillment-contract.md:1)
    presence committed; applicability active; enforcement enforced (required; rules: CO-BND-001@commerce-boundaries/1.0.0)

Quality evaluation: violation (1 violation, 1 findings)
  QS-INVOICE-CHANGE (structural, mandatory, modifiability): violation
  Areas with a scenario: modifiability violation
  Areas without a scenario: availability, deployment-safety, operability, performance, security

FAIL quality:QS-INVOICE-CHANGE Quality scenario QS-INVOICE-CHANGE
  Why it matters: When the fulfillment team refactors how it stores shipments, invoicing keeps working without a billing change.
  Owner: team-billing
  scope billing-service, fulfillment-service: 1 finding(s)
    quality/scenarios.yaml:4
    Next action: fix implementation

Not configured: Diagram currency, Assessment currency

Unknown area(s): Deployment trace
Violation area(s): Quality evaluation
9 active error(s), 2 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 3 unknown
```

The decision is committed, active (`accepted`), and enforced by a rule that runs on every check. If someone deletes the ADR, marks it superseded, or drops `CO-BND-001`, this area fails. The scenario's link to the decision is no longer `unknown`.

## 7. Approve the security review

Billing handles money, so Acme's security team reviews its invoicing code. In Concordia, the review is an assessment, and an approval counts only if it is a verified record that binds the exact code that was reviewed. The assessment depends on one file, `InvoiceService.ts`.

```bash
mkdir -p /tmp/concordia-docs/tour/assessments/ASM-BILLING && cat > /tmp/concordia-docs/tour/assessments/ASM-BILLING/v1.yaml <<'EOF'
apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-BILLING
version: 1
type: security
title: Invoice handling in the billing service
scope:
  calmNodes: [billing-service]
environment: production
subject:
  summary: The billing source code that issues invoices
  dependencies:
    - kind: implementation
      selector:
        path: services/billing/src/InvoiceService.ts
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
EOF
```

```output
```

Alice from security may approve it. Her approval counts only once it is on the trust anchor, the protected branch: `origin/main` in a real repository, `main` here.

```bash
cat >> /tmp/concordia-docs/tour/concordia.yaml <<'EOF'
assessments:
  paths: [assessments]
  trustAnchor: {ref: main}
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [alice@example.com]
      paths: [assessments/approvals/**]
  required:
    - {id: billing-security, assessment: ASM-BILLING, type: security, scope: {calmNodes: [billing-service]}, environment: production, authority: security-review}
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && concordia policy add builtin:review-currency
```

```output
added builtin:review-currency
```

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Assess billing" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 10/11 covered (90.9%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

`concordia assessment status` evaluates the review as `check` does. `--at` fixes the evaluation time so that this page reads the same on any day.

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-BILLING v1: review-required
  security, production, outcome satisfactory, review state ready-for-review, assessments/ASM-BILLING/v1.yaml:1
  why: no approval
    no verified approval binds ASM-BILLING@v1; a reviewed flag or a note in the document is not an approval
  changed: addition of implementation:file:services/billing/src/InvoiceService.ts, class implementation or verification
  next action: get review

Required assessments
  billing-security: ASM-BILLING v1 is review-required (no approval)
```

The document says `outcome satisfactory`, but that alone counts for nothing: without a verified approval the review is `review-required`. Alice first asks what exactly she would approve. Concordia prints the subject with a digest of each file, and writes nothing until she confirms that digest. The command also refuses to run when `CI` is `true`: a person records a decision, not a pipeline.

```bash
cd /tmp/concordia-docs/tour && concordia assessment approve ASM-BILLING --reviewer alice@example.com --no-color
```

```output
Assessment ASM-BILLING@v1 (security, environment production, outcome satisfactory)
Title: Invoice handling in the billing service
Version digest: sha256:4c2e9643913f5828452e81471318def187b73754089c23b1069e84111cdcb8fc
Digest contract: review-digest/1, capability profile: cbm-0.12.1
Dependencies (1):
  implementation file:services/billing/src/InvoiceService.ts sha256:125134fece377cc99f3dd9c02c862fd695a8f16a1bdfde288b326427bd3b43b6
Assumptions: 0, evidence: 0
Subject digest: sha256:cc2827c2b962a8e30b99214e373ee31d56b0c0a65208a6b435d713f72097e1b5
error CO-ASM-005: assessment approve or revoke refused: confirm the subject that you examined with --confirm-subject sha256:cc2827c2b962a8e30b99214e373ee31d56b0c0a65208a6b435d713f72097e1b5
```

She confirms the subject digest, and Concordia writes the approval record. The record digest includes the time of the decision and differs on every run, so we drop that one line.

```bash
cd /tmp/concordia-docs/tour && concordia assessment approve ASM-BILLING --reviewer alice@example.com --confirm-subject sha256:cc2827c2b962a8e30b99214e373ee31d56b0c0a65208a6b435d713f72097e1b5 --no-color | grep -v '^Record digest'
```

```output
Assessment ASM-BILLING@v1 (security, environment production, outcome satisfactory)
Title: Invoice handling in the billing service
Version digest: sha256:4c2e9643913f5828452e81471318def187b73754089c23b1069e84111cdcb8fc
Digest contract: review-digest/1, capability profile: cbm-0.12.1
Dependencies (1):
  implementation file:services/billing/src/InvoiceService.ts sha256:125134fece377cc99f3dd9c02c862fd695a8f16a1bdfde288b326427bd3b43b6
Assumptions: 0, evidence: 0
Subject digest: sha256:cc2827c2b962a8e30b99214e373ee31d56b0c0a65208a6b435d713f72097e1b5
approval written: assessments/approvals/ASM-BILLING-v1-1.yaml
Concordia counts this record only when it is on the trust anchor: merge it into the anchor branch (a change that touches only this file), then rebuild the candidate on the anchor.
```

In a real repository, the record goes to the protected branch in a small pull request. Here we commit it to `main`. The status shows the time of the decision, which we replace with a placeholder for the same reason.

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Approve ASM-BILLING v1" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 10/11 covered (90.9%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color | sed 's/decided [^;]*;/decided <time of the decision>;/'
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-BILLING v1: current
  security, production, outcome satisfactory, review state approved, assessments/ASM-BILLING/v1.yaml:1
  approval ASM-BILLING-v1-1 by alice@example.com (security-review, protected-review), decided <time of the decision>; no expiry time

Required assessments
  billing-security: ASM-BILLING v1 is current
```

The review is `current`: a listed reviewer approved it, the record is on the protected branch, and it binds the code as it is now.

## 8. Fix the outage's cause, and see who needs to know

Now the billing team does what `CO-BND-001` asked: `InvoiceService.ts` stops using `ShipmentStore` and relies on the contract only. It is the right fix, and it changes the exact file that Alice reviewed.

```bash
cat > /tmp/concordia-docs/tour/services/billing/src/InvoiceService.ts <<'EOF'
import { ShipmentRequest } from "../../../packages/fulfillment-contract/src/index";

export class InvoiceService {
  issue(orderId: string, amountCents: number): ShipmentRequest {
    return { orderId, total: amountCents };
  }
}
EOF
```

```output
```

```bash
cd /tmp/concordia-docs/tour && git add -A && git commit -q -m "Billing uses only the fulfillment contract" && concordia index | grep -v '^generation'
```

```output
project: commerce-tour
files: 10/11 covered (90.9%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

```bash
cd /tmp/concordia-docs/tour && concordia assessment status --at 2030-01-01 --no-color | sed 's/decided [^;]*;/decided <time of the decision>;/'
```

```output
Assessment currency at 2030-01-01T00:00:00.000Z (digest contract review-digest/1, capability profile cbm-0.12.1)

ASM-BILLING v1: review-required
  security, production, outcome satisfactory, review state review-required, assessments/ASM-BILLING/v1.yaml:1
  approval ASM-BILLING-v1-1 by alice@example.com (security-review, protected-review), decided <time of the decision>; no expiry time
  why: subject digest mismatch
    1 dependency item(s) differ from the subject that approval ASM-BILLING-v1-1 binds: modified implementation file:services/billing/src/InvoiceService.ts
  changed: modification of implementation:file:services/billing/src/InvoiceService.ts, class implementation or verification
  next action: get review

Required assessments
  billing-security: ASM-BILLING v1 is review-required (subject digest mismatch)
```

Alice's approval still exists and still verifies, but it no longer covers the code: the file she reviewed changed. The status names that file and the next action, get a review. Nobody had to remember to ask her.

The last check shows where Acme stands:

```bash
cd /tmp/concordia-docs/tour && concordia check --no-color
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (3 violation, 3 unknown, 2 pass, 7 findings)

UNKNOWN ASR-PRV-001 Each container occurrence has exact lineage from source to deployed image (advisory: does not change the exit code)
  Why it matters: A tag or a static link from an image to a Dockerfile only suggests where an image came from. Exact lineage needs an admissible chain of statements from the source revision to the digest that runs, or the deployed image cannot be traced to the code that was reviewed.
  Cannot judge: no loaded contract enables claim "exact-lineage" (claim-not-enabled)

UNKNOWN ASR-PRV-002 The deployed digest of each container occurrence matches the observed digest (advisory: does not change the exit code)
  Why it matters: When the digest that a cluster runs differs from the digest that the deployment record says was rolled out, the deployed software is not the software that was reviewed and traced.
  Cannot judge: no loaded contract enables claim "runtime-drift" (claim-not-enabled)

UNKNOWN CO-COV-001 Legacy must not call Billing
  Why it matters: The legacy monolith is being retired. If it still calls billing, switching it off breaks invoicing.
  Cannot judge: minimum coverage not met: 0.667 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 66.7% (required 90.0%)

FAIL CO-CYC-001 Services are acyclic
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other.
  Owner: team-billing
  Coverage: 87.5% (required 90.0%)
  cycle through billing-service, fulfillment-service: 1 finding(s)
    services/billing/src/InvoiceService.ts  imports packages/fulfillment-contract/src/index.ts
                                            then notifyShipped calls InvoiceService in services/billing/src/InvoiceService.ts
  Fix: Break the cycle. For example, let fulfillment publish a shipped event that billing consumes, instead of calling billing.

FAIL CO-DECL-001 Dependencies are declared
  Why it matters: Reviews and threat models read the architecture model. A dependency that the model does not show is a data flow that nobody reviewed.
  Owner: team-fulfillment
  Coverage: 87.5% (required 90.0%)
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

Deployment trace: unknown (1 partial, 1 subjects)
  Evidence: declared configuration, and rendered or evaluated in production. Not traced: deployment execution and runtime.
  Subjects: 1 in 1 environment(s): 0 complete, 1 partial, 0 unresolved, 0 unknown
  partial: billing-service in production

ADR governance: pass (5 pass, 1 notApplicable)
  ADR-1 accepted (docs/adr/0001-billing-uses-the-fulfillment-contract.md:1)
    presence committed; applicability active; enforcement enforced (required; rules: CO-BND-001@commerce-boundaries/1.0.0)

Assessment currency: violation (1 violation, 3 pass, 1 findings, 1 assessments, 1 required, 1 reviewRequired)
  ASM-BILLING v1 (security): review-required (subject digest mismatch)
    outcome satisfactory; approval ASM-BILLING-v1-1 by security-review (protected-review); assessments/ASM-BILLING/v1.yaml:1
    changed: modification of implementation:file:services/billing/src/InvoiceService.ts, class implementation or verification; next action: get review

FAIL REV-APR-001 A required assessment has a current verified approval (advisory: does not change the exit code)
  Why it matters: An assessment that nobody with authority approved for the subject as it is now, or whose approval expired, was revoked, or was bound to an earlier subject, is not review evidence for the architecture it covers.
  required billing-security, assessment ASM-BILLING: 1 finding(s)
    assessments/ASM-BILLING/v1.yaml:1
  Fix: Get review of the assessment version and record a verified approval, or write a new assessment version for the changed subject. When currency is unknown, supply the missing evidence or fix the analysis configuration.

Quality evaluation: violation (1 violation, 1 findings)
  QS-INVOICE-CHANGE (structural, mandatory, modifiability): violation
  Areas with a scenario: modifiability violation
  Areas without a scenario: availability, deployment-safety, operability, performance, security

FAIL quality:QS-INVOICE-CHANGE Quality scenario QS-INVOICE-CHANGE
  Why it matters: When the fulfillment team refactors how it stores shipments, invoicing keeps working without a billing change.
  Owner: team-billing
  scope billing-service, fulfillment-service: 1 finding(s)
    quality/scenarios.yaml:4
    Next action: fix implementation

Not configured: Diagram currency

Unknown area(s): Deployment trace
Violation area(s): Assessment currency, Quality evaluation
7 active error(s), 2 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 3 unknown
```

`CO-BND-001` is gone: billing no longer depends on fulfillment's internals, so the refactor that caused the outage would not break invoicing today. Three things remain, and each says what is at stake.

- The security review needs Alice again (`REV-APR-001`). It is `advisory`, so it shows on every pull request without failing the build until Acme makes it `mandatory`.
- Fulfillment still calls back into billing in `notify.ts`. That keeps the cycle (billing now reaches fulfillment through its contract package, which belongs to fulfillment), the layering violation, and the undeclared dependency. Each `Fix:` line says what to change.
- The modifiability goal still fails, because `CO-LYR-001` still fails. It turns green when `notify.ts` is fixed.

## What we learned

| Question from the incident | Where Concordia answered it |
|---|---|
| Would it have been caught? | `CO-BND-001` points at the two lines that caused the outage. |
| What else is waiting to break? | The cycle and layering findings in `notify.ts`, and the legacy calls Concordia cannot judge. |
| What actually ships? | The deployment trace, with the sidecar that the code graph lost and the rendered manifests that recovered it. |
| Is the decision enforced? | ADR governance: `ADR-0001` is committed, active, and enforced by `CO-BND-001`. |
| Is the security review still valid? | Assessment currency: it was, until the reviewed file changed. |

## Next steps

- [Getting started](getting-started.md) and [Brownfield adoption](brownfield-adoption.md) cover rules, bindings, and baselines that accept today's findings and fail only on new ones.
- [Trace a deployment](../how-to/trace-a-deployment.md), [Add stronger evidence](../how-to/add-stronger-evidence.md), [Govern architecture decisions](../how-to/govern-architecture-decisions.md), and [Review and approve assessments](../how-to/review-and-approve-assessments.md) do each step on a real repository.
- [Keep diagrams current](../how-to/keep-diagrams-current.md) covers diagram currency, the one area this tour leaves out.
- [Evidence and trust](../explanation/evidence-and-trust.md) explains why Concordia reports `unknown` instead of guessing.

To clean up:

```bash
cd /tmp/concordia-docs/tour && concordia clean --all --yes && cd /tmp && rm -rf /tmp/concordia-docs/tour
```

```output
removed: .concordia/, build/concordia/, CBM project 'commerce-tour'
```
