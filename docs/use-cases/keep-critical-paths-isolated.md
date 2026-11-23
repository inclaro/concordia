# Keep critical paths isolated

A critical service must keep working when a less important service is down. A dependency from a critical service to a non-critical one, direct or through other code, lets a minor outage stop the critical service. At the bank, the fraud service sends alerts through the notifications service. If payment authorization waits for the fraud score, and the fraud score waits for an alert, a notifications outage stops payments. This rule finds every such path in the code.

## The rule

The rule is in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-PATH-001
  name: Critical services do not depend on non-critical services
  severity: error
  owner: team-architecture
  rationale: Payment authorization must keep working when a non-critical service such as notifications is down. A path from a critical service to a non-critical one, direct or through other code, lets a minor outage stop payments.
  remediation: Remove the dependency, or decouple it with a queue so that the critical service does not wait for the non-critical one.
  subject:
    tags: [critical]
  denyPath:
    target:
      tags: [non-critical]
    edges: [IMPORTS, CALLS]
    maxDepth: 4
```

The rule selects services by tag, so a new critical service is checked as soon as it has the tag.

## What Concordia reads

- **Source code.** The TypeScript files under `services/` and `jobs/`, at the committed revision. CBM (Codebase Memory MCP) parses them into a code graph, and Concordia reads the `IMPORTS` and `CALLS` edges from that graph.
- **Bindings.** `bindings/concordia.bindings.yaml` maps the source roots of the subjects and the targets to their CALM nodes.
- **Architecture.** `architecture/payments.calm.json` gives the tags. `payments-service` and `fraud-service` have the tag `critical`. `notifications-service` has the tag `non-critical`.

The CALM model does not declare a relationship from fraud to notifications. Concordia does not need one. The path comes only from the edges that it observes in the code.

## How Concordia decides

1. Concordia selects the subjects (nodes tagged `critical`) and the targets (nodes tagged `non-critical`) from the CALM file.
2. It finds the code of each subject and each target through the bindings.
3. From each code entity of a subject, it follows `IMPORTS` and `CALLS` edges at `syntactic` confidence or higher, for at most 4 edges.
4. The code between the start and the end does not need a binding. A path can pass through any code in the graph, including code of other services.
5. Each path that ends in code of a target is a finding of kind `forbidden-path`. The evidence lists every edge of the path.
6. If the graph covers fewer in-scope files than the required coverage (here 90%), the result is `unknown`, not `pass`.

## What you still declare

- **The tags.** Concordia takes the tags `critical` and `non-critical` from the CALM file on trust. A service without the tag is not checked.
- **The bindings.** The start and the end of a path must be bound to a subject and a target. Code that no binding maps to a target is not an end.
- **The depth.** A path longer than `maxDepth` (here 4 edges) is not found. The limit is 10.
- **The edge kinds and the confidence.** A dependency that CBM reports only at `heuristic` confidence, for example an HTTP call or a message on a queue, does not count. See [confidence levels](../reference/policies.md#confidence-levels).

## Try it on the example

You need `concordia`, CBM, and `git` on your `PATH`. See [install Concordia](../how-to/install.md). Run the commands from the root of a Concordia checkout.

Copy the example to a scratch git repository and index it:

```bash
rm -rf /tmp/concordia-usecases/keep-critical-paths-isolated && mkdir -p /tmp/concordia-usecases/keep-critical-paths-isolated && cp -R docs/examples/payments/. /tmp/concordia-usecases/keep-critical-paths-isolated && cd /tmp/concordia-usecases/keep-critical-paths-isolated && sed -i.bak 's/payments-example/payments-uc-keep-critical-paths-isolated/' concordia.yaml && rm concordia.yaml.bak && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && concordia index | grep -v '^generation'
```

```output
project: payments-uc-keep-critical-paths-isolated
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

Check the rule. The `sed` command keeps the summary, the rule block, and the totals:

```bash
cd /tmp/concordia-usecases/keep-critical-paths-isolated && concordia check --rule PAY-PATH-001 --no-color 2>/dev/null | sed -n '1p;/^FAIL/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
Conformance: violation (1 violation, 2 findings)
FAIL PAY-PATH-001 Critical services do not depend on non-critical services
  Why it matters: Payment authorization must keep working when a non-critical service such as notifications is down. A path from a critical service to a non-critical one, direct or through other code, lets a minor outage stop payments.
  Owner: team-architecture
  fraud-service -> notifications-service: 2 finding(s)
    services/fraud-service/src/alerts.ts    imports services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts:4  calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
  Fix: Remove the dependency, or decouple it with a queue so that the critical service does not wait for the non-critical one.
2 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

The fraud service imports the notifications API and calls `sendAlert`. Each path here is one edge long. The payments service has no path yet, because no payments code reaches `alerts.ts`.

Now make the path longer. A developer changes the risk model so that a high score raises a fraud alert. The payments service calls the risk model, so payment authorization now waits for the notifications service:

```bash
cd /tmp/concordia-usecases/keep-critical-paths-isolated && cat > services/fraud-service/src/internal/RiskModel.ts <<'EOF'
import { raiseFraudAlert } from "../alerts";

