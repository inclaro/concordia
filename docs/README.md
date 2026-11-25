# Concordia documentation

Concordia shows you where your systems drift from the architecture that you approved. It checks the code, the Kubernetes manifests, the Terraform plans, the decisions, and the security reviews of a repository against the CALM model and the rules of your team. It fails the build before the drift reaches production, and it says `unknown` when it cannot prove a result. **Start here:** [Getting started: your first architecture check](tutorials/getting-started.md).

## Use cases

Each page shows one rule on the bank payments example in `docs/examples/payments/`: the rule, the evidence that Concordia reads, how it decides, what you still declare, and how to make the rule block a merge.

**Prevent outages:**

- [Integrate through published APIs](use-cases/integrate-through-published-apis.md): stops a service from importing the internal code of another service, so a refactor of that code cannot break its callers.
- [Keep services free of cycles](use-cases/keep-services-free-of-cycles.md): stops dependency cycles between services, so you can deploy and roll back each service alone.
- [Keep critical paths isolated](use-cases/keep-critical-paths-isolated.md): stops a critical service from depending on a non-critical one, directly or through other services.

**Prevent security issues:**

- [Enforce trust boundaries in code](use-cases/enforce-trust-boundaries-in-code.md): stops an internet-facing service from reaching into the internals of a service that holds restricted data.
- [Protect database ownership](use-cases/protect-database-ownership.md): stops any code other than the owning service from reading or writing a database.
- [Block network paths in Kubernetes](use-cases/block-network-paths-in-kubernetes.md): finds the NetworkPolicy that opens a port between two workloads that must stay apart. The finding is advisory.
- [Block network paths in the cloud](use-cases/block-network-paths-in-the-cloud.md): finds the security group rule in a Terraform plan that opens a database to a public load balancer. The finding is advisory.
- [Limit Kubernetes permissions](use-cases/limit-kubernetes-permissions.md): finds the RoleBinding that lets one workload change another. The finding is advisory.
- [Require encrypted connections](use-cases/require-encrypted-connections.md): checks that a service calls other services over HTTPS in production. On the example the result is `unknown`, and the page shows the missing evidence.
- [Trace images to trusted builds](use-cases/trace-images-to-trusted-builds.md): stops an image without verified lineage from a trusted build from passing as traced. On the example the result is `unknown`, and the page shows the evidence to add.

**Catch drift from the architecture:**

- [Keep the model in sync with code](use-cases/keep-the-model-in-sync-with-code.md): finds code dependencies that the architecture model does not declare.
- [Require a decision for significant changes](use-cases/require-a-decision-for-significant-changes.md): stops a change that adds a significant relationship, such as a new database dependency, without an architecture decision.
- [Enforce architecture decisions](use-cases/enforce-architecture-decisions.md): finds decisions that are missing, no longer in force, or not enforced by any rule.

**Catch drift from assumptions and controls:**

- [Keep security reviews current](use-cases/keep-security-reviews-current.md): finds a security review that no longer matches the code it approved.

## Tutorials

- [Getting started](tutorials/getting-started.md): run your first architecture check on the code of a bank payments platform, and read a violation and an `unknown` result.
- [Brownfield adoption](tutorials/brownfield-adoption.md): accept the bank's existing findings in a baseline, fail only on new ones, waive the new findings until a date, and see the waiver expire.
- [A tour of Concordia](tutorials/tour-of-concordia.md): follow a refactor of the fraud risk model that broke payment authorization, find what else is waiting to break, find an open network path from the internet to the ledger database, enforce a decision, and see a security review go stale when the code changes.

## How-to guides

- [Install](how-to/install.md): install Concordia and its prerequisites.
- [Set up a workspace](how-to/set-up-a-workspace.md): create and configure `concordia.yaml`.
- [Bind code to architecture](how-to/bind-code-to-architecture.md): map code to CALM nodes.
- [Write policies](how-to/write-policies.md): express rules with each predicate.
- [Use policy packs](how-to/use-policy-packs.md): use the core pack, override rules, vendor and share packs.
- [Resolve unknown verdicts](how-to/resolve-unknown-verdicts.md): fix coverage and evidence gaps.
- [Manage baselines and waivers](how-to/manage-baselines-and-waivers.md): tolerate known findings.
- [Run in CI](how-to/run-in-ci.md): gate pull requests and publish reports.
- [Roll out new obligations](how-to/roll-out-new-obligations.md): move typed rules from shadow to advisory to mandatory, and roll back.
- [Trace a deployment](how-to/trace-a-deployment.md): follow a service to its workloads, images, build definitions, and source, and gate the infrastructure rules.
- [Render diagrams](how-to/render-diagrams.md): produce Structurizr views.
- [Keep diagrams current](how-to/keep-diagrams-current.md): declare views, commit them, and check that they are present, fresh, and match the architecture.
- [Govern architecture decisions](how-to/govern-architecture-decisions.md): register ADRs, link the rules that enforce them, replace a decision, and roll the checks out.
- [Review and approve assessments](how-to/review-and-approve-assessments.md): record an approval with `protected-review` or `signed`, revoke it, and read why a record is not counted.
- [Add stronger evidence](how-to/add-stronger-evidence.md): render manifests, supply statements and check results, read the advisory assurance rules, and gate measured quality scenarios.

