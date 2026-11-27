# Limit Kubernetes permissions

The channels API faces the internet. Someone gives its service account a role in the fraud namespace, so that a deploy script can restart the fraud cache. Now an attacker who takes over the channels API can patch the fraud cache Deployment and run their own image in place of the cache that decides which payments go through.

## The rule

The rule is `PAY-RBAC-001` in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-RBAC-001
  name: The channels API cannot change the fraud cache
  severity: error
  owner: team-fraud
  rationale: A service account of an internet-facing workload that can patch a deployment in the fraud namespace can replace the fraud cache with its own image.
  remediation: Remove the RoleBinding that grants the channels-api service account a role in the fraud namespace.
  require:
    effectiveAccess:
      dimension: authorization
      from: {calmNodes: [channels-api]}
      to: {calmNodes: [fraud-cache]}
      expect: not-granted
```

## What Concordia reads

- **The Kubernetes manifests** under `deploy/production/`, declared in `assurance.rendering.sources`. Concordia reads the ServiceAccount of each Deployment, and every Role, ClusterRole, RoleBinding, and ClusterRoleBinding.
- **The bindings**: `channels-api` binds the Deployment `channels/channels-api`, and `fraud-cache` binds the Deployment `fraud/fraud-cache`.
- **The workspace**: the evaluator `rbac` and the assumption `rbac-authorizer-enabled` for `production`, under `assurance.evaluators`.

Concordia does not use the code graph or the CALM model for the result. A role reference in the code graph is not a grant.

## How Concordia decides

1. The bindings map `channels-api` and `fraud-cache` to their Deployments in `production`.
2. The subject is the ServiceAccount of the `channels-api` Deployment: `channels/channels-api`. The object is the Deployment `fraud/fraud-cache`.
3. The `rbac` evaluator asks, for each of the verbs `get`, `list`, `watch`, `create`, `update`, `patch`, and `delete`, whether a binding grants the verb on that Deployment to that ServiceAccount.
4. Wildcards, `resourceNames`, and ClusterRole aggregation count. A binding that refers to a role that is not in the manifests makes the answer `unknown`, unless another binding grants the verb.
5. One granted verb is a violation. The result names the RoleBinding and the Role.
6. A `not-granted` result counts only when the workspace declares `rbac-authorizer-enabled`. Without it, the result is `unknown` with the reason `enforcement-not-declared`.

## What you still declare

- **The bindings** from CALM nodes to workloads. A node with no workload binding in the environment gives `unknown` with the reason `binding-gap`.
- **The assumption `rbac-authorizer-enabled`.** Concordia cannot see whether the API server uses the RBAC authorizer.
- **The limits of the evaluator.** Concordia does not evaluate cloud IAM, admission control, or webhook authorizers. It does not know which users are in which groups. The ClusterRoles that the cluster provides (`cluster-admin`, `admin`, `edit`, `view`) are missing targets when your manifests do not contain them.
- **That the manifests are what you deploy.** A RoleBinding that someone made with `kubectl` is not in the evidence.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, `git`, and `jq` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/limit-kubernetes-permissions && mkdir -p /tmp/concordia-usecases/limit-kubernetes-permissions
cp -R docs/examples/payments/. /tmp/concordia-usecases/limit-kubernetes-permissions
cd /tmp/concordia-usecases/limit-kubernetes-permissions
sed -i.bak 's/payments-example/payments-uc-limit-kubernetes-permissions/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule PAY-RBAC-001 --no-color 2>/dev/null | sed -n '/^FAIL/,/Fix:/p'
```

```text
FAIL PAY-RBAC-001 The channels API cannot change the fraud cache (advisory: does not change the exit code)
  Why it matters: A service account of an internet-facing workload that can patch a deployment in the fraud namespace can replace the fraud cache with its own image.
  Owner: team-fraud
  channels-api -> fraud-cache: 1 finding(s)
    deploy/production
      open: get deployments/fraud-cache in fraud for ServiceAccount channels/channels-api (RoleBinding fraud/channels-api-deployment-editor grants Role fraud/deployment-editor), patch deployments/fraud-cache in fraud for ServiceAccount channels/channels-api (RoleBinding fraud/channels-api-deployment-editor grants Role fraud/deployment-editor)
  Fix: Remove the RoleBinding that grants the channels-api service account a role in the fraud namespace.
```

The Role `fraud/deployment-editor` allows `get` and `patch` on Deployments, and the RoleBinding gives it to the channels API service account.

Read the queries that granted access:

```bash
jq '.assurance.evaluations[] | select(.verdict == "granted") | {query, policies, assumptions}' build/concordia/results.json
```

```json
{
  "query": "get deployments/fraud-cache in fraud for ServiceAccount channels/channels-api",
  "policies": [
    "RoleBinding fraud/channels-api-deployment-editor grants Role fraud/deployment-editor"
  ],
  "assumptions": [
    "rbac-authorizer-enabled"
  ]
}
{
  "query": "patch deployments/fraud-cache in fraud for ServiceAccount channels/channels-api",
  "policies": [
    "RoleBinding fraud/channels-api-deployment-editor grants Role fraud/deployment-editor"
  ],
  "assumptions": [
    "rbac-authorizer-enabled"
  ]
}
```

The other five verbs are `not-granted`. Remove the `select` to see them.

## Make it block a merge

This rule is advisory only. The `FAIL` line says `advisory: does not change the exit code`, and the run exits with 0:

```bash
concordia check --rule PAY-RBAC-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 0
```

The contract of the `effective-access` family is experimental in this release, so a `require.effectiveAccess` rule runs as `advisory` or `shadow`. You cannot make it `mandatory`: an entry in `check.enforcement` with `mode: mandatory` stops `check` with `CO-POL-018` and exit code 2 (see [block network paths in the cloud](block-network-paths-in-the-cloud.md#make-it-block-a-merge) for the output). Until the family is stable, report the finding in the pull request from `results.json` or the SARIF report, and review it there.

## Related

- [The `rbac` evaluator](../reference/assurance.md#rbac)
- [`require.effectiveAccess`](../reference/policies.md#requireeffectiveaccess)
- [`assurance.evaluators`](../reference/workspace.md#assuranceevaluators)
- [Block network paths in Kubernetes](block-network-paths-in-kubernetes.md)
