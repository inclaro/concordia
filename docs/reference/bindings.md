# Bindings reference

A binding connects an architecture node (CALM) to the observed code entities in the graph. This page describes the `BindingSet` file format, the match kinds, how Concordia chooses between competing bindings, and how CALM-embedded bindings convert.

For a task-oriented guide, see [Bind code to architecture](../how-to/bind-code-to-architecture.md). For the concept, see [How Concordia works](../explanation/how-concordia-works.md).

## BindingSet file

Binding files are listed in the workspace key `bindings.files` (see [Workspace reference](workspace.md)). Each file is YAML validated against `internal/binding/schemas/binding.schema.json`. Unknown keys are rejected.

```yaml
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-fulfillment
    architectureRef: calm:node:fulfillment-service
    exclusive: true
    required: true
    match:
      any:
        - sourceRoot: services/fulfillment/**
        - sourceRoot: packages/fulfillment-contract/**
    projection:
      c4Level: container
```

| Field | Type | Default | Description |
|---|---|---|---|
| `apiVersion` | const | required | `concordia.io/v1alpha1`. |
| `kind` | const | required | `BindingSet`. |
| `bindings` | array | required | The bindings. |
| `bindings[].id` | string | required | Unique binding ID (non-empty). A duplicate ID in the workspace gives `CO-BND-006`. |
| `bindings[].architectureRef` | string | required | `calm:node:<id>`. The ID pattern is `[A-Za-z0-9._-]+`. An ID that is not in the architecture gives `CO-BND-001`. |
| `bindings[].exclusive` | boolean | `true` | When `true`, two different CALM nodes at the same top specificity for one entity are a conflict (`CO-BND-003`). |
| `bindings[].required` | boolean | `true` | When `true`, a binding that matches zero observed entities gives `CO-BND-004`. |
| `bindings[].cardinality` | `one`, `many` | `many` | With `one`, matching more than one entity in one environment gives `CO-BND-008`. See [Cardinality and agreement](#cardinality-and-agreement). |
| `bindings[].agreement` | `all`, `any` | `all` | Whether all bound entities or any one must satisfy an obligation. |
| `bindings[].status` | `active`, `proposed` | `active` | A `proposed` binding is not used in resolution. |
| `bindings[].authority` | string | none | Who approved the binding. Recorded, not verified. |
| `bindings[].match.any` | array | required, at least 1 item | Match items. A binding matches an entity when any item matches. |
| `bindings[].projection.c4Level` | enum | none | `person`, `softwareSystem`, `container`, or `component`. |
| `bindings[].trust` | object | none | The trust context of the node that the binding names; see [Trust context](#trust-context). |

## Match kinds

Each `match.any` item has one of two forms. The short form has exactly one key, named after the match kind (`sourceRoot: services/billing/**`). The long form has `kind` (`sourceRoot`, `package`, `qualifiedName`, `buildTarget`, or `otelService`) and `value` (`{kind: sourceRoot, value: services/billing/**}`); both forms have the same meaning. The deployed-resource kinds use `kind` with their own identity fields (see [Deployed-resource kinds](#deployed-resource-kinds)).

| Key | Value | Matches |
|---|---|---|
| `sourceRoot` | Relative POSIX glob, for example `services/billing/**` | Observed entities whose file path matches the glob. Dotfiles are included. Entities without a file path never match. A value that is not a relative POSIX glob gives `CO-BND-002`. A leading `@<name>/` limits the glob to the workspace project `<name>`; an unknown project gives `CO-BND-002`. |
| `package` | Dotted name. A trailing `..` is optional, for example `com.acme..` | Entities whose dotted qualified name contains the name segments as a contiguous run. |
| `qualifiedName` | Exact string | Entities whose qualified name equals the value. The CBM project prefix is removed from qualified names before matching. |
| `buildTarget` | Label `//path`, `//path:name` or `//path/...` | Files of the Bazel or Buck package (a directory with `BUILD`, `BUILD.bazel` or `BUCK`). `//path` matches the package itself, `//path/...` also its sub-packages, `//...` all packages. The target name is checked but not used. |
| `otelService` | string | The runtime service of that `service.name` in the traces of `observation.runtime`. No other kind binds a runtime service. |

### Deployed-resource kinds

A deployed-resource match is a mapping with `kind` and identity fields (in a binding file, an item of `match.any`; unknown keys and a missing required field give `CO-BND-002`). Names are the field names of the schema.

| `kind` | Required fields | Optional fields | Matches |
|---|---|---|---|
| `workload` | `environment`, `resourceKind`, `name` | `cluster`, `apiGroup`, `namespace` | The Kubernetes resource with exactly this scoped identity. An optional field that is not set matches any value. |
| `route` | `environment`, `name` | `cluster`, `apiGroup`, `resourceKind`, `namespace` | An Ingress or Gateway API route resource (the `resourceKind` limits it). A `Route` node of the provider has no environment and is never matched. |
| `infraResource` | `environment`, `address` | `cluster` | The resource whose provider key `<group>/<Kind>/<namespace>/<name>` equals `address`, for example `core/Namespace/_/payments`. A namespace is a hosting target: bind it to a CALM container node and placement obligations use that node. A resource with an `address` property or qualified name equal to `address` matches too. |
| `composeService` | `file`, `service` | `environment` | The Compose service of that name in that Compose file. |
| `image` | `repository` | `digest`, `environment` | The `Image` node or entity of the repository (the tag and digest of the text are ignored). With `digest`, only the entity of that immutable digest, which `check` makes from the `image` reference of each occurrence edge. One digest used in two environments gives two entities, so a binding for one environment never binds the other. |

Environment scope: `workload`, `route`, and `infraResource` name an `environment` that the workspace declares (see [Workspace reference](workspace.md)); an undeclared name gives `CO-BND-002`. A match with an `environment` never matches an entity of another environment, an entity whose environment is `unresolved`, or an entity with no environment (for example a provider `Image` node). A `cluster` matches only entities whose environment declares that cluster. The deployed-resource kinds also match the synthesized entities of a run (images), not only the nodes of the graph.

## Specificity

For each entity, Concordia takes the best-scoring item of each binding, then keeps only the bindings at the highest specificity. Specificity is a pair `(tier, length)`, compared tier first.

| Tier | Match kind | Length |
|---|---|---|
| 5 | `otelService` | Number of characters in the value. |
| 4 | `qualifiedName` | Number of characters in the value. |
| 3 | `package` | Number of name segments. |
| 2 | `buildTarget` | `len(package) + 1` for `//pkg` and `//pkg:name`; `len(package)` for a recursive label `//pkg/...`. |
| 1 | `sourceRoot` | Number of characters before the first glob character (`*`, `?`, `[`, `{`). |

The deployed-resource tiers are appended above the as-built tiers, so the order of the existing kinds does not change:

| Tier | Match kind | Length |
|---|---|---|
| 11 | `image` with `digest` | Number of set fields (`environment`). |
| 10 | `workload` | Number of optional fields set (`cluster`, `apiGroup`, `namespace`, `resourceKind`). |
| 9 | `infraResource` | `1` with a `cluster`, else `0`. |
| 8 | `route` | Number of optional fields set. |
| 7 | `composeService` | `1` with an `environment`, else `0`. |
| 6 | `image` with only `repository` | `1` with an `environment`, else `0`. |

Resolution is deterministic: the order of bindings has no effect.

| Outcome for one entity | Result |
|---|---|
| No binding matches | Unbound. In-scope source files with no bound entity are counted by `bind validate` and listed as `CO-BND-005` warnings with `--show-unbound`. |
| One CALM node at the top specificity | The entity binds to that node. |
| Several bindings at the top specificity, all for the same CALM node | The entity binds to that node. |
| Several CALM nodes at the top specificity, at least one binding `exclusive` | Conflict (`CO-BND-003`). The entity is left unbound. |
| Several CALM nodes at the top specificity, none `exclusive` | The entity binds to all of those nodes. |

### Ambiguous bindings

Non-exclusive bindings at the same top specificity that name different CALM nodes keep the entity bound to all of them, but Concordia never picks one by order of declaration. The entity is listed in `Set.Ambiguous` with the binding IDs, `CO-BND-009` is a warning for each group of such entities, and the obligations that depend on the entity are `unknown` with the reason `ambiguous-binding`.

### Cardinality and agreement

| Field | Values | Effect |
|---|---|---|
| `cardinality` | `many` (default), `one` | With `one`, a binding that matches more than one entity in one environment is an error (`CO-BND-008`, exit code 2) that lists the entities. An entity is a scoped resource identity, an image repository, or the observed node: two declarations of one resource, or an `Image` node and its synthesized entity, count once. |
| `agreement` | `all` (default), `any` | Tells typed obligations whether every entity bound to the CALM node must satisfy the requirement (`all`), or one is enough (`any`). `Set.AgreementOf(<CALM ID>)` gives `any` only when every deployed-resource binding of that node says `any`. Code bindings do not take part. |

### Status and authority

| Field | Values | Effect |
|---|---|---|
| `status` | `active` (default), `proposed` | A `proposed` binding is skipped by resolution, is never required to match, and never matches an entity. A person sets it to `active`. `bind discover --strategy deployment` writes candidates with `status: proposed` to the candidates file only. |
| `authority` | text | Names the person or process that approved the binding. Concordia records it, with the binding ID and status, in the evidence of each result that uses the binding (`Set.UsedBy` lists the binding IDs of an entity). It is not verified in this release. |

## Trust context

A binding entry can declare the trust context of the node that it names. The `trust` object is closed and both keys are optional:

```yaml
bindings:
  - id: bind-payments-prod
    architectureRef: calm:node:payments-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: payments}
    trust:
      dimensions: {account: a-prod, region: eu-west-1}
      classification: restricted
```

| Field | Type | Description |
|---|---|---|
| `bindings[].trust.dimensions` | object | Dimension name to value. Each name is a built-in dimension (`organization`, `account`, `principal`, `zone`, `region`) or one of the workspace `trust.dimensions`. An undeclared name gives `CO-SEC-001` (error, exit code 2). |
| `bindings[].trust.classification` | string | A value of the workspace `trust.classifications`. A value outside the list gives `CO-SEC-001` (error, exit code 2). A missing classification stays `unknown`. |

An unknown key under `trust` fails the schema (`CO-BND-002`, exit code 2). The invalid part of a declaration is dropped, so a later check never uses it. Concordia merges the values of a node and of the binding entries that name it; if two declarations differ on a dimension, that dimension is `unknown` for the node. `index` and `check` never write a binding file. See [Trust context reference](trust-context.md) for the dimensions, the classification, and the crossings that Concordia derives from these values.

## CALM-embedded bindings

A node can carry a `concordia` extension with a `bindings` array (see [Architecture reference](architecture.md)). Each item converts to a binding with these values:

| Field | Value |
|---|---|
| `id` | `calm-ext:<node-id>:<i>`, where `<i>` is the zero-based item index. |
| `architectureRef` | The node that carries the extension. |
| `exclusive` | `true`. |
| `required` | `true`. |
| `source` | `<architecture file>#<JSON pointer>`. |

The item `kind` maps to a match kind:

| Item `kind` | Match kind |
|---|---|
| `source-root` | `sourceRoot` |
| `package` | `package` |
| `qualified-name` | `qualifiedName` |
| `build-target` | `buildTarget` |
| `otel-service` | `otelService` |
| `workload` | `workload` |
| `image` | `image` |
| `route` | `route` |
| `infra-resource` | `infraResource` |
| `compose-service` | `composeService` |

The deployed-resource items carry the identity fields of a binding file, with kebab-case names for the two compound fields: `environment`, `cluster`, `api-group`, `resource-kind`, `namespace`, `name`, `repository`, `digest`, `address`, `file`, and `service`. They also carry the optional `cardinality`, `agreement`, `status`, and `authority`, which every embedded item accepts (the `kind`/`value` items too). The required fields of each `kind` are those of [Deployed-resource kinds](#deployed-resource-kinds). The extension schema rejects any other `kind` or key with exit code 2.

Embedded and file bindings are merged and sorted by ID. A duplicate ID across them gives `CO-BND-006`.

## Related diagnostics

`CO-BND-001` to `CO-BND-006`, `CO-BND-008` (cardinality `one` breached), and `CO-BND-009` (ambiguous non-exclusive bindings): see [Diagnostics reference](diagnostics.md). `CO-BND-007` is reserved.

`CO-SEC-001`: a `trust` value that is not in the workspace vocabulary (see [Trust context reference](trust-context.md)).

## Next steps

- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Policies reference](policies.md)
- [CLI reference: `bind validate`](cli.md)
