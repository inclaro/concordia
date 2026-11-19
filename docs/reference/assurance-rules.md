# Assurance rules reference

The assurance pack is a set of rules that ships inside the `concordia` binary. It is the built-in policy pack `builtin:assurance`, version `1.0.0`. The source is `internal/policy/packs/assurance.policy.yaml`.

To turn the pack on, add `- use: builtin:assurance` to `policies.packs` (see [Use policy packs](../how-to/use-policy-packs.md)). The pack is opt-in. For the rule format, see [Policies reference](policies.md#assurance-rules-requireeffectiveaccess-requirecheckresult-and-requireclaim). For the claims that the rules need, their contracts, and the reasons for `unknown`, see [Assurance](assurance.md). To supply the evidence, see [Add stronger evidence](../how-to/add-stronger-evidence.md).

## How the assurance rules work

Each rule is a `require.claim` rule. It needs one claim of the claim registry, and Concordia evaluates the claim for every subject that the claim covers. The subject is an environment for ASR-REN-001 and a container occurrence for ASR-PRV-001 and ASR-PRV-002.

- The rules read the evidence that the workspace key [`assurance`](workspace.md#assurance) declares: rendered environments, provenance statements, deployment records, and observations. They do not read the code graph.
- A rule declares no `subject`. The claim names its subjects, and a rule-level `subject` with `require.claim` is refused with `CO-POL-001`.
- A rule is `unknown`, never `pass`, when the workspace has no `assurance` key or when no loaded contract enables the claim (`claim-not-enabled`). The finding names the input that is missing. See [Reasons](assurance.md#reasons).
- A contract is `experimental`, so the severity of a finding of these rules is capped at `warning`.
- The rules declare no `enforcement`. Each rule is `advisory` by default. See [Enforcement](#enforcement).
- A result is evaluated for each environment of the run. A run limited with `check --environment production` gives the other environments no result.

| Rule ID | Family | Claim | Subject |
|---|---|---|---|
| `ASR-REN-001` | `assurance` | `complete-resource-set` | Each environment |
| `ASR-PRV-001` | `assurance` | `exact-lineage` | Each container occurrence of the rendered resources |
| `ASR-PRV-002` | `assurance` | `runtime-drift` | Each container occurrence of the rendered resources |

## Rendering rule

### ASR-REN-001: each environment has complete rendered evidence

|  |  |
|---|---|
| Subject | Each environment of the run. |
| Claim | `complete-resource-set`, enabled by the `rendering` contract. |
| Evidence | Level `rendered-or-evaluated`, completeness `complete`, integrity at least `content verified by digest`. |
| Family | `assurance` |
| Default | `advisory`, severity `warning` |

Static manifests, charts, and overlays show what an author wrote, not what a cluster receives. The claim holds when every rendered source of the environment is complete.

| Result | Meaning |
|---|---|
| `pass` | Every rendered source of the environment has the coverage `complete`. The detail gives the number of sources and resources. |
| `unknown`, `rendered-evidence-missing` | No rendering source is declared for the environment, or a source is `partial` or `failed`. The detail names the environment or the gaps, and the finding shows the declared source path. A partial resource set never counts as complete. |
| `unknown`, `claim-not-enabled` | No loaded contract enables `complete-resource-set`. |

The rule never gives a `violation`: a render that is not complete says that the evidence is missing, not that the environment is wrong. To fix it, declare a source under `assurance.rendering`, pin the renderer with `concordia lock update`, or fix the render error. See [Rendering](assurance.md#rendering).

## Provenance rules

The provenance rules say whether the image that a container occurrence names can be traced to the source that was reviewed, and whether the digest that runs is the digest that was deployed. They use the contract that you declare under [`assurance.provenance`](workspace.md#assuranceprovenance).

### ASR-PRV-001: each container occurrence has exact lineage from source to deployed image

|  |  |
|---|---|
| Subject | Each container occurrence (`container`, `init`, or `ephemeral`) of the rendered resources. |
| Claim | `exact-lineage`, enabled by the `provenance` contract. |
| Evidence | Level `rendered-or-evaluated`, completeness `complete`, integrity `producer authenticated`. |
| Family | `assurance` |
| Default | `advisory`, severity `warning` |

A tag or a static link from an image to a Dockerfile only suggests where an image came from. Exact lineage needs a digest, a verified provenance statement for that digest from a producer trusted for `build-provenance`, a builder in the trust contract, and a source in the declared repositories.

| Result | Meaning |
|---|---|
| `pass` | An admissible chain of statements binds the digest of the occurrence to the source revision. The result is the entry of the occurrence in [`assurance.lineage`](results.md#lineage). |
| `violation` | A verified provenance statement names the digest, but its builder is not in the trust contract. |
| `unknown` | The chain is not complete: the image has a tag and no digest, no statement was supplied, the statement is not admissible, or none of its resolved dependencies is a declared source repository. The `missing` list of the entry says what is absent, and `rejected` lists each statement with every reason. |
| `unknown`, `rendered-evidence-missing` | The environment has no complete rendered source, so its occurrences are not all known. This result belongs to the environment. |

A SLSA level in a statement is ignored, and the rule never derives one.

### ASR-PRV-002: the deployed digest of each container occurrence matches the observed digest

|  |  |
|---|---|
| Subject | Each container occurrence of the rendered resources. |
| Claim | `runtime-drift`, enabled by the `provenance` contract. |
| Evidence | Level `runtime-observation`, completeness `complete`, integrity `producer authenticated`. |
| Family | `assurance` |
| Default | `advisory`, severity `warning` |

The claim compares three digests: the digest of the rendered image, the digest that a deployment record says was rolled out, and the digest that a workload observation shows.

| Result | Meaning |
|---|---|
| `pass` | The admissible digests agree. |
| `violation` | Two digests differ. The detail names both digests and both sources (`rendered`, `deployment-record`, or `workload-observation`) and does not choose between them. The entry lists each difference under `runtimeDrift.findings`. |
| `unknown` | A deployment record or an observation is missing, or is not admissible. The next step is to supply a current observation of the environment. |
| `unknown`, `rendered-evidence-missing` | As for ASR-PRV-001. |

The deployment record and the observation are evidence of what was recorded and observed at their own time. A result never says that the cluster runs the digest now.

## Enforcement

Every rule of the pack is `advisory` by default, so the pack does not change the exit code: an `unknown` result shows in the report with the enforcement `advisory`, and a violation does not set exit code `1`. Set the mode by rule, pack, or family with `check.enforcement` entries. The family is `assurance`. Write the narrow entries first. A rule of the pack cannot be `mandatory` in this release: no assurance claim is stable until its fixture suite passes, so a `mandatory` mode, from `check.enforcement` or from the rule, stops `check` with `CO-POL-018` (exit 2) and the guarantee `family assurance-claim has maturity advisory`. Use `advisory` or `shadow`.

```yaml
check:
  enforcement:
    - { family: assurance, environment: staging, mode: shadow }
    - { rules: [ASR-PRV-001], environment: staging, mode: shadow }
```

With `shadow`, a finding appears only in `results.json` with the enforcement `shadow`. See [roll out new obligations](../how-to/roll-out-new-obligations.md) and [check.enforcement](workspace.md#checkenforcement).

## Rule IDs

Baselines and waivers key on the rule ID. The IDs of this pack are never reused or renumbered.

## Related diagnostics

- `CO-POL-001`: a rule declares a `subject` with `require.claim`.
- `CO-PRV-003`: a pinned renderer is missing or differs from the lock.
- `CO-PRV-009`: a statement is malformed, fails verification, or does not match its subject.
- `CO-PRV-010`: a statement is not admissible.

See [Diagnostics](diagnostics.md).
