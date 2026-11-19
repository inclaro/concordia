# Bind code to architecture

This guide shows you how to bind CALM nodes to the code that implements them, and how to fix binding errors.

## Prerequisites

- A [workspace](set-up-a-workspace.md) with a CALM architecture that has nodes.
- The repository indexed with `concordia index`.

## Discover candidates

1. Generate candidate bindings from the indexed graph:

   ```bash
   concordia bind discover
   ```

   The command writes `bindings/concordia.bindings.candidates.yaml` (change it with the global `--output`) and prints how many candidates it found. It matches directory names and qualified-name segments against the node IDs and names. Use `--strategy path`, `--strategy package`, or the default `--strategy path,package`.

2. Open the candidates file. Each candidate has a `match`, a `score`, the `reasons` it matched, `matchedObservedCount`, and `conflictsWith`. `reviewStatus` is always `unreviewed`: the command never edits your binding file.
3. Copy the candidates you accept into `bindings/concordia.bindings.yaml`. Candidates have a single `match` entry; a binding wraps entries in `match.any`:

   ```yaml
   apiVersion: concordia.io/v1alpha1
   kind: BindingSet
   bindings:
     - id: bind-billing
       architectureRef: calm:node:billing-service
       exclusive: true
       match:
         any:
           - sourceRoot: services/billing/**
     - id: bind-fulfillment
       architectureRef: calm:node:fulfillment-service
       exclusive: true
       match:
         any:
           - sourceRoot: services/fulfillment/**
           - sourceRoot: packages/fulfillment-contract/**
   ```

   Each entry in `any` has exactly one key: `sourceRoot` (a glob over file paths), `package` (a dotted prefix ending in `..`, such as `services.billing..`), `qualifiedName` (an exact name), `buildTarget` (a Bazel or Buck label such as `//services/billing/...`), or `otelService` (a `service.name` from runtime traces). Deployed resources use a mapping with `kind`; see [bind deployed resources](#bind-deployed-resources).
4. Delete or keep the candidates file. The workspace never reads it.

## Validate the bindings

```bash
concordia bind validate
```

On success the command prints `bindings valid: <n> binding(s), <b>/<o> observed node(s) bound, <u> unbound file(s)` and exits 0.

| Option | Effect |
|---|---|
| `--show-unbound` | Print a `CO-BND-005` warning for each in-scope indexed file that has no binding. |
| `--strict` | Exit 2 when any file is unbound. |

## Fix binding errors

All of these exit 2.

**`CO-BND-003`: ambiguous exclusive binding.** One observed node matches two bindings for different CALM nodes at the same specificity, and at least one is `exclusive`. The message names the observed node and both binding IDs. Fix it in one of these ways:

- Make one selector more specific. An `otelService` beats a `qualifiedName`, which beats a `package`, which beats a `buildTarget`, which beats a `sourceRoot`. Within a kind, the longer package prefix or the longer literal glob prefix wins.
- Set `exclusive: false` on the bindings that may legitimately share code.

**`CO-BND-004`: required binding matched zero entities.** The message names the binding. Check the glob against `concordia bind validate --show-unbound` output and the project `include`/`exclude` globs, then fix the selector. If the code does not exist yet, set `required: false`.

**`CO-BND-008`: cardinality `one` breached.** A binding with `cardinality: one` matched more than one entity in one environment. The message lists the entities. Narrow the match (add a `namespace`, a `resourceKind`, or a digest), or set `cardinality: many`.

**`CO-BND-009`: ambiguous non-exclusive bindings (warning).** Two non-exclusive bindings at the same tier map one entity to different CALM nodes. Concordia does not pick one: the obligations that depend on the entity are `unknown` with the reason `ambiguous-binding`. Make one binding more specific, or make the bindings exclusive so that the conflict is reported as `CO-BND-003`.

Other binding errors: `CO-BND-001` (the `architectureRef` names no CALM node), `CO-BND-002` (the file or a selector is invalid, or names an environment that the workspace does not declare), `CO-BND-006` (duplicate binding ID). See [diagnostics](../reference/diagnostics.md).

## Bind deployed resources

Deployed resources (Kubernetes workloads, routes, namespaces, Compose services, images) bind with the kinds `workload`, `route`, `infraResource`, `composeService`, and `image`. The required and optional fields of each kind are in the [binding reference](../reference/bindings.md#deployed-resource-kinds). The kinds `workload`, `route`, and `infraResource` need an `environment` that the workspace key `environments` declares (see [Workspace reference](../reference/workspace.md#environments)). The same workload name in `staging` and `production` is two resources, and a binding that names `production` binds only the production one.

1. Propose candidates from the deployed resources of the graph:

   ```bash
   concordia bind discover --strategy deployment
   ```

   The command writes candidates with `status: proposed` to the candidates file only. It never changes a binding file, and a `proposed` binding takes no part in resolution.

2. Copy a candidate you accept into a binding file as an item of `match.any`, and set the status yourself (the default is `active`):

   ```yaml
   bindings:
     - id: bind-billing-prod
       architectureRef: calm:node:billing-service
       cardinality: one
       agreement: all
       authority: platform-team
       match:
         any:
           - kind: workload
             environment: production
             apiGroup: apps
             resourceKind: Deployment
             namespace: payments
             name: billing
     - id: bind-payments-namespace
       architectureRef: calm:node:payments-container
       match:
         any:
           - kind: infraResource
             environment: production
             address: core/Namespace/_/payments
   ```

   The `infraResource` item binds the namespace `payments` of `production` to a CALM container node; placement obligations for workloads in that namespace then use the container node as the hosting target.

   An `image` item binds a container image repository. Add a `digest` to bind one immutable image only:

   ```yaml
   bindings:
     - id: bind-billing-image
       architectureRef: calm:node:billing-service
       match:
         any:
           - kind: image
             repository: registry.example.com/billing
   ```

3. Run `concordia bind validate` to check the file, then `concordia check`. A rule that needs the CALM node of a deployed resource that no binding matches gives an `info` finding of kind `binding-gap` with the next action `resolve binding` and the location of the resource. The rule is then `unknown` instead of `pass`, and the fact stays in the evidence.

## Bind inside the CALM file

You can also put bindings on a CALM node under its `concordia` extension. These bindings are always exclusive and required, and take the ID `calm-ext:<node>:<index>`:

```json
{
  "unique-id": "billing-service",
  "node-type": "service",
  "name": "Billing Service",
  "description": "Issues invoices.",
  "concordia": {
    "bindings": [{ "kind": "source-root", "value": "services/billing/**" }]
  }
}
```

The kinds are `source-root`, `package`, `qualified-name`, `build-target`, `otel-service`, `workload`, `image`, `route`, `infra-resource`, and `compose-service`. The last five take the same identity fields as in a binding file (see [Binding file reference](../reference/bindings.md)).

## Next steps

- [Write policies](write-policies.md)
- [Binding file reference](../reference/bindings.md)
- [How Concordia works](../explanation/how-concordia-works.md)
