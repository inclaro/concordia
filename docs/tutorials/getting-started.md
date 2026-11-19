# Getting started: your first architecture check

*2026-10-21T01:26:35Z by Showboat 0.6.1*
<!-- showboat-id: 1e763754-113a-4051-9aab-f85785c9d380 -->

We will check a small commerce codebase against its CALM architecture and watch Concordia report where the code breaks the architecture. You need Concordia and the Codebase Memory MCP server (CBM) on your PATH, and `git` and `jq`. If you have not installed them yet, follow [Install Concordia](../how-to/install.md) first.

Every command and output below is real. You can re-run the whole page with `showboat verify docs/tutorials/getting-started.md`.

First, we check the versions. Notice that the output names four versions: Concordia, the CALM schema it supports, the adapter protocol, and the CBM release it found.

```bash
concordia --version
```

```output
concordia dev
calm-schema 1.2,1.3
cbm-adapter concordia-cbm/v2
cbm 0.12.1
```

Now we create a throwaway git repository from the sample project. Run the commands from the root of a Concordia checkout. We copy `docs/examples/commerce` to `/tmp/concordia-docs/getting-started` and name its CBM project `commerce-getting-started`. Running the commands again always starts from a clean state.

```bash
rm -rf /tmp/concordia-docs/getting-started && mkdir -p /tmp/concordia-docs/getting-started && cp -R docs/examples/commerce/. /tmp/concordia-docs/getting-started && cd /tmp/concordia-docs/getting-started && sed -i.bak 's/name: commerce-example/name: commerce-getting-started/' concordia.yaml && rm concordia.yaml.bak && ls
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

Concordia reads the committed state of a repository, so we make it a git repository with one commit. We give it a fixed author so that the commits look the same for everyone.

```bash
cd /tmp/concordia-docs/getting-started && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial commerce example" && git log --format=%s
```

```output
Initial commerce example
```

The sample holds a CALM architecture, a binding file, a policy pack, a waiver file, and two source trees. The formats are described in [architecture extensions](../reference/architecture.md), [bindings](../reference/bindings.md) and [policies](../reference/policies.md). The workspace file ties them together. Notice that it says which code to observe (`include` and `exclude`) and where the architecture, bindings and policies live.

```bash
cd /tmp/concordia-docs/getting-started && cat concordia.yaml
```

```output
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: commerce-platform
architecture:
  source: architecture/commerce.calm.json
  urlMapping: architecture/url-mapping.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: commerce-getting-started
      root: .
      include: [services/**, packages/**]
      exclude: [services/legacy/**]
  indexMode: fast
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
```

Next, we validate the architecture model. A valid model is the precondition for everything else.

```bash
cd /tmp/concordia-docs/getting-started && concordia model validate
```

```output
model valid: architecture/commerce.calm.json (0 warning(s))
```

Now we index the code. Concordia asks CBM to build a graph of the repository, then records the graph it will use as a generation. Notice the coverage line, `files: 6/7 covered`. The workspace excludes the legacy file, but the file still counts in the total. The output also names the generation, an ID that changes with every commit; we leave that one line out with `grep -v` so that this page stays reproducible.

```bash
cd /tmp/concordia-docs/getting-started && concordia index | grep -v '^generation'
```

```output
project: commerce-getting-started
files: 6/7 covered (85.7%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Bindings connect architecture nodes to code. Before we check anything, we confirm that the binding file is valid and see how much of the code it covers.

```bash
cd /tmp/concordia-docs/getting-started && concordia bind validate
```

```output
bindings valid: 4 binding(s), 25/97 observed node(s) bound, 0 unbound file(s)
```

Now we run the check. Read each `FAIL` block from the top: the rule, why it matters, who owns it, and its findings grouped by the two architecture elements they connect. Each finding is one line: the file and line, and what the code does there (`imports` a file, `calls` a class or method). `Fix:` says what to change. The confidence of each edge and the full fingerprints are in `--verbose` and in `concordia explain`. The command exits with code 1 because eight findings are active errors. The first line, `warning CO-OBS-016`, says that CBM reported an edge kind that Concordia does not use (`HAS_BRANCH`, from the git history). Concordia keeps it as unsupported evidence and it changes no verdict.

```bash
cd /tmp/concordia-docs/getting-started && concordia check --no-color; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
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
exit code: 1
```

We can ask Concordia to explain one rule. `explain` prints the rationale, the remediation, the compiled rule, the coverage numbers, and the evidence for every finding.

```bash
cd /tmp/concordia-docs/getting-started && concordia explain CO-BND-001 --no-color
```

```output
CO-BND-001 Billing uses Fulfillment contracts only
Verdict: violation   Severity: error   Family: boundary   Policy: commerce-boundaries@1.0.0
Rationale: When fulfillment refactors its internals, invoicing breaks. The fulfillment contract is the part that fulfillment keeps stable for billing.
Remediation: Import from packages/fulfillment-contract instead.
Rule owner: team-billing
Subjects: calm:node:billing-service
Targets: calm:node:fulfillment-service
Plan:
  {
    "aggregation": "edge",
    "evidenceRequirements": {
      "minimumConfidence": "syntactic",
      "minimumCoverage": 0.9,
      "onUnknown": "fail"
    },
    "name": "Billing uses Fulfillment contracts only",
    "owner": "team-billing",
    "pack": {
      "id": "commerce-boundaries",
      "version": "1.0.0"
    },
    "predicate": {
      "allow": {
        "edgeKinds": [],
        "targetCalmNodes": [],
        "targetSourceRoots": [
          "packages/fulfillment-contract/**"
        ],
        "targetTags": []
      },
      "family": "boundary"
    },
    "rationale": "When fulfillment refactors its internals, invoicing breaks. The fulfillment contract is the part that fulfillment keeps stable for billing.",
    "remediation": "Import from packages/fulfillment-contract instead.",
    "ruleId": "CO-BND-001",
    "severity": "error",
    "subjects": [
      "billing-service"
    ],
    "targets": [
      "fulfillment-service"
    ],
    "traversal": {
      "direction": "outbound",
      "edgeKinds": [
        "calls",
        "imports"
      ],
      "maximumDepth": 1,
      "minimumDepth": 1
    },
    "unsupportedEdgeKinds": []
  }
Coverage:
  files: 100.0% (4/4 covered, required 90.0%)
  excluded: 0
  stale: 0
  unindexed: 0
  unbound endpoints: 2
  sub-threshold evidence: 0
  evidence: minimum confidence syntactic, on unknown fail

Finding 1efd…786
  Fingerprint: sha256:1efd7ca9d4bf67534af113a43fa53900b6e8b59406c498f688c84a5f61bd9786
  Kind: forbidden-dependency
  Message: forbidden dependency billing-service -> fulfillment-service
  Source: calm:node:billing-service
  Target: calm:node:fulfillment-service
  Owner: team-billing
  Disposition: active
  Location: services/billing/src/InvoiceService.ts
  Evidence path:
    File services.billing.src.InvoiceService.ts.__file__ (services/billing/src/InvoiceService.ts)
    -[IMPORTS syntactic, cbm:IMPORTS]-> at services/billing/src/InvoiceService.ts
    Module services.fulfillment.src.internal.ShipmentStore (services/fulfillment/src/internal/ShipmentStore.ts:1)

Finding 3f8c…13a
  Fingerprint: sha256:3f8c9c39703b27041ce8bb5f2a8c3e2f4bb7c3a8eaf9baac0d1a0bdcee90113a
  Kind: forbidden-dependency
  Message: forbidden dependency billing-service -> fulfillment-service
  Source: calm:node:billing-service
  Target: calm:node:fulfillment-service
  Owner: team-billing
  Disposition: active
  Location: services/billing/src/InvoiceService.ts:7
  Evidence path:
    Method services.billing.src.InvoiceService.InvoiceService.issue (services/billing/src/InvoiceService.ts:5)
    -[CALLS resolved, cbm:CALLS:lsp_ts_method]-> at services/billing/src/InvoiceService.ts:7
    Method services.fulfillment.src.internal.ShipmentStore.ShipmentStore.reserve (services/fulfillment/src/internal/ShipmentStore.ts:4)

Finding d021…cd2
  Fingerprint: sha256:d021e2b567a29f195262f7426788a8c73ab5c2ab3094f04bc073e3efec9cdcd2
  Kind: forbidden-dependency
  Message: forbidden dependency billing-service -> fulfillment-service
  Source: calm:node:billing-service
  Target: calm:node:fulfillment-service
  Owner: team-billing
  Disposition: active
  Location: services/billing/src/InvoiceService.ts:6
  Evidence path:
    Method services.billing.src.InvoiceService.InvoiceService.issue (services/billing/src/InvoiceService.ts:5)
    -[CALLS syntactic, cbm:CALLS:import_map]-> at services/billing/src/InvoiceService.ts:6
    Class services.fulfillment.src.internal.ShipmentStore.ShipmentStore (services/fulfillment/src/internal/ShipmentStore.ts:1)
```

One rule in the earlier output did not fail. `CO-COV-001` has the verdict `unknown`, with reason `CO-CON-021`: the rule watches the legacy code, but the workspace excludes that code, so coverage is below the required 90%. Notice that the exit code is 4, not 0. Concordia never reports code it could not see as passing. The [how-to guide on unknown verdicts](../how-to/resolve-unknown-verdicts.md) shows the fixes.

```bash
cd /tmp/concordia-docs/getting-started && concordia check --rule CO-COV-001 --no-color; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: unknown (1 unknown)

UNKNOWN CO-COV-001 Legacy must not call Billing
  Why it matters: The legacy monolith is being retired. If it still calls billing, switching it off breaks invoicing.
  Cannot judge: minimum coverage not met: 0.500 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 50.0% (required 90.0%)

Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation
No environment declared: the deployment trace and environment-specific assurance need a declared environment.

0 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 4
```

Finally, we render the architecture as a Structurizr workspace. The `dsl` format needs no other tool. We count the CALM nodes in the file with `grep`.

```bash
cd /tmp/concordia-docs/getting-started && concordia render --format dsl && grep -c '"calm.unique-id"' build/concordia/structurizr/workspace.dsl
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
workspace written: /tmp/concordia-docs/getting-started/build/concordia/structurizr/workspace.dsl
10
```

We clean up. `clean --all` removes Concordia's local state and the CBM project that `index` created.

```bash
cd /tmp/concordia-docs/getting-started && concordia clean --all --yes
```

```output
removed: .concordia/, build/concordia/, CBM project 'commerce-getting-started'
```

You have indexed a codebase, bound it to an architecture, found eight violations with their evidence, and met an `unknown` verdict. Next steps:

- [Adopting Concordia on an existing codebase](brownfield-adoption.md) shows how to accept today's violations and fail only on new ones.
- [Run Concordia in CI](../how-to/run-in-ci.md) puts the check in your pipeline.
- [Write policies](../how-to/write-policies.md) shows how to add your own rules.
