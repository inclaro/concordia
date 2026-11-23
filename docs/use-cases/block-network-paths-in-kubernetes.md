# Block network paths in Kubernetes

The fraud cache holds the risk scores that decide which payments go through. During an incident, an operator adds a NetworkPolicy so that the channels namespace can reach the cache, and nobody removes it. The internet-facing channels API can now read or overwrite risk scores. The architecture diagram shows no such connection.

## The rule

The rule is `PAY-NET-001` in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-NET-001
  name: The channels API has no network path to the fraud cache
  severity: error
  owner: team-fraud
  rationale: The fraud cache holds risk scores. An internet-facing workload that can reach it can read or poison the scores that decide which payments go through.
  remediation: Remove the NetworkPolicy that admits the channels namespace to the fraud cache, or narrow it to the fraud service.
  require:
    effectiveAccess:
      dimension: network
      from: {calmNodes: [channels-api]}
      to: {calmNodes: [fraud-cache]}
      expect: denied
      ports: [6379]
```

## What Concordia reads

- **The Kubernetes manifests** under `deploy/production/`, declared in `assurance.rendering.sources` with the tool `plain`. Concordia reads the Deployments, their labels and container ports, the Namespaces, and every `networking.k8s.io/v1` NetworkPolicy. For Helm charts and Kustomize overlays, Concordia renders them itself with a pinned renderer.
- **The bindings**: `channels-api` binds the Deployment `channels/channels-api`, and `fraud-cache` binds the Deployment `fraud/fraud-cache`.
- **The workspace**: the evaluator `network-policy` and the assumption `network-policy-enforced` for `production`, under `assurance.evaluators`.

Concordia does not use the code graph or the CALM relationships for this rule. A missing relationship in the model is not isolation, and a selection edge in the code graph is not a policy decision.

## How Concordia decides

1. The bindings map `channels-api` and `fraud-cache` to their Deployments in `production`.
2. Concordia reads the rendered manifests of `production` and checks that their coverage is complete.
3. For TCP port 6379, the `network-policy` evaluator checks two directions. Does a policy that selects the channels API for egress allow the traffic? Does a policy that selects the fraud cache for ingress allow it? A workload that no policy selects for a direction is open in that direction.
4. The policies that select a workload form a union: one allow rule in any of them is enough.
5. One open port is a violation. The result names the NetworkPolicy that opened it, `fraud/allow-ops-debug`.
6. A `denied` result counts only when the workspace declares `network-policy-enforced`. Without it, the result is `unknown` with the reason `enforcement-not-declared`.

## What you still declare

- **The bindings** from CALM nodes to workloads. A node with no workload binding in the environment gives `unknown` with the reason `binding-gap`.
- **The assumption `network-policy-enforced`.** Concordia cannot see whether the network plugin of the cluster enforces NetworkPolicy. It takes your word for it.
- **The limits of the evaluator.** Concordia does not evaluate CNI-specific kinds (CiliumNetworkPolicy, CalicoNetworkPolicy, AdminNetworkPolicy), host-network pods, node traffic, service address translation, ingress controllers, or a service mesh. An `ipBlock` that may contain pod addresses gives `unknown`, because Concordia does not know the pod network of the cluster.
- **That the manifests are what you deploy.** The evidence is the committed manifests, not the cluster.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, `git`, and `jq` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/block-network-paths-in-kubernetes && mkdir -p /tmp/concordia-usecases/block-network-paths-in-kubernetes
cp -R docs/examples/payments/. /tmp/concordia-usecases/block-network-paths-in-kubernetes
cd /tmp/concordia-usecases/block-network-paths-in-kubernetes
sed -i.bak 's/payments-example/payments-uc-block-network-paths-in-kubernetes/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule PAY-NET-001 --no-color 2>/dev/null | sed -n '/^FAIL/,/Fix:/p'
```

```text
FAIL PAY-NET-001 The channels API has no network path to the fraud cache (advisory: does not change the exit code)
  Why it matters: The fraud cache holds risk scores. An internet-facing workload that can reach it can read or poison the scores that decide which payments go through.
  Owner: team-fraud
  channels-api -> fraud-cache: 1 finding(s)
    deploy/production
      open: tcp/6379 from channels/channels-api to fraud/fraud-cache (NetworkPolicy fraud/allow-ops-debug (ingress), no policy selects the workload for egress)
  Fix: Remove the NetworkPolicy that admits the channels namespace to the fraud cache, or narrow it to the fraud service.
```

The fraud namespace has a default-deny policy for ingress, but `allow-ops-debug` admits the whole `channels` namespace on port 6379. No policy limits the egress of the channels API.

Read the evaluation that decided it:

```bash
jq '.assurance.evaluations[] | {query, verdict, policies, assumptions}' build/concordia/results.json
```

```json
{
  "query": "tcp/6379 from channels/channels-api to fraud/fraud-cache",
  "verdict": "allowed",
  "policies": [
    "NetworkPolicy fraud/allow-ops-debug (ingress)"
  ],
  "assumptions": [
    "network-policy-enforced"
  ]
}
```

Each query of the evaluator is one entry in `assurance.evaluations`, with the evaluator version, the digest of the rendered input, the limits, and the gaps.

## Make it block a merge

This rule is advisory only. The `FAIL` line says `advisory: does not change the exit code`, and the run exits with 0:

```bash
concordia check --rule PAY-NET-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 0
```

The contract of the `effective-access` family is experimental in this release, so a `require.effectiveAccess` rule runs as `advisory` or `shadow`. You cannot make it `mandatory`: an entry in `check.enforcement` with `mode: mandatory` stops `check` with `CO-POL-018` and exit code 2 (see [block network paths in the cloud](block-network-paths-in-the-cloud.md#make-it-block-a-merge) for the output). Until the family is stable, report the finding in the pull request from `results.json` or the SARIF report, and review it there.

## Related

- [`require.effectiveAccess`](../reference/policies.md#requireeffectiveaccess)
- [The `network-policy` evaluator](../reference/assurance.md#network-policy)
- [`assurance.evaluators`](../reference/workspace.md#assuranceevaluators)
- [Add stronger evidence](../how-to/add-stronger-evidence.md): render Helm charts and Kustomize overlays.
- [Block network paths in the cloud](block-network-paths-in-the-cloud.md)
- [Limit Kubernetes permissions](limit-kubernetes-permissions.md)
