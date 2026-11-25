# Recorded Terraform plans

Each directory holds a Terraform configuration and the plan that `terraform show -json` printed for it. Unlike the
handwritten fixtures next to this directory, these files were not edited after recording, so they show the real shape
of a plan. `internal/assurance/evaluate/cloudfw/realplans_test.go` runs queries against them.

No plan was made with a real cloud account. No configuration was applied to a real cloud.

| Directory | Provider | Recorded with | How |
|---|---|---|---|
| `aws-create` | `hashicorp/aws` 6.74.0 | Terraform 1.17.0 | Create-only plan, offline. Fake static keys from `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, with `skip_credentials_validation`, `skip_requesting_account_id`, `skip_metadata_api_check`, and `skip_region_validation`. No data sources. |
| `aws-state` | `hashicorp/aws` 6.74.0 | Terraform 1.17.0 | Plan against existing state. `applied/main.tf` was applied to LocalStack 4.12.0 community edition in docker, with the EC2 and STS endpoints set to `http://localhost:4566` and the keys `test`/`test`. Then `main.tf` changed three rules (see below), and `terraform plan -out` and `terraform show -json` recorded the plan. |
| `gcp-create` | `hashicorp/google` 8.12.0 | Terraform 1.17.0 | Create-only plan, offline. A fake token in `GOOGLE_OAUTH_ACCESS_TOKEN`, and the project `concordia-fake-project`. |
| `azure-create` | `hashicorp/azurerm` 5.15.0 | Terraform 1.17.0 | Create-only plan, offline. `resource_provider_registrations = "none"`. The token came from a local fake managed-identity endpoint (`ARM_USE_MSI=true`, `ARM_MSI_ENDPOINT=http://127.0.0.1:50342/...`) that returned an unsigned token with the zero tenant and subscription IDs. All other HTTPS went to a closed proxy port, so the plan made no call to Azure. |
| `aws-dynamic` | `hashicorp/aws` 6.74.0 | Terraform 1.17.0 | Create-only plan, offline, as `aws-create`. `dynamic "ingress"` blocks over a known list, a list with an element unknown until apply, a list of unknown length, and a known empty list, and `dynamic "network_interface"` blocks of `aws_instance`. |
| `aws-mixed` | `hashicorp/aws` 6.74.0 | Terraform 1.17.0 | Create-only plan, offline, as `aws-create`. Inline `ingress` lists with static blocks only, a static and a dynamic block, and a dynamic block only, each naming groups whose IDs are unknown until apply. |

The `.terraform.lock.hcl` file of each directory pins the provider build.

## What the `dynamic` recordings show

- The `configuration` section of a plan does not show a `dynamic` block at all: neither its key nor the references of its content. `aws_security_group.dyn_known` has only `name` and `vpc_id` in its expressions.
- An inline list written only by a dynamic block over a known empty list (`dyn_empty`) is unknown until apply, the same as a group without blocks (`plain`).
- A dynamic block over a list of unknown length gives one placeholder `ingress` element with unknown fields (`dyn_unknown_length`), and a wholly unknown `network_interface` list on `aws_instance` (`dyn_nic_unknown_length`), the same as an instance without the block (`plain`).
- Static inline blocks share one `references` list under `ingress`. A static block and a dynamic block (`mixed`) give the references of the static block only, so the plan looks the same as two static blocks that name one group.

## The `aws-state` change

The plan against state makes these changes to the applied configuration:

- It deletes `aws_vpc_security_group_ingress_rule.ledger_from_gateway`.
- It adds `aws_vpc_security_group_ingress_rule.ledger_from_ops`.
- It updates `aws_vpc_security_group_ingress_rule.ops_ssh` from `10.20.0.0/16` to `10.20.1.0/25`.

The plan also has updates that come from the configuration itself, not from the change:

- `aws_security_group.ledger_db` has inline `ingress` blocks and a standalone rule. The state copies the standalone rule into the inline list, so every plan removes it again.
- `aws_network_interface.ops` lists one group in `security_groups`, and `aws_network_interface_sg_attachment.ops` attaches a second one. Every plan removes the attached group again.
- `aws_instance.app` gets `vpc_security_group_ids` again, because LocalStack does not return it.

These setups differ from `aws-create`, because LocalStack community cannot create everything:

- LocalStack community 4.12 has no RDS, ELBv2, or ElastiCache. So `aws_lb`, `aws_db_instance`, `aws_db_subnet_group`, and `aws_elasticache_cluster` are not in `aws-state`.
- `aws_network_interface_attachment` fails: the provider waits with the `DescribeNetworkInterfaces` filter `attachment.attachment-id`, which LocalStack does not implement. The secondary interface uses the `attachment` block of `aws_network_interface` instead.
- Burstable instance types (`t3.micro`) never finish: LocalStack pages `DescribeInstanceCreditSpecifications` forever. The instances use `m5.large`.
- The newer `localstack/localstack:latest` image needs an auth token, so the recording used the `4.12` tag.

## Identifiers

No field was rewritten. The recorded files contain no real account ID, subscription ID, tenant ID, object ID, token, or secret:

- `aws-create` and `gcp-create` hold no account ID. The credentials came from the environment, so they are not in the `configuration` section.
- `aws-state` holds IDs that LocalStack generated (`vpc-…`, `sg-…`, `eni-…`, `i-…`, `vol-…`) and LocalStack's fixed fake account `000000000000` in `owner_id` and ARNs. Some ARNs have an empty account field. `referenced_security_group_id` in the prior state reads `000000000000/sg-…`: the provider adds the owner account because `skip_requesting_account_id` leaves its own account ID empty. With real credentials, a group of the same account has no prefix.
- `gcp-create` holds the fake project `concordia-fake-project` and the service account emails `app@concordia-fake-project.iam.gserviceaccount.com` and `ledger@concordia-fake-project.iam.gserviceaccount.com`.
- `azure-create` holds no subscription, tenant, or object ID: every ID of a new resource is unknown until apply. The SSH public key of the VM is a throwaway key whose private half was deleted.

## Record a plan again

Run these commands in a copy of a directory, with the credentials described above, and never with real ones:

```sh
terraform init
terraform plan -out=plan.out
terraform show -json plan.out > plan.json
```

A new recording has a new `timestamp`, and new LocalStack IDs in `aws-state`. Update the tests when the queries depend on those values.
