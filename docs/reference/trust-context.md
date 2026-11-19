# Trust context reference

Trust context tells Concordia which authority and which data classification apply to a CALM node, a CALM relationship, or a binding entry. Concordia uses it to report, for each flow, which boundaries the flow crosses, and it defines the closed list of system facts that a security assessment can depend on. This page describes the dimensions, the data classification, the crossing states, the `trust` keys, the `results.json` section, and the dependency kinds.

Concordia reads only what a team declares. It never infers a value from a name, a tag, a namespace, a network location, or a file path.

## Dimensions

A dimension is a named authority boundary. One element assigns one value to a dimension.

| Dimension | What the value names |
|---|---|
| `organization` | The organization that owns the element. |
| `account` | The cloud account, subscription, or tenant that holds the element. |
| `principal` | The identity that the element runs as or acts for. |
| `zone` | The network or trust zone, for example `internet` or `internal`. |
| `region` | The region or jurisdiction. |

These five names are built in. A team declares more names in the workspace key `trust.dimensions`:

```yaml
trust:
  dimensions: [tenant]
  classifications: [public, internal, restricted]
```

A dimension name that is neither built in nor listed in `trust.dimensions` fails validation with `CO-SEC-001` (error, exit code 2). A value is a non-empty string. Values are compared as exact strings: `internal` and `Internal` are different values.

## Data classification

A classification is one value of the workspace list `trust.classifications`. The list is yours; Concordia has no built-in levels and gives them no order.

- A node, relationship, or binding entry that omits the classification has the classification `unknown`. `unknown` is not a default and not the lowest level. Every output shows it as `unknown`.
- A value that is not in the list fails validation with `CO-SEC-001` (error, exit code 2). A workspace without `trust.classifications` accepts no classification.
- A value such as `unclassified` that your list contains is an explicit statement. It is different from `unknown`.

## Where trust is declared

The `trust` object has the same shape in all three places. Both keys are optional. The object is closed: any other key fails validation (`CO-INT-007` in CALM, `CO-BND-002` in a binding file; exit code 2).

| Key | Type | Description |
|---|---|---|
| `dimensions` | object | Dimension name to value. |
| `classification` | string | A value of `trust.classifications`. |

On a CALM node or relationship, `trust` sits in the `concordia` extension. It validates the same way under CALM 1.2 and 1.3:

```json
{
  "unique-id": "billing-service",
  "node-type": "service",
  "name": "Billing",
  "description": "Billing",
  "concordia": {
    "trust": {
      "dimensions": { "organization": "acme", "account": "a2", "zone": "internal" },
      "classification": "restricted"
    }
  }
}
```

On a relationship, the `classification` is the data classification of the flow. A crossing compares the dimension values of the two nodes of the relationship (and of the binding entries that name them); the `dimensions` of a relationship are validated and kept as part of its trust context, but they are not an end of a crossing.

On a binding entry, `trust` sits next to `architectureRef`. The values apply to the node that the entry names:

```yaml
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-payments-prod
    architectureRef: calm:node:payments-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: payments}
    trust:
      dimensions: {account: a-prod, region: eu-west-1}
```

The values of a node and the values of all binding entries that name it are merged. When two declarations give different values for one dimension, Concordia drops that dimension for the node, so its crossings are `unknown`. It does not choose a winner.

`index` and `check` never write a CALM file or a binding file.

## Crossing states

For each CALM relationship of kind `connects` or `interacts`, for each step of a declared CALM flow, and for each observed remote call that no relationship covers, Concordia derives one crossing result for each dimension. The `composed-of`, `deployed-in`, and `options` relationships are structure and give no crossing.

| State | Meaning |
|---|---|
| `crossing` | Both ends have a value and the values differ. |
| `same` | Both ends have a value and the values are equal. |
| `unknown` | At least one end has no value. |

Direction is kept from source to destination: a call from `internet` to `internal` and the reverse call are two separate crossings. An end without a value shows as `unknown` in `from` or `to`.

Two workloads in namespaces named `trusted` and `untrusted` have a `zone` result of `unknown` unless a bound CALM element declares the `zone` value. A name is never a declaration.

### Flow kinds

| `kind` | `flow` | Source and destination |
|---|---|---|
| `relationship` | The relationship `unique-id`. A relationship with several destinations (`interacts`) adds `#<destination>`. | The two nodes of the relationship. |
| `flow-step` | `<flow unique-id>/<sequence-number>`, with the same `#<destination>` suffix. | The nodes of the relationship that the transition names, swapped for `destination-to-source`. The field `relationship` names the relationship. |
| `observed` | `observed:<source>-><destination>`. | Two bound nodes, or a bound node and an external destination. |

An observed flow comes from an observed remote call (`calls-service`, `dispatches-async`, `calls-grpc`, `calls-graphql`, `calls-trpc`). A pair that a declared relationship already covers is reported once, as the relationship. When the source is bound and no binding maps the destination, the destination is an external destination: its crossings are `unknown`, and the entry carries `externalDestination: true`.

The data classification of a crossing is the classification of the relationship. A flow without one has `unknown`.

## `trust` in `results.json`

`results.json` has an optional top-level object `trust`. It is additive under `schemaVersion` `1.1`. It is left out when no node, relationship, or binding declares any trust value, so a run without trust keys has the result of before.

```json
{
  "trust": {
    "crossings": [
      {
        "flow": "web-to-api",
        "kind": "relationship",
        "source": "web",
        "destination": "api",
        "dimension": "zone",
        "from": "internet",
        "to": "internal",
        "direction": "internet → internal",
        "state": "crossing",
        "classification": "restricted"
      }
    ]
  }
}
```

