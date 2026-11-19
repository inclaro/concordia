# Deployment traceability release notes

This release adds the deployment trace and the `infrastructure` pack on top of the [evidence foundation](evidence-foundation.md). It covers plain Kubernetes manifests and the Dockerfiles that build their images. This page lists what a user can see change. No existing workspace needs a change to keep its results: the new rule pack is opt-in, and the other additions are results fields and a command.

## A placement fact and two coverage predicates

The capability profile `cbm-0.12.1` gains three evidence families, all with the maturity `advisory`. See [Capability profiles](../reference/capability-profiles.md).

- `placed-in` is a derived fact from a workload to the namespace that hosts it. A `deploys-artifact` fact is never an input. A namespace that no Namespace object declares gets a synthesized hosting target. An `infraResource` binding such as `Namespace/<name>` maps that target to a CALM container node.
- `deployment-workload-set` is a coverage predicate for each environment. It also lists declared workloads that the provider did not return, so a missing node is never a definite absence.
- `dockerfile-set` is a coverage predicate for each project.

`container-occurrence-versions` was already in the profile and is reused.

## The deployment trace

For each trace subject and each selected environment, Concordia builds a chain: logical service, workload, container and image reference, build definition, source evidence. Each step states its evidence role, resolution, gaps, and limits.

- The trace shows the declared level only. It never says that a workload runs.
- An image to Dockerfile link is a lineage candidate. It is never proof that the deployed image comes from that source.
- A workload whose environment is `unresolved` stays in a visible list.
- `results.json` has a new top-level `deploymentTrace` object and the `deploymentTrace` area is filled. `schemaVersion` stays `1.1`. See [Results reference](../reference/results.md#deployment-trace).
- The new command `concordia trace <architecture-id>` prints the trace. It accepts `--environment`, `--source`, `--results`, and `--format`. It works with `--offline`. An id that is not a trace subject, and an environment that is not declared, are `CO-WRK-010` and `CO-WRK-017` (exit 2). See [trace](../reference/cli.md#trace) and [Trace a deployment](../how-to/trace-a-deployment.md).
- `concordia explain` prints the trace excerpt of the subject for a finding of an `infrastructure` rule.

## The `infrastructure` pack

The built-in pack `infrastructure` (version `1.0.0`) has six typed rules. Concordia builds one obligation for each subject and each declared environment.

| Rule | What it requires |
|---|---|
| `INFRA-PLC-001` | A deployed service is placed in its declared container. |
| `INFRA-PLC-002` | A deployed service has no unexpected placement. A reverse placement is unexpected. |
| `INFRA-TRC-001` | Each deployable service has a workload. |
| `INFRA-TRC-002` | Each workload of a deployable service has a resolved image reference. |
| `INFRA-TRC-003` | Each image of a deployable service has a build definition candidate. |
| `INFRA-TRC-004` | Occurrence and version coverage of the images is sufficient. |

- The pack is opt-in. Add `- use: builtin:infrastructure` to `policies.packs`. See [Infrastructure rules](../reference/infrastructure-rules.md).
- Every rule is advisory by default. Violations do not set exit code 1 until you set a mode per family and environment with `check.enforcement`. The families are `deployment-placement` and `deployment-trace`.
- A mandatory rule on a family that has the maturity `advisory` in the profile is refused with `CO-POL-018` (exit 2).
- The trace rules select nodes with the tag `deployable`. A model without that tag gives them no subject. The placement rules select nodes that a `deployed-in` relationship lists.
- A rule that has a subject and no evidence is a `violation` only when the coverage of its evidence family is `complete` for the environment. Otherwise it is `unknown` with the reason `coverage-incomplete`.

## Typed-obligation extensions

Four additions to the `obligation:` block, used by the pack and available to your own typed rules. See [Policies reference](../reference/policies.md#typed-rules-the-obligation-block).

- The selector key `deployedIn`: `true` for the declared container, `false` for any other hosting target.
- The direction `from-image`.
- The evidence key `passCompleteness`.
- Build lineage facts do not depend on an environment.

`concordia policy verify` prints `deployedIn` and `passCompleteness` for each typed rule.

## What needs your action

- Add `builtin:infrastructure` to `policies.packs` to get the rules, and declare `environments` and bindings. A workload, an image, and a hosting target take part only through a binding to a CALM node. See [Bindings reference](../reference/bindings.md#deployed-resource-kinds).
- Tag the nodes to trace with `deployable`.
- If the lockfile pins a pack, a new binary can change the digest of a built-in pack: `check` then fails with `CO-INT-012` until you update the lock. See [Use policy packs](../how-to/use-policy-packs.md).

## Unchanged

No new diagnostic code, workspace key, finding kind, or exit code. Fingerprints of the new rules use the typed fingerprint of the evidence foundation (environment and occurrence).
