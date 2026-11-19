# Core rules reference

The core pack is a set of architecture rules that ships inside the `concordia` binary. It is the built-in policy pack `builtin:core`, version `1.0.0`. The source is `internal/policy/packs/core.policy.yaml`.

To turn the pack on, see [Use policy packs](../how-to/use-policy-packs.md). For the pack format and the five predicates, see [Policies reference](policies.md).

## How the core rules work

The core rules do not know your system. They know only the CALM node types and a set of tags. A node type is a CALM `node-type`, for example `service` or `database`. A tag is a string in the `concordia.tags` list of a node.

- A rule that selects nodes by tag checks nothing until your architecture model carries that tag. This is intended. On a model without tags, these rules have no subject, and their verdict is `not-applicable` (`CO-CON-022`).
- A rule that selects nodes by node type works on any model.
- Each rule reports an edge between two code entities that are bound to two different CALM nodes. Code that is not bound to a node is not checked. See [Bindings reference](bindings.md).
- Each rule uses the evidence settings of the [`evidence` block](policies.md#the-evidence-block). The default minimum confidence is `syntactic`. The default minimum coverage is `0.95`.

## Rules

| Rule ID | Severity | Predicate | What it checks |
|---|---|---|---|
| `CORE-CYC-001` | error | `require` (acyclic) | Services and systems have no dependency cycle. |
| `CORE-DECL-001` | error | `declaredOnly` | Each dependency between services, databases, and web clients is declared in the architecture. |
| `CORE-DB-001` | error | `forbid` | A client does not access a database directly. |
| `CORE-BND-001` | error | `forbid` | A service does not import the code of another service. |
| `CORE-LYR-001` | error | `layerOrder` | Layers depend downward only. |
| `CORE-LYR-002` | error | `forbid` | The domain does not depend on infrastructure or presentation. |
| `CORE-LYR-003` | warning | `forbid` | Shared code does not depend on a service, a web client, or a system. |
| `CORE-SEC-001` | error | `forbid` | Public and external code does not access restricted nodes directly. |
| `CORE-OBS-001` | warning | `observedRequired` | Each declared connection that the model marks as required is observed. |

### CORE-CYC-001: no dependency cycles

|  |  |
|---|---|
| Subject | Nodes of type `service` or `system`. |
| Edges | `IMPORTS`, `CALLS`. Code entities are collapsed to their CALM node first. |
| Tags needed | None. |
| Finding kind | `cycle` |
| Tune | Change the severity with an override. The edge kinds are fixed. |

### CORE-DECL-001: dependencies are declared

|  |  |
|---|---|
| Subject | Nodes of type `service`, `database`, or `webclient`. Both ends of the edge must be subjects. |
| Edges | `IMPORTS`, `CALLS`, `HTTP_CALLS`, `ASYNC_CALLS`, `READS`, `WRITES`. |
| Tags needed | None. |
| Finding kinds | `undeclared` (no relationship between the nodes), `contradiction` (the relationship does not allow the edge kind). |
| Tune | Declare the dependency as a `connects` relationship. Use `concordia.observation.allowed-edge-kinds` on the relationship to limit the kinds. |

The CBM adapter gives `HTTP_CALLS` and `ASYNC_CALLS` the confidence `heuristic`. The default minimum confidence is `syntactic`, so these edges are not reported: they are counted as sub-threshold evidence, and the rule can end as `unknown` (`CO-CON-023`). To check these edges, set `evidence.minimumConfidence: heuristic` for this rule with an override. Then low-confidence `CALLS` edges count too.

### CORE-DB-001: clients do not access a database

|  |  |
|---|---|
| Subject | Nodes of type `webclient` and nodes tagged `client`. |
| Target | Nodes of type `database`. |
| Edges | `IMPORTS`, `CALLS`, `READS`, `WRITES`. |
| Tags needed | `client` (optional: `webclient` nodes work without it). |
| Finding kind | `forbidden-dependency` |
| Tune | Tag a node `client` to include it. Disable the rule for a legacy case with `enabled: false`, or waive one finding. |

### CORE-BND-001: no code imports between services

|  |  |
|---|---|
| Subject | Nodes of type `service`. |
| Target | Nodes of type `service`. |
| Edges | `IMPORTS`, `INHERITS`, `IMPLEMENTS`. |
| Allowed | An edge to a node tagged `contract` or `shared-kernel`. |
| Tags needed | `contract`, `shared-kernel` (to allow shared code). |
| Finding kind | `forbidden-dependency` |
| Tune | Put the published contract or the shared kernel in its own CALM node. Tag that node. The tag is read on the target node of the edge. |

A service that calls another service (`CALLS`) is not a finding of this rule. `CORE-DECL-001` checks that the call is declared.

### CORE-LYR-001: layers depend downward only

|  |  |
|---|---|
| Layers | In order: nodes tagged `presentation`, then `application`, then `domain`. |
| Edges | `IMPORTS`, `CALLS`. |
| Tags needed | `presentation`, `application`, `domain`. |
| Finding kind | `layer-violation` |
| Tune | The edge kinds and the layers are fixed in this pack. To use other layers, write your own rule. |

An edge from a lower layer to a higher layer is a finding. An edge that skips a layer (for example presentation to domain) is allowed. A node with more than one layer tag takes the first layer in the order above.

### CORE-LYR-002: the domain does not depend on infrastructure

|  |  |
|---|---|
| Subject | Nodes tagged `domain`. |
| Target | Nodes tagged `infrastructure` or `presentation`. |
| Edges | `IMPORTS`, `CALLS`. |
| Tags needed | `domain`, `infrastructure`, `presentation`. |
| Finding kind | `forbidden-dependency` |
| Tune | Change the severity with an override. Fix a finding with dependency inversion: the domain owns an interface and the infrastructure implements it. |

### CORE-LYR-003: shared code is a leaf

|  |  |
|---|---|
| Severity | warning |
| Subject | Nodes tagged `shared`. |
| Target | Nodes of type `service`, `webclient`, or `system`. |
| Edges | `IMPORTS`, `CALLS`. |
| Allowed | An edge to a node that is tagged `shared` too. |
| Tags needed | `shared`. |
| Finding kind | `forbidden-dependency` |
| Tune | Raise the severity to `error` with an override when you want the build to fail. |

### CORE-SEC-001: trust boundary

|  |  |
|---|---|
| Subject | Nodes tagged `public` or `external`. |
| Target | Nodes tagged `restricted`. |
| Edges | `IMPORTS`, `CALLS`, `READS`, `WRITES`, `HTTP_CALLS`. |
| Tags needed | `public` or `external`, and `restricted`. |
| Finding kind | `forbidden-dependency` |
| Evidence | `minimumConfidence: heuristic`. |

This rule sets the minimum confidence to `heuristic`, so that `HTTP_CALLS` edges count. The same level applies to the other edge kinds. If this gives false findings, raise the level with `evidence: { minimumConfidence: syntactic }` in an override. Then `HTTP_CALLS` edges are no longer reported.

### CORE-OBS-001: no architecture drift

|  |  |
|---|---|
| Severity | warning |
| Subject | All nine CALM node types: `actor`, `ecosystem`, `system`, `service`, `database`, `network`, `ldap`, `webclient`, `data-asset`. |
| Checks | Each `connects` or `interacts` relationship with `concordia.observation.required: true`. |
| Tags needed | None. |
| Finding kind | `unobserved` |
| Tune | Set `required` on a relationship in the model. Set `allowed-edge-kinds` and `minimum-confidence` on it to say which evidence counts. |

A relationship without `required: true` is never checked by this rule. The rule reports only when the file coverage meets the required minimum. A node with a custom `node-type` is not a subject.

## Decision governance

The core rules check the code against the architecture. Whether the decisions behind the rules are committed, active, and enforced is a separate check: the built-in pack `builtin:adr-governance` (`GOV-ADR-001` to `GOV-ADR-006`). It is not part of `core`, and the digest of `core` does not change when you add it. A decision links the rules that enforce it, for example `CORE-DB-001`, in the [decision register](adr-governance.md). See [policies reference](policies.md#decision-governance-rules-the-decisions-key) for the rule key and the rules.

## Results that can change

The tokens have one meaning (see [Token meanings](policies.md#token-meanings)), and the pair reconciliation looks at code kinds only. The core pack reads these meanings, so some of its results can differ from the results of an earlier release on the same repository.

**EVD-06: `IMPORTS` is code imports only.** Before, every native `IMPORTS` edge was an `imports` edge. Now a Terraform module import (`via=terraform`) and an import from a `kustomization.yaml` are `includes-infrastructure`, and an `IMPORTS` edge with no `via` from a file that is neither code nor a Kustomize file is `ambiguous` (`CO-OBS-019`). A rule that lists `IMPORTS` no longer counts these edges. A finding that rested only on such an edge is gone, and a dependency, a cycle, or a layer path that went through one is gone with it. A repository with no Terraform and no Kustomize files is not affected.

| Rule | What can change |
|---|---|
| `CORE-CYC-001` | A cycle that needed an infrastructure import disappears. |
| `CORE-DECL-001` | An infrastructure import between two bound nodes is no longer an undeclared dependency. |
| `CORE-DB-001` | An infrastructure import from a client to a database is no longer a finding. |
| `CORE-BND-001` | An infrastructure import between two services is no longer a forbidden import. |
| `CORE-LYR-001`, `CORE-LYR-002`, `CORE-LYR-003` | An infrastructure import between two layers, or from shared code, is no longer a finding. |
| `CORE-SEC-001` | An infrastructure import into a restricted node is no longer a trust boundary finding. |
| `CORE-OBS-001` | An infrastructure import no longer satisfies a required relationship, so an `unobserved` finding can appear. |

The other kinds that these rules read (`CALLS`, `READS`, `WRITES`, `HTTP_CALLS`, `ASYNC_CALLS`, `INHERITS`, `IMPLEMENTS`) keep their meaning. Facts of infrastructure, reference, documentation, diagram, protocol, and test kinds never enter `reconciliation[]`.

## Tag conventions

A tag is a plain string in `concordia.tags` of a CALM node. The core pack does not check the tags themselves. It uses them to choose the nodes of a rule.

| Tag | Meaning | Used by |
|---|---|---|
| `presentation` | User interface layer. | `CORE-LYR-001`, `CORE-LYR-002` |
| `application` | Application or use-case layer. | `CORE-LYR-001` |
| `domain` | Domain layer with the business rules. | `CORE-LYR-001`, `CORE-LYR-002` |
| `infrastructure` | Technical code: persistence, messaging, frameworks. | `CORE-LYR-002` |
| `shared` | Common code that other nodes use. It must not use the nodes of a service. | `CORE-LYR-003` |
| `contract` | A published interface of a service. Other services may import it. | `CORE-BND-001` |
| `shared-kernel` | A small model that several services own together. Other services may import it. | `CORE-BND-001` |
| `client` | A node that runs on the side of the caller, for example a mobile app. | `CORE-DB-001` |
| `public` | A node that untrusted callers can reach. | `CORE-SEC-001` |
| `external` | A node that is outside your control, or a third-party system. | `CORE-SEC-001` |
| `restricted` | A node that holds data or functions that need protection. | `CORE-SEC-001` |

Put a tag on a node in the `concordia` extension (see [Architecture reference](architecture.md#node-concordia)):

```json
{
  "unique-id": "orders-domain",
  "node-type": "service",
  "name": "Orders Domain",
  "description": "Business rules for orders.",
  "concordia": {
    "tags": ["domain"]
  }
}
```

The node must also be bound to code (see [Bindings reference](bindings.md)). A node without a binding gives no edges, so no rule reports it.

## Change a rule

You can change the severity, turn a rule off, or change the evidence settings and the owner of a rule without a copy of the pack. Use the `rules` key of the pack entry in `policies.packs`. See [Use policy packs](../how-to/use-policy-packs.md).

```yaml
policies:
  packs:
    - use: builtin:core
      rules:
        CORE-LYR-003: { severity: error }
        CORE-SEC-001: { enabled: false }
```

A rule ID never changes meaning inside a pack version. Baselines and waivers use the rule ID in their key.

## Review currency

Whether the assessments that the workspace requires have a current, verified approval is a separate check: the built-in pack `builtin:review-currency` (`REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, and `REV-GOV-001`). It is not part of `core`, and the digest of `core` does not change when you add it. The rules default to `advisory`. See [policies reference](policies.md#review-currency-rules-the-assessmentcurrency-key) for the rule key and the rules, and [assessments](assessments.md#review-rules) for the documents and the rule results.
