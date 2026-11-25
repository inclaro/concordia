# Cloud firewall and use cases release notes

This release adds the `cloud-firewall` evaluator. A network rule can now check the security groups, network security groups (NSGs), and VPC firewall rules that a Terraform or OpenTofu plan will deploy on AWS, Azure, or GCP. The release also fixes how `check --changed` and `diff` load the base architecture, and it rewrites the documentation around use cases on a bank payments example. This page lists what a user can see change. The evaluator is opt-in and advisory.

## The `cloud-firewall` evaluator

- The evaluator answers whether TCP or UDP traffic from one planned cloud resource to another, on one port, is `allowed`, `denied`, or `unknown`. It reads the firewall rules of the plans that you declare under [`assurance.plans`](../reference/workspace.md#assuranceplans). It does not run Terraform, and it does not read the `.tf` files.
- The prefix of the resource type selects the provider: `aws_` (security groups), `azurerm_` (NSGs), or `google_` (VPC firewall rules). The [cloud-firewall reference](../reference/assurance.md#cloud-firewall) lists the endpoint types, the rule resources, and the semantics of each provider.
- Enable it with the new evaluator kind `cloud-firewall` in `assurance.evaluators[].kinds`. See [`assurance.evaluators`](../reference/workspace.md#assuranceevaluators).
- A `denied` result rests on the new assumption `firewall-rules-complete-in-plan`: no firewall rule for the queried resources exists outside the evaluated plans, for example in another Terraform state or in a change made in the console. Declare it in `assurance.evaluators[].assumes`. Without it, a `denied` answer is `unknown` with `enforcement-not-declared`. An `allowed` answer needs no assumption.
- The new contract is `cloud-firewall` (1.0.0). It enables the new claim `cloud-network-isolation`, which needs the integrity `content not verified`: the plan needs no signed statement. The contract does not enable `network-isolation`, `effective-encryption-in-transit`, `cloud-iam`, or `routing-reachability`. The claim registry now has 13 claims, so `claimsRegistryDigest` in `results.json` and the registry digest of `concordia doctor` change. See [Claims and contracts](../reference/assurance.md#claims-and-contracts).

## Network rules that use it

`require.effectiveAccess` with `dimension: network` keeps its form. There is no new policy dimension. Your bindings choose the evaluator for each pair of a rule:

| The two CALM elements bind to | Evaluator |
|---|---|
| planned resources ([`infraResource`](../reference/bindings.md)), both | `cloud-firewall` |
| rendered workloads, both | `network-policy` |
| a planned resource and a rendered workload | none: `unknown`, `binding-gap`, "the pair mixes planned cloud resources and Kubernetes workloads" |

- A pair with an `infraResource` binding on one end and no binding in the environment on the other goes to `cloud-firewall`, which reports a `binding-gap` for the unbound end.
- A `cloud-firewall` pair does not need rendered manifests.
- An environment without a `cloud-firewall` evaluator gives `unknown`, `no-evaluator-available`. An environment without a usable plan gives `unknown`, `rendered-evidence-missing`. A bound address that the plan does not hold gives `rendered-evidence-missing` with the gap `workload-not-in-input`.
- With `ports`, Concordia queries TCP on each port. Without `ports`, it takes the ports from the firewall rules of the plans, separately for each protocol, so a rule can also give `udp/<port>` queries.

See [`require.effectiveAccess`](../reference/policies.md) and [The gates of a result](../reference/assurance.md#the-gates-of-a-result).

## Results and `check` output

- Each query appears in [`assurance.evaluations`](../reference/results.md#evaluations) with the `kind` `cloud-firewall`. The `query` has the form `<protocol>/<port> from <source address> to <destination address>`, with plan addresses, for example `tcp/5432 from aws_lb.public to aws_db_instance.ledger`.
- Each entry in `policies` is `<address> <rule>`, for example `aws_vpc_security_group_ingress_rule.ledger_from_gateway rule` or `aws_security_group.gateway egress[0]`.
- `inputDigest` is the SHA-256 of the plan bytes, or the digest of the sorted plan digests when the environment has more plans.
- The new optional field `integrity` gives the integrity of the evidence that the evaluator read. A `cloud-firewall` entry always has it: `content not verified` for a plan without a statement, `producer authenticated` for a plan that a verified statement binds, and the lowest level for more plans. `schemaVersion` stays `1.1`.
- A `cloud-firewall` finding is located at the plan file, for example `infra/plans/production.json`.
- In the pretty `check` output, every effective-access finding now has one more line under its location. The line states what the evaluator observed, with the rules that decided it:

```text
  public-load-balancer -> ledger-db: 1 finding(s)
    infra/plans/production.json
      open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_security_group.gateway egress[0], aws_vpc_security_group_egress_rule.gateway_out rule, aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)
```

## What Concordia reads from a plan

The plan reader changes for every declared plan, also without the new evaluator:

- Of the `configuration` section, Concordia reads the `references` lists and the names of the attribute and block keys that the configuration sets for each resource. The lists hold resource addresses, not values. They let the evaluator resolve an attribute that is unknown until apply, such as the ID of a security group that the plan creates. The key names tell it which unknown attributes the provider computes only because the configuration does not set them.
- Concordia never reads `constant_value`.
- Of the values before a change (`change.before`), Concordia reads only the owner of a firewall rule resource that the plan deletes or replaces: `security_group_id`, `network_security_group_name`, and `resource_group_name`. A sensitive owner value is redacted, as before.

See [Plans](../reference/assurance.md#plans).

## When the answer is `unknown`

The evaluator gives `unknown` with a named gap when the plan cannot prove the answer. It never guesses. These cases are `unknown` by design in this release:

- A GCP firewall policy resource (`google_compute_network_firewall_policy*`, `google_compute_region_network_firewall_policy*`, or `google_compute_firewall_policy*`) in the plan. Every GCP query is then `unknown` with `unsupported-policy-kind`.
- IPv6 ranges, unless an IPv4 rule decides first.
- The two endpoints belong to different providers (`mixed-providers`), or to two Azure virtual networks or two GCP networks (`peering-not-evaluated`).
- An AWS prefix list, an Azure service tag other than `*`, `VirtualNetwork`, `Internet`, and `AzureLoadBalancer`, and an Azure source port range (`unsupported-policy-kind`).
- An inline `ingress`, `egress`, or `security_rule` block that names a group that the plan creates. Use rule resources, such as `aws_vpc_security_group_ingress_rule` or `azurerm_network_security_rule`, when you want such rules to decide.
- In a create plan, a new group without inline blocks, a new instance without a `network_interface` block, or a new network interface without an `attachment` block. A side that no rule allows is `unknown` until the resources exist.
- A reference through `var.`, `local.`, or `each.value`, even when the plan records the value.
- A plan that Terraform marks incomplete (`incomplete-plan`), and an address that is in more than one plan of the environment.

The [gaps and limits](../reference/assurance.md#gaps-and-limits) and the [known limits of this release](../reference/assurance.md#known-limits-of-this-release) list every case.

The evaluator is tested against plans that Terraform 1.17.0 recorded for `hashicorp/aws` 6.74.0, `hashicorp/google` 8.12.0, and `hashicorp/azurerm` 5.15.0, without edits. The plans and how they were recorded are in `test/fixtures/assurance/plans/real`.

## What the evaluator does not see

Some features are outside the evaluator. They do not make a result `unknown`: a result can be `allowed` or `denied` without them. The contract states these limits on every result:

- Network ACLs, route tables, and firewall appliances (AWS Network Firewall, Azure Firewall) are not evaluated.
- VPC and VNet peering, transit gateways, and private links are not evaluated.
- GCP hierarchical and network firewall policies are not evaluated.
- Rules managed outside the plan are assumed absent, not observed. A `denied` result rests on the assumption `firewall-rules-complete-in-plan` for this reason.
- A plan states what would be applied, not what is applied.

An `allowed` result therefore does not show that a packet gets through: a network ACL, a route table, or a firewall appliance can still block it. See [Gaps and limits](../reference/assurance.md#gaps-and-limits).

## Enforcement

The `effective-access` family is experimental, so a `cloud-firewall` rule is advisory. Its `FAIL` line says `advisory: does not change the exit code`. A `mandatory` entry for it in `check.enforcement` is refused with `CO-POL-018` (exit 2). See [Block network paths in the cloud](../use-cases/block-network-paths-in-the-cloud.md#make-it-block-a-merge).

## The base model of `check --changed` and `diff`

- `check --changed` and `diff` now load the base revision of the architecture with the trust vocabulary of the workspace. Before, a CALM file that used a classification declared in the workspace failed to load as base. ADR triggers then gave `unknown` (`trigger-unknown`), and `diff` dropped the trust declaration silently.
- The base model and the git-ref side of `diff` now read the URL mapping file (`architecture.urlMapping`) and the files that it maps at that revision. The worktree side of `diff` loads the workspace mapping. Before, a CALM file with a mapped control requirement failed as base, and ADR triggers gave `unknown`.
- A mapping entry that leaves the workspace, for example `../shared/req.json` in a monorepo, is not in the revision. Concordia now resolves it against the directory of the mapping file in the workspace on disk, and reads it as it is now. An absolute entry is also read as it is now.

## Export bundles

- `concordia export bundle` now adds the URL mapping file and each file that it maps to `inputs/`. A mapping entry that is absolute or leaves the workspace, or a mapped file that does not exist, is left out. The manifest `schemaVersion` stays `1.0`.
- When a side of `diff` is a bundle that holds the mapping file, the architecture loads with that mapping and the mapped files of the bundle. A bundle that an earlier release wrote loads with an empty mapping, as before: a mapped URL in its architecture does not resolve.

See [export bundle](../reference/cli.md#export-bundle) and [diff](../reference/cli.md#diff).

## Other changes in `check`

- A `GOV-ADR-006` trigger finding that only an architecture change raised now points to the architecture file (`architecture.source`), without a line. Before, it had no location. The fingerprint has no path, so baselines and waivers still match. See [ADR governance](../reference/adr-governance.md).
- The grouped pretty output now has a stable order. Groups are sorted by their ends, then by next action. In a group, findings are sorted by path, then line, then message, and a finding without a location comes last. Two copies of a workspace with different CBM project names print the same block. `results.json` keeps its own order (rule ID, then fingerprint). See [Pretty output](../reference/results.md#pretty-output).
- The reference pages now state the precedence of `--unknown` and `--minimum-coverage` more clearly. The behaviour does not change. A flag replaces the value of every rule, also a rule or a CALM control that sets its own. `check.unknown` and `check.minimumCoverage` apply only to a rule that sets no value. So `check --unknown warn` makes every rule advisory for ADR governance, and a decision with `enforcement: required` then gives a `GOV-ADR-004` violation of kind `adr-advisory-only`. See [check](../reference/cli.md#check) and [Mandatory rules](../reference/adr-governance.md#mandatory-rules).

## Documentation

- [Use cases](../README.md#use-cases): 14 new pages. Each page shows one rule on the example, the evidence that Concordia reads, how it decides, what you still declare, and how to make the rule block a merge. The groups are "Prevent outages", "Prevent security issues", "Catch drift from the architecture", and "Catch drift from assumptions and controls".
- `docs/examples/payments/` is a new example: a retail bank payments platform with planted findings in TypeScript code, a Kotlin service, Kubernetes manifests, a Terraform configuration and its plan, a decision register, and a threat model. Every output on the use-case pages comes from a run on it. The documentation no longer links to the commerce example. It stays in the repository for the tests.
- The three tutorials ([Getting started](../tutorials/getting-started.md), [Brownfield adoption](../tutorials/brownfield-adoption.md), and [A tour of Concordia](../tutorials/tour-of-concordia.md)) now use the payments example. The tour no longer has the deployment-trace, sidecar, and quality-scenario sections. Its next steps link to [trace a deployment](../how-to/trace-a-deployment.md) and [add stronger evidence](../how-to/add-stronger-evidence.md).
- The project `README.md` has a table "What Concordia catches" with a link to each use case.

## What needs your action

- Nothing changes for a network rule whose pairs bind to workloads.
- A network rule pair whose two ends bind to planned resources (`infraResource`) now goes to `cloud-firewall`. Before, such a pair was `unknown` because it had no rendered workload. Now it stays `unknown` with `no-evaluator-available` until you enable the evaluator.
- To use the evaluator, declare the plan under `assurance.plans`, and add `cloud-firewall` to `assurance.evaluators[].kinds` for the environment. Add `firewall-rules-complete-in-plan` to `assumes` only when the plans hold every firewall rule of the bound resources. Without it, a closed port is `unknown` with `enforcement-not-declared`. See [Block network paths in the cloud](../use-cases/block-network-paths-in-the-cloud.md).
- If a script parsed the order of the grouped pretty output of `check`, update it, or read `results.json` instead.
- If you compare `claimsRegistryDigest` or the registry digest of `doctor` with a stored value, update the stored value.
- Write new export bundles if you want `diff` to read their URL mapping.
