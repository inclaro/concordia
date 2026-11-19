# How to trace a deployment

This guide shows you how to follow a CALM service to its workloads, container images, build definitions, and source paths in each environment, and how to gate the rules that check the chain. The trace chain is at the **declared** level: what manifests, Dockerfiles, and bindings say. Rendered manifests and plans give a second level, `rendered-or-evaluated`, which the trace lists beside the chain. The trace does not show what a cluster runs or whether a deployment succeeded.

## Prerequisites

- A workspace that passes `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- An index of the project (`concordia index`) from a Codebase Memory version that the capability profile supports. See [capability profiles](../reference/capability-profiles.md).
- The repository holds the manifests, Dockerfiles, and build scripts that you want to trace.

## Declare the environments

A trace is built for each declared environment. List them under `environments` in `concordia.yaml`. Give each one the `sources` that hold its manifests, and a `cluster` name if you want the trace to show it:

```yaml
environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**"]
  - id: staging
    sources: ["k8s/staging/**"]
```

A manifest that is in the sources of no environment is listed under `unresolvedEnvironmentWorkloads` in `results.json`. It takes part in no environment trace. Without `environments`, the trace has the status `not-configured` and `concordia trace` exits with 2.

## Tag the services

A trace subject is a CALM node that you tag `deployable`, a node that a `deployed-in` relationship deploys, or a node that has a deployment binding. Tag a service in the architecture file like this:

```json
{"unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.", "concordia": {"tags": ["deployable"]}}
```

## Bind workloads, images, and namespaces

Concordia never binds a workload to a service because the names look alike. Bind the workload of each environment, the image repository, the source root, and the namespace that hosts the container:

```yaml
apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-workload
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: payments}
        - {kind: workload, environment: staging, resourceKind: Deployment, name: api, namespace: payments-staging}
  - id: bind-api-image
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: image, repository: registry.example.com/payments/api}
  - id: bind-api-source
    architectureRef: calm:node:api-service
    match:
      any:
        - sourceRoot: app/**
  - id: bind-worker-workload
    architectureRef: calm:node:worker-service
    match:
      any:
        - {kind: workload, environment: staging, resourceKind: Deployment, name: worker, namespace: payments}
  - id: bind-payments-namespace
    architectureRef: calm:node:prod-cluster
    match:
      any:
        - {kind: infraResource, environment: production, address: core/Namespace/_/payments}
```

A workload, image, or Dockerfile that no binding names stays visible under the unbound workloads of its environment, with its file and line.

## Enable the pack

The built-in `infrastructure` pack holds the rules INFRA-PLC-001 and INFRA-PLC-002 (placement) and INFRA-TRC-001 to INFRA-TRC-004 (the trace). Add it to the workspace:

```yaml
policies:
  packs:
    - use: builtin:infrastructure
```

Check that the rules are declared as you expect:

```sh
concordia policy verify
```

The rules are `advisory`: a violation or an `unknown` result shows in the report and does not change the exit code. See [infrastructure rules](../reference/infrastructure-rules.md).

## Run the check and read the report

```sh
concordia check
```

The report has a `Deployment trace` section of its own. It counts every subject by status, so an `unknown` subject is never hidden by a passing conformance section:

- `complete`: every step has an entity for every bound workload and no gap is open.
- `partial`: a step has an entity and a gap is open.
- `unresolved`: the workload set of the environment is complete and no workload was found.
- `unknown`: the coverage is not enough to decide.

## Read the trace of one service

```sh
concordia trace api-service
```

The command reads the results file of `check`, or runs the pipeline when there is none. It does not change the CALM model and works with `--offline`. For each environment it prints five steps: service, workload, container image, build definition, and source evidence. Each step shows its evidence role (declared intent, parsed source fact, or inferred association) and its resolution (exact, explicitly bound, inferred, ambiguous, or unresolved). Role and resolution stay separate: there is no score.

Under each step you find:

- the entities with their file and line, and the binding that links them to the service;
- `gap` lines: something that you can fix by supplying evidence or configuration, with a reason such as `coverage-incomplete`, `absent`, `binding-gap`, `mutable-tag`, or `ambiguous-build-definition`. Gaps decide the status;
- `limit` lines: boundaries that always apply to the declared level, for example that the link between the image and the Dockerfile is not verified. A limit never changes the status.

Limit the output to some environments with `--environment` (repeat it for more than one), and show the lines around each location with `--source`:

```sh
concordia trace api-service --environment production --source
```

The lines are read inside the workspace root, and secret values are replaced by a redaction marker (see [redaction](../reference/redaction.md)). A location outside the workspace root is shown without lines. For a tool, use `--format json`:

```sh
concordia trace worker-service --environment production --format json
```

An id that is not a trace subject exits with 2 and `CO-WRK-010`. An environment that is not declared exits with 2 and `CO-WRK-017`:

```sh
concordia trace ghost-service  # exit 2
concordia trace api-service --environment qa  # exit 2
```

## Add rendered or evaluated evidence

If the workspace declares [`assurance.rendering`](../reference/workspace.md#assurancerendering) sources or plans, the trace also reports the level `rendered-or-evaluated`. Nothing changes in the declared chain or in its status. The level shows in the text output and adds data in the JSON output (`concordia trace <id> --format json` and `results.json`):

- `levels`: the status of `rendered-or-evaluated` is `evaluated` when at least one environment has a rendered source or plan that is not `failed`, and `not-available` otherwise. `deployment-execution` and `runtime-observation` stay `not-available`.
- `evaluated` of an environment: its rendered sources and plans, each with its coverage (`complete`, `partial`, or `failed`) and digest.
- `evaluated` of a subject: the rendered workloads and the planned resources that a binding maps to the subject. A planned resource needs an `infraResource` binding with the address of the resource.

In the text output, the `Evidence:` line says whether the rendered-or-evaluated level is evaluated in the environment, and a `Rendered or evaluated` block after the five steps lists the sources with their coverage and the rendered entities of the subject:

```text
Evidence: declared configuration, and rendered or evaluated from 1 source (below). Not traced: deployment execution and runtime.
...
Rendered or evaluated: evaluated
   render helm deploy/chart: complete
   Deployment shop/orders  at deploy/chart
```

When every source of the environment failed, the `Evidence:` line says that no rendered source or plan holds evidence, and the block lists the failed sources. To produce this evidence, see [add stronger evidence](add-stronger-evidence.md).

## Read verified lineage

A build command that links an image to a Dockerfile is a `candidate` lineage. It is not verified: the trace keeps the limit `image-provenance-unverified`. When the workspace declares build provenance statements that verify, and the exact lineage claim passes for a container occurrence, the entity of that container prints `lineage verified`, and the missing static link is not a gap. Only an exact lineage claim from a provenance statement makes the lineage `verified`. The producer must be declared and the statement must verify as `producer authenticated`; see [add stronger evidence](add-stronger-evidence.md#trace-from-source-to-the-running-image) and the [lineage entries](../reference/results.md#lineage).

## Explain a finding

`concordia explain` for an `INFRA-` rule prints the environment, the scoped identity, and the occurrence of each finding, and the same trace block that `trace` prints for the subject of the finding:

```sh
concordia explain INFRA-PLC-001
```

## Set the mode of each family and environment

A typed rule is `advisory` until the workspace says otherwise. Use `check.enforcement` to set a mode for each rule family and environment. The families are `deployment-placement` for the INFRA-PLC rules and `deployment-trace` for the INFRA-TRC rules. The first entry that matches wins. This example keeps the trace findings of `staging` in `results.json` only (MIG-03 starts in `shadow`):

```yaml
check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
  enforcement:
    - {family: deployment-trace, environment: staging, mode: shadow}
```

Use `advisory` to show the findings of an environment without a gate, and `shadow` to keep them in `results.json` only. The mode `mandatory` is refused in this release: the families `placed-in`, `uses-image`, and `built-from` have the maturity `advisory`, so a `mandatory` entry for `deployment-placement` or `deployment-trace` stops `check` with `CO-POL-018` and exit code 2. The message names the rule, the environment, and the family. The steps of the rollout and the rollout report are in [roll out new obligations](roll-out-new-obligations.md).

## Related

- [`trace` in the CLI reference](../reference/cli.md#trace)
- [Infrastructure rules](../reference/infrastructure-rules.md)
- [Results file](../reference/results.md)
- [Resolve unknown verdicts](resolve-unknown-verdicts.md)
