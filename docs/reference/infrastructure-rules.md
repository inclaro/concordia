# Infrastructure rules reference

The infrastructure pack is a set of deployment rules that ships inside the `concordia` binary. It is the built-in policy pack `builtin:infrastructure`, version `1.0.0`. The source is `internal/policy/packs/infrastructure.policy.yaml`.

To turn the pack on, add `- use: builtin:infrastructure` to `policies.packs` (see [Use policy packs](../how-to/use-policy-packs.md)). For the rule format, see [Policies reference](policies.md#typed-rules-the-obligation-block). For the evidence that the rules read, see [Capability profiles](capability-profiles.md).

## How the infrastructure rules work

All six rules are typed rules. Concordia builds one obligation for each subject and each declared environment, and each obligation has its own result. Declare the environments in the workspace first. See [Workspace reference](workspace.md).

- The rules read the manifests of the repository as evidence. Nothing is rendered, executed, or run. A result says what the manifests declare, never what runs in a cluster.
- A rule that has a subject and no evidence is a `violation` only when the coverage of its evidence family is `complete` for the environment. Otherwise it is `unknown` with the reason `coverage-incomplete` and the loss mode that limits the coverage. See [Verdicts and coverage](../explanation/verdicts-and-coverage.md).
- A workload, an image, and a hosting target take part only through a binding to a CALM node. Concordia never attaches a workload to a node by name. See [Bindings reference](bindings.md#deployed-resource-kinds).
- The rules declare no `scope` and no `enforcement`. Each rule is `advisory` by default. See [Enforcement](#enforcement).

| Rule ID | Family | Obligation | Subject |
|---|---|---|---|
| `INFRA-PLC-001` | `deployment-placement` | `required` `placed-in` from the workload, target the declared container | Nodes that a `deployed-in` relationship lists |
| `INFRA-PLC-002` | `deployment-placement` | `forbidden` `placed-in` from the workload, target any other hosting target | Nodes that a `deployed-in` relationship lists |
| `INFRA-TRC-001` | `deployment-trace` | `required` `exists` `placed-in` from the workload | Nodes tagged `deployable` |
| `INFRA-TRC-002` | `deployment-trace` | `required` `uses-image` from the workload | Nodes tagged `deployable` |
| `INFRA-TRC-003` | `deployment-trace` | `required` `built-from` from the image | Nodes tagged `deployable` |
| `INFRA-TRC-004` | `deployment-trace` | `required` `uses-image` from the workload, with full occurrence coverage | Nodes tagged `deployable` |

The trace rules select by the tag `deployable` only. Add the tag to the `concordia.tags` list of each node whose deployment you want to trace. A model without that tag gives the trace rules no subject. The placement rules need no tag: their subject is the `deployed-in` relationship (`deployedIn: true`, see [Policies reference](policies.md#selecting-the-declared-deployment-topology-deployedin)).

## Placement rules

### INFRA-PLC-001: the declared placement is observed

|  |  |
|---|---|
| Subject | Each node that a `deployed-in` relationship lists, in each declared environment. |
| Obligation | `required`, `forAll`, `placed-in` from the workload. The target is the container of the relationship (`target: {deployedIn: true}`). |
| Evidence | Role `parsed-source-fact`, `minimumResolution: explicitly-bound`, `completeness: complete`, `passCompleteness: any`. |
| Family | `deployment-placement` |
| Default | `advisory` |

A placement needs three things: a workload that a binding maps to the node, a `placed-in` fact from the workload to a hosting target of the same environment, and an `infraResource` binding of that hosting target to the container node. For plain Kubernetes the hosting target is the namespace of the workload. When the manifests declare no Namespace object, bind `Namespace/<name>` of the environment with an `infraResource` binding: the hosting target is then a synthesized entity.

| Result | Meaning |
|---|---|
| `pass` | A workload of the node is placed in the hosting target that is bound to the container. A parsed placement is evidence on its own, so the pass does not need complete coverage. The result shows the manifest file and line. |
| `violation` | No placement exists in the environment and the coverage of `placed-in` is `complete`. The next action is `fix implementation`. A placement in the reverse direction, or in another environment, does not count. |
| `unknown`, `coverage-incomplete` | No placement was found, and the extraction of the environment is partial (for example a Helm chart or a `deploy/` directory in the sources). The next action is `supply evidence`. |
| `unknown`, `binding-gap` | A workload of the node is placed in a hosting target that has no binding. The next action is `resolve binding`, and the finding shows the manifest location. |
| `unknown`, `ambiguous-binding` | Non-exclusive bindings map one workload or hosting target to different nodes. |
| `unknown`, `evidence-conflict` | Two files of one environment declare the same workload. CO-OBS-021 names both files. |
| `unknown`, `unsupported-family` | The CBM build has no profile with the family `placed-in`. The next action is `fix analysis configuration`. |
| `not-applicable` | The model has no `deployed-in` relationship. |

With `agreement: all` in the bindings of the node (the default), every bound workload must be placed correctly, and a passing workload does not hide a failing one. With `agreement: any`, one correct placement is enough.

A placement in another environment never satisfies the obligation. A run limited with `check --environment production` gives the obligations of the other environments no verdict.

### INFRA-PLC-002: no unexpected placement

|  |  |
|---|---|
| Subject | Each node that a `deployed-in` relationship lists, in each declared environment. |
| Obligation | `forbidden`, `forAll`, `placed-in` from the workload. The target is any hosting target that is bound to a CALM element other than the declared container (`target: {deployedIn: false}`). |
| Evidence | Role `parsed-source-fact`, `minimumResolution: explicitly-bound`, `completeness: complete`. |
| Family | `deployment-placement` |
| Default | `advisory` |

The results of INFRA-PLC-002 are separate from those of INFRA-PLC-001. It reports a workload that is placed in a hosting target bound to another element, and a placement in the reverse direction: a `placed-in` fact whose workload is bound to the container and whose hosting target is bound to the node.

| Result | Meaning |
|---|---|
| `pass` | Every workload of the node is placed in the declared container, or no placement exists, and the coverage is `complete`. |
| `violation` | A workload is placed in a hosting target that is bound to another element, or in the reverse direction. The class is wrong direction or unexpected placement, the result shows the manifest location, and the next action is `fix implementation`. |
| `unknown`, `binding-gap` | A workload of the node is placed in a hosting target that has no binding. The next action is `resolve binding`. The rule never guesses what an unbound namespace is. |
| `unknown`, other reasons | The same reasons as INFRA-PLC-001. |
| `not-applicable` | The node takes part in no `deployed-in` relationship, or the model has none. |

### The placement helper agrees with the rules

The deployment view and the view projection do not read these results. They read the placement records of `deployment.Placements`, a helper that needs no pack: it resolves each `placed-in` fact of a declared environment through the `workload` binding of the workload and the `infraResource` binding of the hosting target. A test on the `infra` fixture compares it with INFRA-PLC-001 and INFRA-PLC-002 for each node and environment:

| Rule result | Helper result |
|---|---|
| INFRA-PLC-001 `pass` | A record in the declared container. |
| INFRA-PLC-002 `violation`, unexpected placement | A record in another container. |
| INFRA-PLC-002 `violation`, wrong direction | A record that is marked reverse. |
| `unknown`, `binding-gap` or `ambiguous-binding` | A gap with the same reason. |
| A definite absence | No record and no gap. |

## Trace rules

The trace rules say whether the chain from a service to its workload, image, and build definition can be followed in the manifests. The [`deploymentTrace` section of the results](results.md) shows the same chain with its gaps and limits.

### INFRA-TRC-001: each deployable service has a workload

|  |  |
|---|---|
| Subject | Nodes tagged `deployable`. |
| Obligation | `required`, `exists`, `placed-in` from the workload. |
| Evidence | Role `parsed-source-fact`, `minimumResolution: exact`, `completeness: complete`, `passCompleteness: any`. |
| Family | `deployment-trace` |
| Default | `advisory` |

`pass` when a bound workload of the node in the environment has a `placed-in` fact, also when the coverage is `partial`. An absence is a `violation` only when the coverage of `deployment-workload-set` for the environment is `complete`, and `unknown` with `coverage-incomplete` otherwise. A node with no workload binding follows the absence rule, and the trace lists any unbound workload.

### INFRA-TRC-002: each workload has a resolved image reference

|  |  |
|---|---|
| Subject | Nodes tagged `deployable`. |
| Obligation | `required`, `forAll`, `uses-image` from the workload. |
| Evidence | Role `parsed-source-fact`, `minimumResolution: exact`, `completeness: complete`, `passCompleteness: partial`. |
| Family | `deployment-trace` |
| Default | `advisory` |

`pass` for a bound workload that has a `uses-image` fact, even if the provider may have merged other containers of the workload. A container with a template expression as image gives no fact, and the rule is `unknown` with `coverage-incomplete` while the `uses-image` coverage of the environment is not `complete`. Concordia does not guess the image.

### INFRA-TRC-003: each image has a build definition candidate

|  |  |
|---|---|
| Subject | Nodes tagged `deployable`. |
| Obligation | `required`, `forAll`, `built-from` from the image (`direction: from-image`). |
| Evidence | Role `parsed-source-fact`, `minimumResolution: exact`, `completeness: complete`, `passCompleteness: any`. |
| Family | `deployment-trace` |
| Default | `advisory` |

`pass` for a node whose bound image is the source of a `built-from` fact, in every environment. The Dockerfile is a candidate, not a proof. A build link exists only when a build command names the image, so an absence is `unknown` with `coverage-incomplete` and the loss mode `build-link-needs-build-command`, never a `violation`. A node with no `image` binding is `not-applicable`: third-party and unbound images do not give a violation.

### INFRA-TRC-004: occurrence and version coverage is sufficient

|  |  |
|---|---|
| Subject | Nodes tagged `deployable`. |
| Obligation | `required`, `forAll`, `uses-image` from the workload. |
| Evidence | Role `parsed-source-fact`, `minimumResolution: exact`, `completeness: complete`. |
| Family | `deployment-trace` |
| Default | `advisory` |

The rule is never `pass` while a `merged-occurrences` gap is open or the `uses-image` facts are marked possibly lossy by the profile. It is `unknown` in those cases. CBM merges two containers of one workload that use one image repository with different tags into one fact, so the other tag is not visible. A workload whose containers use different repositories has one fact for each container.

## Enforcement

Every typed rule is `advisory` by default, so the pack does not change the exit code: an `unknown` result shows in the report with the enforcement `advisory`, and a violation does not set exit code `1`. Set the mode by family and environment with `check.enforcement` entries. Write the narrow entries first.

```yaml
check:
  enforcement:
    - { family: deployment-placement, environment: staging, mode: shadow }
    - { family: deployment-trace, mode: advisory }
```

With `shadow`, a finding appears only in `results.json` with the enforcement `shadow`: the pretty report, SARIF, and JUnit do not show it. `check.rollback` runs every typed rule as `shadow` until its expiry date. `check --rollout-report` lists each `unknown` with its reason and the findings that would block. See [roll out new obligations](../how-to/roll-out-new-obligations.md) and [check.enforcement](workspace.md#checkenforcement).

The mode `mandatory` is refused in this release. The profile families `placed-in`, `uses-image`, and `built-from` have the maturity `advisory`, so a `mandatory` entry for `deployment-placement` or `deployment-trace` stops `check` with `CO-POL-018` (exit code `2`). The message names the rule, the environment, and the family, for example `family placed-in has maturity advisory`.

## Why placement is a derived fact

CBM has no edge that says a workload is placed in a namespace. Concordia derives the fact `placed-in` while it normalizes the evidence: for each workload resource with a resolved namespace and a name that is not a template, it adds one `placed-in` fact from the workload to the Namespace of the same environment, or to a synthesized Namespace entity when the manifests declare none. The fact has the role `parsed-source-fact`, the resolution `exact`, the manifest location, and a derivation record that names the rule `placement-from-namespace` and the input nodes. A workload with an unresolved namespace (a Helm template or a Kustomize base) gets no fact, and the coverage of the environment says why.

A deployment action (`deploys-artifact`) and a selector join are never placement evidence. `kubectl apply -f` ends at a file, so it cannot name a hosting target.

## Why placement is not evaluated by pair reconciliation

The pair-level reconciliation (the `reconciliation` array of the results) compares code edges between two bound CALM nodes, and it accepts `deployed-in` in both directions. A placement fact has a direction (the workload is in the hosting target, never the reverse) and an environment (a placement in `staging` is not one in `production`). A pair scan can express neither. `placed-in` is therefore not a code kind, it creates no reconciliation pair, and no obligation result is replaced by a pair summary. Each obligation decides on its own facts with the CALM direction. A pair with one correct and one reverse placement shows two separate obligation results.

## Limits

- An unbound workload is not evidence for any node. It stays in the trace under the unbound workloads of its environment with its manifest location, and INFRA-PLC-001 for a node with no bound workload follows the absence rule.
- INFRA-PLC-002 gives `unknown` with `binding-gap` for a workload placed in an unbound namespace. Bind each namespace of the environment to the node it belongs to, or accept the `unknown`.
- INFRA-TRC-004 stays `unknown` while the profile marks the `uses-image` facts of the workload as possibly lossy. This is the lossy mark: it says the provider can merge containers, not that a container was lost.
- A Helm chart, a Kustomize source, and a Compose or Terraform source are not rendered. They limit the coverage of the environment and move an absence from `violation` to `unknown`.
- The trace stops at the manifests. A build definition is a candidate, and the results never say that anything was built, deployed, or runs.

## Forbid any placement in an environment

A `forbidden` rule on `placed-in` with no target forbids every placement of the subject in the scoped environment. This rule has no declared container, so it needs no `deployedIn` selector:

```yaml
- id: ORG-PLC-001
  name: Deployable services are not placed in the edge environment
  severity: error
  family: deployment-placement
  subject: { tags: [deployable] }
  obligation:
    type: forbidden
    relation: { kind: placed-in, direction: from-workload }
    scope: { environments: [edge] }
  evidence:
    roles: [parsed-source-fact]
    minimumResolution: explicitly-bound
```

The rule gives a `violation` for each workload of the subject that has a `placed-in` fact in `edge`, `pass` when the coverage of `placed-in` is `complete` and no placement exists, and `unknown` otherwise. Declare the environment `edge` in the workspace first.

## Related diagnostics

- `CO-POL-018`: a mandatory rule needs a guarantee that is absent, for example the maturity `stable` of its family.
- `CO-OBS-021`: two files of one environment declare the same workload.
- `CO-OBS-020`: a source directory is excluded by default and not indexed.
- `CO-POL-009`: the provider does not support the evidence kind of a rule.

See [Diagnostics](diagnostics.md).
