# Block network paths in the cloud

The ledger database must be reachable only from the application tier. During a debugging session, an engineer adds a security group rule that lets the public load balancer reach the database on port 5432, and calls it "temporary debug access". The rule is in the plan of the next Terraform apply. Once it is applied, the bank's ledger is one misconfiguration away from the internet, and the architecture diagram still shows no such path.

## The rule

The rule is `PAY-NET-002` in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-NET-002
  name: The public load balancer has no network path to the ledger database
  severity: error
  owner: team-platform
  rationale: The ledger database must be reachable only from the application tier. An open path from the internet-facing load balancer puts the bank's ledger one misconfiguration away from the internet.
  remediation: Remove the security group rule that admits the load balancer group to the ledger database.
  require:
    effectiveAccess:
      dimension: network
      from: {calmNodes: [public-load-balancer]}
      to: {calmNodes: [ledger-db]}
      expect: denied
      ports: [5432]
```

The rule has the same form as a Kubernetes network rule. Your bindings decide which evaluator answers it: both ends bind to planned cloud resources, so Concordia uses the `cloud-firewall` evaluator.

## What Concordia reads

- **The Terraform plan** `infra/plans/production.json`, the output of `terraform show -json`, declared in `assurance.plans`. Concordia reads the planned resources, their attributes, and the `references` lists of the plan's `configuration` section. It never reads a `constant_value`.
- **The bindings**: `public-load-balancer` binds `infraResource` `aws_lb.public`, and `ledger-db` binds `infraResource` `aws_db_instance.ledger`, both in `production`.
- **The workspace**: the evaluator `cloud-firewall` and the assumption `firewall-rules-complete-in-plan` for `production`, under `assurance.evaluators`.

Concordia does not run Terraform, and it does not read `infra/terraform/main.tf` for this rule. It does not use the CALM relationships or any value that a person wrote in YAML to decide whether the port is open.

## How Concordia decides

1. The bindings map `public-load-balancer` to `aws_lb.public` and `ledger-db` to `aws_db_instance.ledger` in the plan.
2. Concordia reads `infra/plans/production.json`, the output of `terraform show -json`. It does not run Terraform. It does not read the `.tf` files for this rule.
3. Concordia finds the security groups of each resource by ID. IDs that are unknown until apply resolve through the plan's configuration references. In the example, `aws_lb.public` has `aws_security_group.gateway`, and `aws_db_instance.ledger` has `aws_security_group.ledger_db`. The example plan is a change against the deployed estate: the groups, the load balancer, the database, and the rules `gateway_out` and `ledger_from_app` already exist, so their IDs are known values. Only the debug rule `ledger_from_gateway` is new.
4. For port 5432, Concordia checks two things. Does an egress rule on a group of the load balancer allow the database? Does an ingress rule on a group of the database allow the load balancer?
5. One open port is a violation. The result names the rules that opened it. The new rule `aws_vpc_security_group_ingress_rule.ledger_from_gateway` opens the database side. The existing egress of the gateway group appears twice: once as the inline rule `aws_security_group.gateway egress[0]` that the state records for the group, and once as its rule resource `aws_vpc_security_group_egress_rule.gateway_out`.
6. The result records the plan digest and the plan integrity.

The evaluator also reads Azure network security groups and GCP VPC firewall rules. The prefix of the resource type selects the provider. For the semantics of each provider, see [cloud-firewall](../reference/assurance.md#cloud-firewall).

## What you still declare

- **The bindings.** Concordia uses `cloud-firewall` only when both CALM elements bind to planned resources. A pair where one end binds to a planned resource and the other to a Kubernetes workload is `unknown` with the reason `binding-gap`.
- **The assumption `firewall-rules-complete-in-plan`.** A `denied` result means "no rule in this plan opens the port". It holds only if no rule for these resources exists outside the plan, for example in another Terraform state or in a change made in the console. Concordia cannot see that, so you declare it under `assurance.evaluators[].assumes`. Without it, a closed port gives `unknown` with the reason `enforcement-not-declared`. An open port needs no assumption.
- **That the plan came from this revision.** An unsigned plan is `content not verified`. The result records that integrity. To bind a plan to a revision, add a signed `plan/v1` statement (see [bind a plan to a revision](../how-to/add-stronger-evidence.md#bind-a-plan-to-a-revision)).
- **Inline rules that the plan knows.** A `denied` result needs groups whose inline rule lists are known in the plan. In a plan that creates a group, Terraform does not know the group's inline `ingress` and `egress` lists before apply, and the result is `unknown` with the gap `unknown-after-apply` until the group exists.
- **The limits from the contract.** Every result states them:
  - Network ACLs, route tables, and firewall appliances (AWS Network Firewall, Azure Firewall) are not evaluated.
  - VPC and VNet peering, transit gateways, and private links are not evaluated.
  - GCP hierarchical and network firewall policies are not evaluated.
  - Rules managed outside this plan are assumed absent, not observed.
  - A plan states what would be applied, not what is applied.
  - IPv6 ranges are not evaluated. A rule that names one gives `unknown`, unless an IPv4 rule decides first.

When the evaluator cannot decide, the result is `unknown` with a gap, for example `unknown-after-apply` for an attribute that no reference resolves, `address-dependent-range` for a CIDR that overlaps an endpoint only partly, or `unsupported-policy-kind` for an AWS prefix list. The [gaps table](../reference/assurance.md#gaps-and-limits) lists them all.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, `git`, and `jq` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/block-network-paths-in-the-cloud && mkdir -p /tmp/concordia-usecases/block-network-paths-in-the-cloud
cp -R docs/examples/payments/. /tmp/concordia-usecases/block-network-paths-in-the-cloud
cd /tmp/concordia-usecases/block-network-paths-in-the-cloud
sed -i.bak 's/payments-example/payments-uc-block-network-paths-in-the-cloud/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule PAY-NET-002 --no-color 2>/dev/null | sed -n '/^FAIL/,/Fix:/p'
```