| Field | Description |
|---|---|
| `flow` | The flow identity, see [Flow kinds](#flow-kinds). |
| `kind` | `relationship`, `flow-step`, or `observed`. |
| `relationship` | The relationship of a `flow-step`. Left out for the other kinds. |
| `source`, `destination` | The CALM node IDs of the two ends. For an external destination, its scoped identity, qualified name, or observed ID. |
| `dimension` | The dimension name. |
| `from`, `to` | The values of the source and the destination, or `unknown`. |
| `direction` | `from → to`. |
| `state` | `crossing`, `same`, or `unknown`. |
| `classification` | The data classification of the flow, or `unknown`. |
| `externalDestination` | `true` for an observed destination that no binding maps. Left out otherwise. |

`crossings` has one entry for each flow and dimension (the built-in and the declared dimensions). It is sorted by `flow`, then by `dimension`, in UTF-16 code unit order, so two runs on equal inputs give equal entries in equal order. The object is closed in `internal/conformance/schemas/results.schema.json`.

`concordia explain <rule-id>` shows the crossings of the flow that a finding is about (from its source node to its target node) under the finding. `explain --format json` adds the same entries as a top-level `trust` object, and leaves the key out when the selected findings have none.

## Dependency kinds

An assessment dependency names one kind from the closed list below. Any other kind, for example `firewall-rule`, fails validation (exit code 2).

| Kind | What the dependency covers |
|---|---|
| `runtime-identity` | The service account or workload identity of a workload, and its identity provider (`ref: serviceAccountName`, `subject`). |
| `role-binding` | The role and role binding that grant permissions (`ref: roleRef`). |
| `secret-reference` | A reference to a Secret (`ref: env`, target kind `Secret`): identifier, role, consuming container. Never the value. |
| `configuration-reference` | A reference to a ConfigMap or other configuration (`ref: envFrom`, target kind `ConfigMap`). |
| `storage-reference` | A volume or storage reference (`ref: volume`). |
| `public-entry-point` | An Ingress or HTTPRoute: host, path, backend, exposure class. |
| `protocol` | The protocol of a relationship or an endpoint. |
| `certificate-reference` | A reference to a certificate or TLS secret (`ref: tls`). |
| `external-destination` | An observed destination that no binding maps. |
| `trust-context` | The trust values of a node, relationship, or binding entry, and its crossings. |
| `data-classification` | The data classification of a node, relationship, or binding entry. |
| `placement` | Where a workload is deployed (environment, cluster, namespace). |
| `binding` | A binding between a CALM node and observed entities. |
| `control` | A CALM control and its configuration. |
| `implementation` | Repository files or qualified names that implement a behavior. |
| `verification` | Test or check results that verify a behavior. |
| `quality-scenario` | A quality scenario that the assessment relies on. |

### Reference mode and target kind

A reference dependency takes its reference mode from the native `ref` value and its endpoint kind from the kind of the target resource. A Secret and a ConfigMap are told apart by the target kind, not by the reference mode.

| `ref` value | Dependency kind |
|---|---|
| `serviceAccountName`, `subject` | `runtime-identity` |
| `roleRef` | `role-binding` |
| `env` to a Secret | `secret-reference` |
| `env` or `envFrom` to a ConfigMap | `configuration-reference` |
| `volume` | `storage-reference` |
| `tls` | `certificate-reference` |
| `parentRef` | Gateway attachment of a `public-entry-point`. |

The kinds `secret-reference`, `configuration-reference`, `storage-reference`, and `certificate-reference` hold identifiers and roles only. A credential, token, or secret value never enters a dependency digest, an assessment record, an approval, a result, a log, or a bundle, and each redaction is recorded.

### What makes a dependency unknown

- **Unsupported kind.** When the capability profile does not support the evidence that a kind needs, the dependency is reported unsupported with the profile identity (`CO-SEC-002`, warning), and its currency contribution is `unknown`. It is never `current`.
- **Empty match.** A selector that matches nothing under complete coverage is an empty match. It is different from unsupported, and the assessment records the empty set as part of its subject.
- **Target not declared.** A reference edge exists only when the target is declared in the indexed project. A dependency on a reference whose target is not declared is `unknown` with the reason `target-not-declared`, never an empty match.
- **Removed target.** If an approved reference dependency existed and the target declaration is removed while the reference stays in the manifest, the item is classified as removed and is a review trigger. A missing edge is not "no change".
- **Merged occurrences.** When the containers of one workload share an image repository, the image-version coverage is `partial` with the loss mode `merged-occurrences`, and the dependency is `unknown` for the lost occurrences.
- **Unmapped Terraform reference.** A Terraform `USAGE` reference (`via: terraform`) is an item only for a kind that the capability profile maps. For any other kind the dependency is unsupported.

A `public-entry-point` takes its identity from the scoped Ingress or HTTPRoute resource and keeps host and path as facets. It does not use the project-global `Route` node, because two environments with equal host and path share one `Route` node.

A dependency can name implementation evidence (repository paths or qualified names) and verification evidence (test or check results). When a named path or result changes, the dependency counts as changed even if the observed topology is the same. A named implementation path that does not exist in the revision gives a diagnostic, and the dependency currency is `unknown`.

## Related diagnostics

| Code | Severity | Meaning |
|---|---|---|
| `CO-SEC-001` | error, exit code 2 | A trust declaration is invalid: undeclared dimension, empty value, or classification outside the list. |
| `CO-SEC-002` | warning | A dependency kind is unsupported by the capability profile. |

See [Diagnostics](diagnostics.md) for the full table.

## Next steps

- [Bindings reference](bindings.md): the `trust` key of a binding entry.
- [Results reference](results.md): the `trust` object of `results.json`.
- [Workspace reference](workspace.md): the `trust` block with `dimensions` and `classifications`.
