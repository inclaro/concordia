# Enforce trust boundaries in code

The bank's channels API faces the internet. The customer profile service holds names, addresses, and identity data. If the channels API imports the profile store directly, an attacker who gets into the channels API reads customer data without passing any authorization check. The architecture diagram still shows a clean boundary, so nobody notices.

## The rule

The rule is `CORE-SEC-001` in the built-in pack `builtin:core`:

```yaml
- id: CORE-SEC-001
  name: Public and external code does not access restricted nodes directly
  severity: error
  rationale: A node tagged public or external is exposed to untrusted callers. A direct access from it to a restricted node removes the control point between them.
  remediation: Route the access through a node that checks authorization, for example a gateway or an application service, and tag that node as neither public nor external.
  evidence:
    minimumConfidence: heuristic
  subject:
    tags: [public, external]
  forbid:
    target:
      tags: [restricted]
    edges: [IMPORTS, CALLS, READS, WRITES, HTTP_CALLS]
    traversal: direct
```

In the payments example, `channels-api` and `public-load-balancer` have the tag `public`, `card-network` has the tag `external`, and `customer-profile` and `ledger-db` have the tag `restricted`.

## What Concordia reads

- **The CALM model** (`architecture/payments.calm.json`): the tags that select the subjects and the targets, and the `concordia.trust` values (`zone` and `classification`) of each node.
- **The bindings** (`bindings/concordia.bindings.yaml`): which source files belong to which CALM node.
- **The code graph** that CBM builds from the TypeScript sources: the imports and calls between files, with the line of each one.

Concordia does not use a relationship in the CALM file as evidence. A boundary that the diagram shows, but the code does not keep, is a violation.

## How Concordia decides

1. Concordia selects the subjects: every CALM node with the tag `public` or `external`. It selects the targets: every node with the tag `restricted`.
2. The bindings map each subject and each target to its source files.
3. Concordia reads the code graph for direct edges of the listed kinds from a subject file to a target file.
4. Each edge is a finding. One finding makes the rule a violation.
5. Separately, Concordia compares the `zone` values of the two ends of each declared relationship and each observed remote call, and reports the crossings in `results.json`. The crossings do not change the verdict of this rule.

## What you still declare

- **The tags.** The rule selects on `public`, `external`, and `restricted`. A node without the right tag is not checked. In this release a rule cannot select on `zone` or `classification` (see [selector dimensions](../reference/policies.md#selector-dimensions)), so the example repeats them as tags. Keep the tags and the trust values in step.
- **The trust values.** Concordia never derives a zone from a name, a namespace, or a path. A node without a `zone` value gives an `unknown` crossing.
- **The bindings.** A file that no binding maps to a node is not checked. `concordia bind validate` reports unbound files.
- **The confidence level.** The rule accepts `heuristic` edges, the lowest level. An edge that CBM cannot resolve at all does not appear.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, `git`, and `jq` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/enforce-trust-boundaries-in-code && mkdir -p /tmp/concordia-usecases/enforce-trust-boundaries-in-code
cp -R docs/examples/payments/. /tmp/concordia-usecases/enforce-trust-boundaries-in-code
cd /tmp/concordia-usecases/enforce-trust-boundaries-in-code
sed -i.bak 's/payments-example/payments-uc-enforce-trust-boundaries-in-code/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule CORE-SEC-001 --no-color 2>/dev/null | sed -n '/^FAIL/,/Fix:/p'
```

```text
FAIL CORE-SEC-001 Public and external code does not access restricted nodes directly
  Why it matters: A node tagged public or external is exposed to untrusted callers. A direct access from it to a restricted node removes the control point between them.
  Owner: team-channels
  channels-api -> customer-profile: 3 finding(s)
    services/channels-api/src/accounts.ts     imports services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:9   calls ProfileStore in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:10  calls ProfileStore.load in services/customer-profile/src/internal/ProfileStore.ts
  Fix: Route the access through a node that checks authorization, for example a gateway or an application service, and tag that node as neither public nor external.
```

`accounts.ts` in the channels API imports the internal profile store and calls it. Each import and each call is one finding.

See which nodes the tags selected:

```bash
concordia explain CORE-SEC-001 --no-color | sed -n '1,7p'
```

```text
CORE-SEC-001 Public and external code does not access restricted nodes directly
Verdict: violation   Severity: error   Family: boundary   Policy: core@1.0.0
Rationale: A node tagged public or external is exposed to untrusted callers. A direct access from it to a restricted node removes the control point between them.
Remediation: Route the access through a node that checks authorization, for example a gateway or an application service, and tag that node as neither public nor external.
Subjects: calm:node:card-network, calm:node:channels-api, calm:node:public-load-balancer
Targets: calm:node:customer-profile, calm:node:ledger-db
Plan:
```

`concordia explain` does not list zone crossings. Read them from `results.json`:

```bash
jq -r '.trust.crossings[] | select(.dimension == "zone" and .state == "crossing") | "\(.flow): \(.direction)"' build/concordia/results.json
```

```text
channels-to-payments: internet → internal
payments-to-card-network: internal → external
```

These are the declared flows that cross a zone. The import from `channels-api` to `customer-profile` is not in the list, because it is not a declared relationship or a remote call. It is code inside one process that reaches into another service. That is the access that `CORE-SEC-001` catches.

## Make it block a merge

The rule has severity `error` and no enforcement mode, so it is `mandatory`. It fails the build as shipped:

```bash
concordia check --rule CORE-SEC-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 1
```

Run `concordia check` in CI after `concordia index`. Any exit code other than 0 stops the merge. See [run Concordia in CI](../how-to/run-in-ci.md). To accept the findings that exist today and block only new ones, create a baseline (see [manage baselines and waivers](../how-to/manage-baselines-and-waivers.md)).

## Related

- [Core rules](../reference/core-rules.md): every rule of `builtin:core`.
- [Trust context](../reference/trust-context.md): dimensions, classifications, and crossings.
- [Selector dimensions](../reference/policies.md#selector-dimensions): what a rule can select on.
- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Protect database ownership](protect-database-ownership.md)
- [Integrate through published APIs](integrate-through-published-apis.md)
