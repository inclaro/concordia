# Keep the model in sync with code

An architecture model that misses a dependency gives wrong answers when you need it most. At a bank, the nightly report reads the ledger tables directly, and the model does not show it. When the ledger team changes the schema, nobody knows that the report breaks, and month-end figures go out wrong.

## The rule

`CORE-DECL-001` is in the built-in pack `builtin:core`. This is the rule as the pack defines it:

```yaml
- id: CORE-DECL-001
  name: Dependencies between services, databases, and web clients are declared
  severity: error
  rationale: An architecture model is only a source of truth when it shows every dependency that exists in the code. A hidden dependency is a coupling that nobody decided on.
  remediation: Remove the dependency from the code, or declare it as a relationship in the architecture model. Edges of kind HTTP_CALLS and ASYNC_CALLS are heuristic in the CBM adapter, so lower evidence.minimumConfidence to heuristic with a rule override to check them.
  subject:
    nodeTypes: [service, database, webclient]
  declaredOnly:
    edges: [IMPORTS, CALLS, HTTP_CALLS, ASYNC_CALLS, READS, WRITES]
```

The example enables the pack in `concordia.yaml` with `policies.packs: [{use: builtin:core}, ...]`.

## What Concordia reads

- **Committed code.** Codebase Memory (CBM) indexes the TypeScript files under `services/**` and `jobs/**` at the committed revision. Concordia reads the import, call, HTTP call, async call, read, and write edges from that index.
- **The CALM model** (`architecture/payments.calm.json`). Concordia reads the node types and the `connects` relationships. The model is the claim that the rule tests. It is not evidence that a dependency exists or does not exist.
- **The bindings** (`bindings/concordia.bindings.yaml`). A `sourceRoot` binding maps a folder to a CALM node.

The result comes from the code. A relationship in the model never hides an edge, and a missing relationship never creates one.

## How Concordia decides

1. The bindings map each indexed file to a CALM node. For example, `jobs/reporting/src/nightly.ts` belongs to `reporting-job`, and `services/ledger-service/src/db/schema.ts` belongs to `ledger-db`.
2. Concordia takes every edge of a listed kind from the index. It keeps the edges whose two ends belong to two different nodes of type `service`, `database`, or `webclient`.
3. For each such edge, Concordia looks for a declared relationship between the two nodes in the model.
4. No relationship gives an `undeclared` finding. A relationship that does not allow the edge kind gives a `contradiction` finding.
5. Each edge has a confidence level. An edge below the minimum confidence of the rule (`syntactic` by default) is counted as sub-threshold and is not reported. An edge to a target that no binding covers gives a `binding-gap` finding instead of a verdict on that edge.
6. One finding makes the rule a `violation`.

## What you still declare

- **The model.** You write the nodes, their types, and the relationships. Concordia checks the model against the code. It does not write the model for you.
- **The bindings.** A file outside every binding belongs to no node, so its edges are not checked. `concordia bind validate --show-unbound` lists the unbound files.
- **What is indexed.** Code that CBM does not read gives no edges. The example excludes the Kotlin service `legacy-core-banking`, so a hidden dependency in that code stays hidden. `concordia explain CORE-DECL-001` lists the excluded files and the language coverage.
- **The confidence of heuristic edges.** The CBM adapter gives HTTP and async call edges the confidence `heuristic`. The edge from `payments-service` to `http://card-network.example.com/authorize` is such an edge, and the URL matches no binding, so it gives a `binding-gap` finding. To check these edges as dependencies, bind the external system and lower `evidence.minimumConfidence` to `heuristic` with a rule override.

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch Git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/keep-the-model-in-sync-with-code && mkdir -p /tmp/concordia-usecases/keep-the-model-in-sync-with-code && cp -R docs/examples/payments/. /tmp/concordia-usecases/keep-the-model-in-sync-with-code && cd /tmp/concordia-usecases/keep-the-model-in-sync-with-code && sed -i.bak 's/payments-example/payments-uc-keep-the-model-in-sync-with-code/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-the-model-in-sync-with-code
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check the rule. The `sed` command keeps the summary, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/keep-the-model-in-sync-with-code && concordia check --rule CORE-DECL-001 --no-color 2>/dev/null | sed -n '1p;/^FAIL/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Conformance: violation (1 violation, 11 findings)
FAIL CORE-DECL-001 Dependencies between services, databases, and web clients are declared
  Why it matters: An architecture model is only a source of truth when it shows every dependency that exists in the code. A hidden dependency is a coupling that nobody decided on.
  ledger-service -> payments-service: 2 finding(s)
    services/ledger-service/src/settlement.ts    imports services/payments-service/src/api/index.ts
    services/ledger-service/src/settlement.ts:4  calls services.payments-service.src.api.paymentStatus in services/payments-service/src/api/index.ts
  reporting-job -> ledger-db: 3 finding(s)
    jobs/reporting/src/nightly.ts    imports services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:5  calls LedgerTable.totals in services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:4  calls LedgerTable in services/ledger-service/src/db/schema.ts
  fraud-service -> notifications-service: 2 finding(s)
    services/fraud-service/src/alerts.ts:4  calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts    imports services/notifications-service/src/api/index.ts
  channels-api -> customer-profile: 3 finding(s)
    services/channels-api/src/accounts.ts:10  calls ProfileStore.load in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:9   calls ProfileStore in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts     imports services/customer-profile/src/internal/ProfileStore.ts
  payments-service: 1 finding(s)
    services/payments-service/src/PaymentService.ts:5  http_calls __route__ANY__http://card-network.example.com/authorize
  Fix: Remove the dependency from the code, or declare it as a relationship in the architecture model. Edges of kind HTTP_CALLS and ASYNC_CALLS are heuristic in the CBM adapter, so lower evidence.minimumConfidence to heuristic with a rule override to check them.