export class RiskModel {
  private readonly threshold = 500_000;

  score(paymentId: string, amountCents: number): number {
    if (paymentId.length === 0) return 1;
    const score = Math.min(1, amountCents / this.threshold);
    if (score > 0.8) raiseFraudAlert(paymentId, score);
    return score;
  }
}
EOF
git commit -q -am "Alert the fraud team on high scores" && concordia index | grep -v '^generation' && concordia check --rule PAY-PATH-001 --no-color 2>/dev/null | sed -n '1p;/^FAIL/,/^  Fix:/p;$p'; echo "exit code: ${PIPESTATUS[0]}"
```

```output
project: payments-uc-keep-critical-paths-isolated
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
Conformance: violation (1 violation, 4 findings)
FAIL PAY-PATH-001 Critical services do not depend on non-critical services
  Why it matters: Payment authorization must keep working when a non-critical service such as notifications is down. A path from a critical service to a non-critical one, direct or through other code, lets a minor outage stop payments.
  Owner: team-architecture
  fraud-service -> notifications-service: 3 finding(s)
    services/fraud-service/src/internal/RiskModel.ts:9  calls raiseFraudAlert in services/fraud-service/src/alerts.ts
                                                        then raiseFraudAlert calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts                imports services/notifications-service/src/api/index.ts
    services/fraud-service/src/alerts.ts:4              calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
  payments-service -> notifications-service: 1 finding(s)
    services/payments-service/src/PaymentService.ts:7  calls RiskModel.score in services/fraud-service/src/internal/RiskModel.ts
                                                       then RiskModel.score calls raiseFraudAlert in services/fraud-service/src/alerts.ts
                                                       then raiseFraudAlert calls services.notifications-service.src.api.sendAlert in services/notifications-service/src/api/index.ts
  Fix: Remove the dependency, or decouple it with a queue so that the critical service does not wait for the non-critical one.
4 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown
exit code: 1
```

The new finding for `payments-service` shows the whole path: payments calls the risk model, the risk model calls `raiseFraudAlert`, and `raiseFraudAlert` calls `sendAlert` in the notifications service. No single file shows this dependency. The path crosses two services before it reaches the target.

See the evidence of the payments path, with the confidence of each edge:

```bash
cd /tmp/concordia-usecases/keep-critical-paths-isolated && concordia explain PAY-PATH-001 --no-color | sed -n '1,2p;/^Finding abb4/,/sendAlert (/p'
```

```output
PAY-PATH-001 Critical services do not depend on non-critical services
Verdict: violation   Severity: error   Family: deny-path   Policy: payments-architecture@1.0.0
Finding abb4…694
  Fingerprint: sha256:abb4459f1899f4b50ff8a8370192789ff2e11e1d5a733afea86d0299c8f02694
  Kind: forbidden-path
  Message: forbidden path payments-service -> notifications-service (3 hops)
  Source: calm:node:payments-service
  Target: calm:node:notifications-service
  Owner: team-payments
  Disposition: active
  Location: services/payments-service/src/PaymentService.ts:7
  Evidence path:
    Method services.payments-service.src.PaymentService.PaymentService.authorize (services/payments-service/src/PaymentService.ts:5)
    -[CALLS resolved, cbm:CALLS:lsp_ts_method]-> at services/payments-service/src/PaymentService.ts:7
    Method services.fraud-service.src.internal.RiskModel.RiskModel.score (services/fraud-service/src/internal/RiskModel.ts:6)
    -[CALLS resolved, cbm:CALLS:lsp_ts_import]-> at services/fraud-service/src/internal/RiskModel.ts:9
    Function services.fraud-service.src.alerts.raiseFraudAlert (services/fraud-service/src/alerts.ts:3)
    -[CALLS syntactic, cbm:CALLS:unique_name]-> at services/fraud-service/src/alerts.ts:4
    Function services.notifications-service.src.api.sendAlert (services/notifications-service/src/api/index.ts:1)
```

The finding is owned by `team-payments`, because the path starts in their service. The path has three hops, within the `maxDepth` of 4.

## Make it block a merge

The rule fails the build as shipped. Its severity is `error`, and a rule that is not a typed rule gates as `mandatory` by default. With `check.failOn: error`, `concordia check` exits with code 1 when a path is active. Run `concordia index` and then `concordia check` in CI; see [run Concordia in CI](../how-to/run-in-ci.md). In the example above, the commit that adds the alert would fail CI with the new payments finding.

The workspace sets `check.unknown: fail`. When coverage falls below 90%, the rule is `unknown` and `check` exits with code 4.

If your code already has such paths, record them in a baseline. Then only a new path blocks a merge. See [manage baselines and waivers](../how-to/manage-baselines-and-waivers.md).

## Related

- [`denyPath` in the policies reference](../reference/policies.md#denypath)
- [Selectors](../reference/policies.md#selectors)
- [Write policies](../how-to/write-policies.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
- [Integrate through published APIs](integrate-through-published-apis.md)
- [Keep services free of cycles](keep-services-free-of-cycles.md)