## Reference

- [CLI](reference/cli.md): commands, options, exit codes, environment.
- [Workspace](reference/workspace.md): every `concordia.yaml` key.
- [Architecture](reference/architecture.md): CALM support and extensions.
- [Bindings](reference/bindings.md): the binding file format.
- [Policies](reference/policies.md): the policy pack format.
- [Core rules](reference/core-rules.md): the rules of the built-in core pack and the tags they need.
- [Infrastructure rules](reference/infrastructure-rules.md): the placement and trace rules of the built-in infrastructure pack.
- [Exceptions](reference/exceptions.md): baseline and waiver formats.
- [Results](reference/results.md): result, report, and manifest formats.
- [Capability profiles](reference/capability-profiles.md): what evidence a CBM build supplies, per family, with loss modes.
- [Redaction](reference/redaction.md): how secret values are removed, the marker, records, and limits.
- [ADR governance](reference/adr-governance.md): the decision register, lifecycle profile, presence, supersession history, obligations, and triggers.
- [Trust context](reference/trust-context.md): authority dimensions, data classification, boundary crossings, and the dependency kinds of an assessment.
- [Assessments](reference/assessments.md): assessment, assumption, approval, and revocation documents, version digests, review states, and outcomes.
- [Assurance](reference/assurance.md): the claim contracts of the assurance adapters, what each enables and does not enable, and the reasons for `unknown`.
- [Assurance rules](reference/assurance-rules.md): the rendering and provenance rules of the built-in assurance pack.
- [Diagnostics](reference/diagnostics.md): every diagnostic code.

## Explanation

- [How Concordia works](explanation/how-concordia-works.md): intent, observation, and the pipeline.
- [Verdicts and coverage](explanation/verdicts-and-coverage.md): why `unknown` is not a pass.
- [Fingerprints, baselines, and waivers](explanation/fingerprints-baselines-waivers.md): how exceptions stay stable.
- [Evidence baseline report](explanation/evidence-baseline-report.md): the gaps of the as-built evidence model, with code locations.
- [Evidence and trust](explanation/evidence-and-trust.md): why Concordia treats evidence as it does and what its trust model assumes.
- [Review currency](explanation/review-currency.md): dependency items, the digest contract, and when an old approval still counts.
- [CBM and your AI agents](explanation/cbm-and-agents.md): why Concordia and your agents must use one CBM build, and how to align them.

## Release notes

- [Evidence foundation](release-notes/evidence-foundation.md): the new evidence contract, the token meanings, the fingerprint definition, and the `core` rules whose results can change.
- [Deployment traceability](release-notes/deployment-traceability.md): the `placed-in` relation, deployment traces, and the `infrastructure` pack.
- [View currency](release-notes/view-currency.md): workspace `views`, the view checks, and the reworked deployment view.
- [ADR governance](release-notes/adr-governance.md): the decision register, the `adr-governance` pack, and the `concordia adr` commands.
- [Review currency](release-notes/review-currency.md): assessments, approvals, the `review-currency` pack, and `check --incremental`.
- [Assurance adapters](release-notes/assurance-adapters.md): rendering, plans, in-toto statements, evaluators, quality scenarios, and the `assurance` pack.
- [CBM build pin](release-notes/cbm-build-pin.md): the digest of the CBM executable in the lock, `doctor`, and `results.json`, and the warning when it differs.
- [CBM session](release-notes/cbm-session.md): one CBM process for each command, time-outs and restarts, and the message for two CBM builds.
- [CBM pinned install](release-notes/cbm-pinned-install.md): the pinned CBM build, `concordia cbm install`, the installers, and the `doctor` check.
- [Cloud firewall and use cases](release-notes/cloud-firewall-and-use-cases.md): the `cloud-firewall` evaluator for Terraform and OpenTofu plans, base-model and export-bundle fixes for `check --changed` and `diff`, a stable order of grouped `check` output, and the use-case pages on the payments example.