10 active error(s), 1 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

Four pairs of nodes depend on each other in the code, and the model shows none of them. The payments service also makes an HTTP call to the card network, which no binding covers. The rule has severity `error`, so `check` exits with code 1.

Ask Concordia for the evidence behind one finding. The `sed` command keeps the finding for the import in `alerts.ts`:

```bash
cd /tmp/concordia-usecases/keep-the-model-in-sync-with-code && concordia explain CORE-DECL-001 --no-color | sed -n '/^Finding afff/,/Module services.notifications-service/p'
```

```output
Finding afff…316
  Fingerprint: sha256:afffc0c1423546784525e6f8ecdba30b75ddfc84ac612800763449aa4f889316
  Kind: undeclared
  Message: undeclared dependency fraud-service -> notifications-service
  Source: calm:node:fraud-service
  Target: calm:node:notifications-service
  Owner: team-fraud
  Disposition: active
  Location: services/fraud-service/src/alerts.ts
  Evidence path:
    File services.fraud-service.src.alerts.ts.__file__ (services/fraud-service/src/alerts.ts)
    -[IMPORTS syntactic, cbm:IMPORTS]-> at services/fraud-service/src/alerts.ts
    Module services.notifications-service.src.api.index (services/notifications-service/src/api/index.ts:1)
```

The evidence path names the file, the edge kind and its confidence, and the module that the file imports.

The fraud team confirms that fraud alerts are a real dependency. Declare it in the model, commit, and index again:

```bash
cd /tmp/concordia-usecases/keep-the-model-in-sync-with-code && jq '.relationships += [{"unique-id": "fraud-to-notifications", "description": "The fraud service sends fraud alerts.", "relationship-type": {"connects": {"source": {"node": "fraud-service"}, "destination": {"node": "notifications-service"}}}}]' architecture/payments.calm.json > calm.tmp && mv calm.tmp architecture/payments.calm.json && git commit -q -am "Declare the fraud alerts" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-the-model-in-sync-with-code
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check again. The `sed` command keeps the summary, the pairs, and the totals:

```bash
cd /tmp/concordia-usecases/keep-the-model-in-sync-with-code && concordia check --rule CORE-DECL-001 --no-color 2>/dev/null | sed -n '1p;/ -> .*finding/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Conformance: violation (1 violation, 9 findings)
  ledger-service -> payments-service: 2 finding(s)
  reporting-job -> ledger-db: 3 finding(s)
  channels-api -> customer-profile: 3 finding(s)
8 active error(s), 1 active info(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

The pair `fraud-service -> notifications-service` is gone. The other three pairs still need a decision: remove the dependency from the code, or declare it. A declared dependency can still break another rule. For example, `PAY-PATH-001` still reports the path from the critical fraud service to the non-critical notifications service. See [keep critical paths isolated](keep-critical-paths-isolated.md).

## Make it block a merge

The rule fails the build as shipped. Its severity is `error`, the workspace sets `check.failOn: error`, and a rule over code edges is `mandatory` by default. Every active finding makes `check` exit with code 1.

On an existing system you may have many undeclared dependencies on the first run. Record them in a baseline, so that only new ones fail the build. See [manage baselines and waivers](../how-to/manage-baselines-and-waivers.md). To show the findings without failing the build for a while, set the mode of the rule to `advisory` under `check.enforcement`:

```yaml
check:
  enforcement:
    - rules: [CORE-DECL-001]
      mode: advisory
```

See [roll out new obligations](../how-to/roll-out-new-obligations.md).

## Related

- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Use policy packs](../how-to/use-policy-packs.md)
- [Policies reference: `declaredOnly`](../reference/policies.md#declaredonly)
- [Confidence levels](../reference/policies.md#confidence-levels)
- [Require a decision for significant changes](require-a-decision-for-significant-changes.md)