```text
FAIL PAY-NET-002 The public load balancer has no network path to the ledger database (advisory: does not change the exit code)
  Why it matters: The ledger database must be reachable only from the application tier. An open path from the internet-facing load balancer puts the bank's ledger one misconfiguration away from the internet.
  Owner: team-platform
  public-load-balancer -> ledger-db: 1 finding(s)
    infra/plans/production.json
      open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_security_group.gateway egress[0], aws_vpc_security_group_egress_rule.gateway_out rule, aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)
  Fix: Remove the security group rule that admits the load balancer group to the ledger database.
```

The egress of the gateway group lets the load balancer send to any address. It shows as the inline `egress[0]` of the group and as the rule resource `gateway_out`. `ledger_from_gateway` lets the gateway group into the database on 5432. The other ingress rule, `ledger_from_app`, admits only `10.20.0.0/16`, which does not contain the load balancer subnet, so it is not in the list.

Read the evaluation that decided it:

```bash
jq '.assurance.evaluations[] | {query, verdict, policies, inputDigest, integrity}' build/concordia/results.json
```

```json
{
  "query": "tcp/5432 from aws_lb.public to aws_db_instance.ledger",
  "verdict": "allowed",
  "policies": [
    "aws_security_group.gateway egress[0]",
    "aws_vpc_security_group_egress_rule.gateway_out rule",
    "aws_vpc_security_group_ingress_rule.ledger_from_gateway rule"
  ],
  "inputDigest": "sha256:f39e6f816f9c0ce49e79dc6e909338b23cb34ce331a371c8145fb2d2d6539e0e",
  "integrity": "content not verified"
}
```

`inputDigest` is the SHA-256 of the plan bytes that Concordia evaluated. `integrity` says that no signed statement binds the plan. `concordia export bundle` copies the plan, so a reviewer can check the bytes against the digest later.

Now remove the debug rule, as the fix would. This `jq` command deletes every object with the rule's address, which removes it from `planned_values`, `resource_changes`, and `configuration`:

```bash
jq 'del(.. | objects | select(.address? == "aws_vpc_security_group_ingress_rule.ledger_from_gateway"))' infra/plans/production.json > plan.json && mv plan.json infra/plans/production.json
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -am "Remove the debug rule"
concordia index > /dev/null
```

```bash
concordia check --rule PAY-NET-002 --no-color 2>/dev/null | sed -n '1p'
```

```text
Conformance: pass (1 pass)
```

```bash
jq '.assurance.evaluations[] | {query, verdict, assumptions}' build/concordia/results.json
```

```json
{
  "query": "tcp/5432 from aws_lb.public to aws_db_instance.ledger",
  "verdict": "denied",
  "assumptions": [
    "firewall-rules-complete-in-plan"
  ]
}
```

The port is now `denied`, and the result rests on the declared assumption `firewall-rules-complete-in-plan`. Without that assumption in `concordia.yaml`, the same plan gives `unknown` with the reason `enforcement-not-declared`.

## Make it block a merge

This rule is advisory only. Even when it fails, the `FAIL` line says `advisory: does not change the exit code`, and the run exits with 0:

```bash
concordia check --rule PAY-NET-002 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 0
```

The contract of the `effective-access` family is experimental in this release, so the rule cannot be `mandatory`. Try it: add an enforcement entry to the `check` key, which is the last key of `concordia.yaml`, and run the check again.

```bash
cat >> concordia.yaml <<'EOF'
  enforcement:
    - { rules: [PAY-NET-002], mode: mandatory }
EOF
```

```bash
concordia check --rule PAY-NET-002 --no-color; echo "exit code: $?"
```

```text
error CO-POL-018: enforcement not valid: rule PAY-NET-002 is mandatory in production, but a guarantee that it depends on is absent: family effective-access has maturity advisory
  hint: provide the guarantee, or set the rule to `advisory` or `shadow` in `check.enforcement`; Concordia never lowers a mode itself
exit code: 2
```

Concordia refuses the run instead of quietly lowering the mode. Remove the entry again. Until the family is stable, report the finding in the pull request from `results.json` or the SARIF report, and review it there.

## Related

- [The `cloud-firewall` evaluator](../reference/assurance.md#cloud-firewall)
- [`require.effectiveAccess`](../reference/policies.md#requireeffectiveaccess)
- [`assurance.plans`](../reference/workspace.md#assuranceplans) and [`assurance.evaluators`](../reference/workspace.md#assuranceevaluators)
- [Bind a plan to a revision](../how-to/add-stronger-evidence.md#bind-a-plan-to-a-revision)
- [Evaluations in `results.json`](../reference/results.md#evaluations)
- [Block network paths in Kubernetes](block-network-paths-in-kubernetes.md)
