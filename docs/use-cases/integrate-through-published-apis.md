# Integrate through published APIs

A team that calls the internal classes of another service depends on code that the other team can change at any time. At the bank, the payments service called the fraud team's risk model class directly. The fraud team refactored the risk model, and payment authorization stopped in production. This rule makes sure that payments uses only the published fraud API.

## The rule

The rule is in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-API-001
  name: Payments uses only the published fraud API
  severity: error
  owner: team-payments
  rationale: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
  remediation: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.
  subject:
    calmNodes: [payments-service]
  publicApiOnly:
    target:
      calmNodes: [fraud-service]
    api:
      sourceRoots: [services/fraud-service/src/api/**]
    edges: [IMPORTS, CALLS]
```

The `api.sourceRoots` globs list the files that form the published API of the fraud service. Every other file of the fraud service is internal.

## What Concordia reads

- **Source code.** The TypeScript files under `services/`, at the committed revision. CBM (Codebase Memory MCP) parses them into a code graph, and Concordia reads the `IMPORTS` and `CALLS` edges from that graph.
- **Bindings.** `bindings/concordia.bindings.yaml` maps `services/payments-service/**` to the CALM node `payments-service` and `services/fraud-service/**` to `fraud-service`.
- **Architecture.** `architecture/payments.calm.json` names the nodes that the rule selects.

The CALM relationship `payments-to-fraud` says that payments uses fraud. Concordia does not use it for the result. The verdict comes only from the edges that it observes in the code.

## How Concordia decides

1. Concordia resolves the subject and the target through the bindings: the files under `services/payments-service/` belong to `payments-service`, and the files under `services/fraud-service/` belong to `fraud-service`.
2. It reads every `IMPORTS` and `CALLS` edge that starts in a file of `payments-service` and ends in a file of `fraud-service`.
3. It drops edges below the minimum confidence of the rule. The default is `syntactic`.
4. It compares the end file of each edge with the `api.sourceRoots` globs. An edge into `services/fraud-service/src/api/**` is allowed.
5. Each edge into any other fraud file is a finding of kind `non-public-api`. One finding makes the rule a violation.
6. If the graph covers fewer in-scope files than the required coverage (here 90%), the result is `unknown`, not `pass`.

## What you still declare

- **The published API.** You list the API files in `api.sourceRoots`. Concordia does not know which files a team intends to keep stable. If the glob is too wide, internal files count as API and calls to them pass.
- **The bindings.** A file that no binding maps to `fraud-service` is not part of the target. A call into that file is not checked.
- **The edge kinds and the confidence.** The rule counts `IMPORTS` and `CALLS` at `syntactic` confidence or higher. A call that CBM reports only at `heuristic` confidence, for example an HTTP call to an internal endpoint, does not give a finding. See [confidence levels](../reference/policies.md#confidence-levels).
- **The scope of observation.** CBM sees only the files that `observation.projects[].include` selects. Code outside it is not checked.

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/integrate-through-published-apis && mkdir -p /tmp/concordia-usecases/integrate-through-published-apis && cp -R docs/examples/payments/. /tmp/concordia-usecases/integrate-through-published-apis && cd /tmp/concordia-usecases/integrate-through-published-apis && sed -i.bak 's/payments-example/payments-uc-integrate-through-published-apis/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-integrate-through-published-apis
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

The workspace excludes the legacy Kotlin file, so 14 of 15 files are covered.

Check the rule. The `sed` command keeps the summary, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/integrate-through-published-apis && concordia check --rule PAY-API-001 --no-color 2>/dev/null | sed -n '1p;/^FAIL/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Conformance: violation (1 violation, 3 findings)
FAIL PAY-API-001 Payments uses only the published fraud API
  Why it matters: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
  Owner: team-payments
  payments-service -> fraud-service: 3 finding(s)
    services/payments-service/src/PaymentService.ts    imports services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:6  calls RiskModel in services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:7  calls RiskModel.score in services/fraud-service/src/internal/RiskModel.ts
  Fix: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.
3 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

`PaymentService.ts` imports the risk model and calls it twice. Each edge is one finding. The `ledger-service` import in the same file is not a finding, because the rule only checks the fraud service.

See the evidence for one finding:

```bash
cd /tmp/concordia-usecases/integrate-through-published-apis && concordia explain PAY-API-001 --no-color | sed -n '1,2p;/^Coverage:/,/^  files:/p;/^Finding 7cbf/,/RiskModel.score (/p'
```

```output
PAY-API-001 Payments uses only the published fraud API
Verdict: violation   Severity: error   Family: public-api-only   Policy: payments-architecture@1.0.0
Coverage:
  files: 100.0% (5/5 covered, required 90.0%)
Finding 7cbf…014
  Fingerprint: sha256:7cbf10f219ab2f3fa0f4e54dd4632aa56b4cdf66a7d31a225d57a65ca18b4014
  Kind: non-public-api
  Message: payments-service uses fraud-service outside its public API: services/fraud-service/src/internal/RiskModel.ts
  Source: calm:node:payments-service
  Target: calm:node:fraud-service
  Owner: team-payments
  Disposition: active
  Location: services/payments-service/src/PaymentService.ts:7
  Evidence path:
    Method services.payments-service.src.PaymentService.PaymentService.authorize (services/payments-service/src/PaymentService.ts:5)
    -[CALLS resolved, cbm:CALLS:lsp_ts_method]-> at services/payments-service/src/PaymentService.ts:7
    Method services.fraud-service.src.internal.RiskModel.RiskModel.score (services/fraud-service/src/internal/RiskModel.ts:4)
```

The evidence path names the calling method, the edge with its confidence, and the method that it calls. All five files in the scope of the rule are in the graph, so the coverage is enough for a verdict. The fingerprint identifies the finding in a baseline or a waiver.

## Make it block a merge

The rule fails the build as shipped. Its severity is `error`, and a rule that is not a typed rule gates as `mandatory` by default. With `check.failOn: error`, `concordia check` exits with code 1 when a finding is active. Run `concordia index` and then `concordia check` in CI; see [run Concordia in CI](../how-to/run-in-ci.md).

The workspace sets `check.unknown: fail`. When coverage falls below 90%, the rule is `unknown` and `check` exits with code 4.

If you adopt the rule on code that already has findings, record them in a baseline. Then only new calls into the internals block a merge. See [manage baselines and waivers](../how-to/manage-baselines-and-waivers.md).

## Related

- [`publicApiOnly` in the policies reference](../reference/policies.md#publicapionly)
- [Write policies](../how-to/write-policies.md)
- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
- [Keep services free of cycles](keep-services-free-of-cycles.md)
- [Keep critical paths isolated](keep-critical-paths-isolated.md)
