# Keep services free of cycles

When two services depend on each other, you cannot release, roll back, or restart one of them alone. An incident in one spreads to the other. At the bank, payments posts entries to the ledger, and a settlement job in the ledger calls back into payments. A ledger incident can then stop payments. This rule finds such cycles between services in the code.

## The rule

The rule is in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-CYC-001
  name: Services are free of cycles
  severity: error
  owner: team-architecture
  rationale: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other. A cycle between payments and the ledger means that a ledger incident can stop payments.
  remediation: Break the cycle. For example, let payments publish a settled event that the ledger consumes, instead of the ledger calling payments.
  subject:
    nodeTypes: [service]
  require:
    graphProperty: acyclic
    edges: [IMPORTS, CALLS]
    collapseBy: architectureBinding
```

`collapseBy: architectureBinding` merges all code of a service into its CALM node before Concordia searches for cycles. A cycle inside one service is not a finding.

## What Concordia reads

- **Source code.** The TypeScript files under `services/` and `jobs/`, at the committed revision. CBM (Codebase Memory MCP) parses them into a code graph, and Concordia reads the `IMPORTS` and `CALLS` edges from that graph.
- **Bindings.** `bindings/concordia.bindings.yaml` maps each source root to one CALM node, for example `services/ledger-service/**` to `ledger-service`.
- **Architecture.** `architecture/payments.calm.json` gives the node type `service` that the subject selects.

The CALM relationships do not decide the result. The model declares `payments-service → ledger-service` and does not declare `ledger-service → payments-service`. Concordia finds the cycle from the code edges in both directions, not from the model.

## How Concordia decides

1. Concordia selects every CALM node of type `service`.
2. It maps each code entity to its CALM node through the bindings.
3. It reads the `IMPORTS` and `CALLS` edges at `syntactic` confidence or higher, and collapses them into edges between CALM nodes.
4. It searches the collapsed graph for cycles.
5. Each cycle is one finding of kind `cycle`. The evidence names one code edge for each step of the cycle.
6. If the graph covers fewer in-scope files than the required coverage (here 90%), the result is `unknown`, not `pass`.

## What you still declare

- **The bindings.** Code that no binding maps to a service is not part of any node, so a cycle through it is not found.
- **The node types.** The rule checks only nodes of type `service` in the CALM file.
- **The edge kinds and the confidence.** A dependency that CBM reports only at `heuristic` confidence, for example an HTTP call, does not count. See [confidence levels](../reference/policies.md#confidence-levels).
- **The scope of observation.** The example excludes the legacy Kotlin service from observation. A cycle through `legacy-core-banking` is not found. `concordia explain` shows this gap as `CO-CON-026`.

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/keep-services-free-of-cycles && mkdir -p /tmp/concordia-usecases/keep-services-free-of-cycles && cp -R docs/examples/payments/. /tmp/concordia-usecases/keep-services-free-of-cycles && cd /tmp/concordia-usecases/keep-services-free-of-cycles && sed -i.bak 's/payments-example/payments-uc-keep-services-free-of-cycles/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-services-free-of-cycles
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check the rule. The `sed` command keeps the summary, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/keep-services-free-of-cycles && concordia check --rule PAY-CYC-001 --no-color 2>/dev/null | sed -n '1p;/^FAIL/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Conformance: violation (1 violation, 1 findings)
FAIL PAY-CYC-001 Services are free of cycles
  Why it matters: A cycle ties two services together. Neither can be released or rolled back on its own, and an outage in one spreads to the other. A cycle between payments and the ledger means that a ledger incident can stop payments.
  Owner: team-architecture
  cycle through ledger-service, payments-service: 1 finding(s)
    services/ledger-service/src/settlement.ts:4  calls services.payments-service.src.api.paymentStatus in services/payments-service/src/api/index.ts
                                                 then PaymentService.authorize calls services.ledger-service.src.api.postEntry in services/ledger-service/src/api/index.ts
  Fix: Break the cycle. For example, let payments publish a settled event that the ledger consumes, instead of the ledger calling payments.
1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

The finding shows both halves of the cycle. `settlement.ts` in the ledger calls `paymentStatus` in payments. `PaymentService.authorize` in payments calls `postEntry` in the ledger. Both calls go through published APIs, so an API rule would not find them. Only the cycle rule does.

See the evidence and the coverage gaps:

```bash
cd /tmp/concordia-usecases/keep-services-free-of-cycles && concordia explain PAY-CYC-001 --no-color | sed -n '1,2p;/^Reasons:/,/^$/p;/^Finding 6a20/,$p'
```

```output
PAY-CYC-001 Services are free of cycles
Verdict: violation   Severity: error   Family: acyclic   Policy: payments-architecture@1.0.0
Reasons:
  CO-CON-026: language coverage not met: .kt 0.000 < 0.9

Finding 6a20…e22
  Fingerprint: sha256:6a208047c13f2f301ece8b2a4db4c41096d8de5964156b193ec29c1dfb27de22
  Kind: cycle
  Message: dependency cycle: ledger-service -> payments-service -> ledger-service
  Source: calm:node:ledger-service
  Target: calm:node:ledger-service, calm:node:payments-service
  Owner: team-ledger
  Disposition: active
  Location: services/ledger-service/src/settlement.ts:4
  Evidence path:
    Function services.ledger-service.src.settlement.settle (services/ledger-service/src/settlement.ts:3)
    -[CALLS syntactic, cbm:CALLS:unique_name]-> at services/ledger-service/src/settlement.ts:4
    Function services.payments-service.src.api.paymentStatus (services/payments-service/src/api/index.ts:15)
    Method services.payments-service.src.PaymentService.PaymentService.authorize (services/payments-service/src/PaymentService.ts:5)
    -[CALLS syntactic, cbm:CALLS:unique_name]-> at services/payments-service/src/PaymentService.ts:17
    Function services.ledger-service.src.api.postEntry (services/ledger-service/src/api/index.ts:3)
  Related locations:
    services/payments-service/src/PaymentService.ts:17
```

The evidence path lists one code edge for each step of the cycle. `CO-CON-026` says that no Kotlin file is in the graph. The rule still reports the cycle that it found, but it cannot show that the legacy service is free of cycles.

## Make it block a merge

The rule fails the build as shipped. Its severity is `error`, and a rule that is not a typed rule gates as `mandatory` by default. With `check.failOn: error`, `concordia check` exits with code 1 when a cycle is active. Run `concordia index` and then `concordia check` in CI; see [run Concordia in CI](../how-to/run-in-ci.md).

The workspace sets `check.unknown: fail`. When coverage falls below 90%, the rule is `unknown` and `check` exits with code 4.

If your code already has cycles, record them in a baseline. Then only a new cycle blocks a merge. See [manage baselines and waivers](../how-to/manage-baselines-and-waivers.md).

## Related

- [`require` (acyclic) in the policies reference](../reference/policies.md#require-acyclic)
- [Require an acyclic graph](../how-to/write-policies.md#require-an-acyclic-graph)
- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
- [Integrate through published APIs](integrate-through-published-apis.md)
- [Keep critical paths isolated](keep-critical-paths-isolated.md)
