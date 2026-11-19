# Write policies

This guide shows you how to write rules in a policy pack, one for each predicate, and how to tie a rule to a CALM control.

## Prerequisites

- A [workspace](set-up-a-workspace.md) with [bindings](bind-code-to-architecture.md) for the nodes your rules mention.
- A policy file that matches `policies.files` (the default glob is `policies/**/*.policy.yaml`).

## Start a policy pack

Every file is a `PolicyPack`. Each rule needs an `id` (uppercase segments joined by `-`, such as `CO-BND-001`), a `name`, a `severity` (`error`, `warning`, or `info`), and exactly one predicate. Optional fields are `rationale`, `remediation`, and `owner`.

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: my-boundaries
  version: 1.0.0
rules: []
```

Every rule except `layerOrder` needs a `subject`. A selector matches the union of `calmNodes` (node IDs), `nodeTypes`, and `tags` (from the node's `concordia.tags`).

Edge lists use tokens such as `IMPORTS` and `CALLS`. See [policies reference](../reference/policies.md) for all tokens.

## Forbid a dependency, with exceptions

`forbid` reports an edge from the subject to the target. `allow` removes targets from that set, here everything under a contract package:

```yaml
- id: CO-BND-001
  name: Billing uses Fulfillment contracts only
  severity: error
  subject:
    calmNodes: [billing-service]
  forbid:
    target:
      calmNodes: [fulfillment-service]
    edges: [IMPORTS, CALLS]
    traversal: direct
  allow:
    target:
      sourceRoots: [packages/fulfillment-contract/**]
```

`traversal` accepts only `direct`. `allow` is valid only next to `forbid`; its `target` takes `sourceRoots` or `calmNodes`, and `when.any` takes entries with `edge` or `targetTag`.

## Enforce a layer order

`layerOrder` lists layers from top to bottom. A lower layer must not depend on a higher one. It takes no `subject`:

```yaml
- id: CO-LYR-001
  name: Billing sits above Fulfillment
  severity: error
  layerOrder:
    layers:
      - calmNodes: [billing-service]
      - calmNodes: [fulfillment-service]
    edges: [IMPORTS]
```

## Allow only declared dependencies

`declaredOnly` reports an observed edge between two subject nodes when the architecture declares no relationship that permits it:

```yaml
- id: CO-DECL-001
  name: Dependencies are declared
  severity: error
  subject:
    nodeTypes: [service, database]
  declaredOnly:
    edges: [IMPORTS, CALLS]
```

## Require declared relationships to exist in code

`observedRequired` reports a declared relationship that has no observed edge. It checks only `connects` and `interacts` relationships that set `concordia.observation.required: true`. The rule takes no edges: the relationship's own `allowed-edge-kinds` apply.

```yaml
- id: CO-REL-001
  name: Declared integrations exist in code
  severity: warning
  subject:
    nodeTypes: [service]
  observedRequired: {}
```

## Require an acyclic graph

`require` with `graphProperty: acyclic` reports dependency cycles between CALM nodes:

```yaml
- id: CO-CYC-001
  name: Services are acyclic
  severity: error
  subject:
    nodeTypes: [service]
  require:
    graphProperty: acyclic
    edges: [IMPORTS, CALLS]
    collapseBy: architectureBinding
```

## Reuse selectors

Define named selectors at the top of the pack and refer to them with `ref`:

```yaml
selectors:
  payments-side:
    calmNodes: [billing-service, ledger-service]
rules:
  - id: CO-DECL-002
    name: Payments dependencies are declared
    severity: error
    subject:
      ref: payments-side
    declaredOnly:
      edges: [CALLS]
```

An unknown or circular `ref` fails with `CO-POL-002`.

## Tune the evidence a rule needs

Add an `evidence` block to override the defaults for one rule:

```yaml
evidence:
  minimumConfidence: resolved
  minimumCoverage: 0.9
  onUnknown: fail
```

`minimumConfidence` is one of `unknown`, `heuristic`, `syntactic`, `runtime`, `resolved`. `onUnknown` is `fail`, `warn`, or `ignore`. For `minimumCoverage` and `onUnknown`, when several sources set a value, the CLI wins over the control, then the rule, then the workspace, then the default. Only the rule `evidence` block and the default set `minimumConfidence`. See [resolve unknown verdicts](resolve-unknown-verdicts.md).

## Require a deployment property with a typed rule

A typed rule says what a subject needs, forbids, or allows in terms of one kind of evidence, for each environment. It needs declared [environments](../reference/workspace.md) and [bindings](bind-code-to-architecture.md) that map workloads to the subject. This rule requires that every workload of the billing service uses an image in production:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: deployment-pack
  version: 1.0.0
rules:
  - id: DEP-IMG-001
    name: Every billing workload uses an image
    severity: error
    family: deployment
    enforcement: advisory
    subject:
      calmNodes: [billing-service]
    obligation:
      type: required
      relation:
        kind: uses-image
        direction: from-workload
      scope:
        environments: [production]
      multiplicity: forAll
    evidence:
      roles: [parsed-source-fact, runtime-observation]
      minimumResolution: explicitly-bound
      completeness: complete
```

`type` is `required`, `forbidden`, or `allowed`. `direction` says which side of the fact the workload is on: `from-workload` for a fact that leaves it, such as `uses-image`. `forAll` (the default) needs the property on every workload of the service; one replica does not vouch for the others. `exists` needs one. The `evidence` keys `roles`, `minimumResolution`, and `completeness` are valid only in a rule with an `obligation` block, and they have the defaults in the example.

Run `concordia policy verify` to see the declaration of the rule. A typed rule is `advisory` unless it says `mandatory`: its verdicts and findings are in the results and do not block the run. Move a family to `mandatory` after you have read the `unknown` results (see [roll out new obligations](roll-out-new-obligations.md)). A family whose maturity is `advisory` cannot be made `mandatory`: `check` stops with `CO-POL-018`. If a verdict is `unknown`, the result names one reason, for example `binding-gap`. See [the reasons for `unknown`](../explanation/verdicts-and-coverage.md#the-reasons-for-unknown).

Use `applies` to limit a rule to some occurrences. It takes selector dimensions and the operators `allOf`, `anyOf`, and `not`. This rule forbids administrative connections to the ledger and leaves HTTPS to other rules:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: connection-pack
  version: 1.0.0
rules:
  - id: DEP-CONN-001
    name: No administrative connections to the ledger
    severity: error
    subject:
      calmNodes: [billing-service]
    obligation:
      type: forbidden
      relation:
        kind: calls-service
        direction: from-subject
        target:
          calmNodes: [ledger-service]
      applies:
        allOf:
          - protocol: administrative
          - not:
              environment: staging
```

If two rules contradict each other for one subject, environment, and relation (one requires or allows it and one forbids it), `check` stops with `CO-POL-017`. Resolve it with a pack-level `precedence` list; the rule listed first wins and the other stays visible as superseded:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: routes-pack
  version: 1.0.0
precedence:
  version: 1
  order: [DEP-ROUTE-002, DEP-ROUTE-001]
rules:
  - id: DEP-ROUTE-001
    name: Billing has an ingress route
    severity: warning
    subject:
      calmNodes: [billing-service]
    obligation:
      type: required
      relation:
        kind: routes-to
        direction: to-subject
  - id: DEP-ROUTE-002
    name: No ingress route to billing in production
    severity: error
    subject:
      calmNodes: [billing-service]
    obligation:
      type: forbidden
      relation:
        kind: routes-to
        direction: to-subject
      scope:
        environments: [production]
```

## Require evidence from rendered manifests and checks

Three `require` forms read the evidence of the [assurance adapters](../reference/assurance.md) and not the code graph. They need the workspace key `assurance` (see [add stronger evidence](add-stronger-evidence.md)). Without it, the rule is `unknown`, never a pass. A rule of these forms has no `subject`, and it cannot be `mandatory`: use `advisory` or `shadow`.

To require that the environment denies a network path, ask the evaluator with `effectiveAccess`:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: assurance-pack
  version: 1.0.0
rules:
  - id: ORG-NET-001
    name: The public gateway has no network path to the database
    severity: error
    require:
      effectiveAccess:
        dimension: network
        from: { calmNodes: [public-gateway] }
        to: { calmNodes: [orders-db] }
        expect: denied
        ports: [5432]
```

A `denied` answer holds only when you declare the enforcement that it assumes under `assurance.evaluators[].assumes`. Otherwise the result is `unknown` with the reason `enforcement-not-declared`.

To require a passing check from your pipeline, use `checkResult`. The check ID must come from a control `check-ids` list or from a result source of the workspace:

```yaml
  - id: ORG-VER-001
    name: The unit tests pass in production
    severity: error
    require:
      checkResult:
        check: unit-tests
        environment: production
```

To require a claim of the claim registry, such as complete rendered evidence for each environment, use `claim`:

```yaml
  - id: ORG-ASR-001
    name: Each environment has complete rendered evidence
    severity: warning
    family: assurance
    require:
      claim:
        id: complete-resource-set
```

Every field of the three forms is in the [policies reference](../reference/policies.md#assurance-rules-requireeffectiveaccess-requirecheckresult-and-requireclaim). To use the ready-made claim rules, enable `builtin:assurance`; see [use policy packs](use-policy-packs.md#choose-a-built-in-pack).

## Link a rule to a CALM control

On a CALM node, add a control whose requirement `config` lists the rules:

```json
"controls": {
  "architecture-001": {
    "description": "Billing may use fulfillment only through its contract.",
    "requirements": [
      {
        "requirement-url": "https://example.org/concordia/controls/dependency-boundary/1.0/requirement.json",
        "config": {
          "control-id": "architecture-001",
          "engine": "concordia",
          "policy-ref": "../policies/boundaries.policy.yaml",
          "rule-ids": ["CO-BND-001"],
          "unknown-disposition": "fail",
          "minimum-policy-coverage": 0.9
        }
      }
    ]
  }
}
```

`engine` must be `concordia` and `rule-ids` is required. An unknown rule ID fails with `CO-POL-005`. Run only a control's rules with `concordia check --control architecture-001`.

## Check one rule

```bash
concordia check --rule CO-BND-001
```

`--rule` is repeatable. An unknown rule ID fails with `CO-WRK-010`. For the whole pack, run `concordia check`; the exit code is 1 when an active finding meets `check.failOn`.

## Next steps

- [Resolve unknown verdicts](resolve-unknown-verdicts.md)
- [Manage baselines and waivers](manage-baselines-and-waivers.md)
- [Policy pack reference](../reference/policies.md)
