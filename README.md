# Concordia

Concordia is a command-line tool that keeps your systems in line with the architecture that you approved. It compares a CALM 1.2 or 1.3 architecture model with the code graph that Codebase Memory (CBM) builds, with the Kubernetes manifests and Terraform plans of each environment, and with your decisions and security reviews. It fails the build when a change breaks a rule, and it reports `unknown` when it cannot prove a result. You can adopt it on existing code by accepting today's violations and failing only on new ones.

## What Concordia catches

Each use case runs on a bank payments example (`docs/examples/payments/`).

|Problem|Use case|
|---|---|
|A service imports the internal code of another service, and a refactor there breaks payments.|[Integrate through published APIs](docs/use-cases/integrate-through-published-apis.md)|
|Two services depend on each other, so you cannot deploy or roll back one alone.|[Keep services free of cycles](docs/use-cases/keep-services-free-of-cycles.md)|
|A critical service depends on a non-critical one, so a minor outage stops payments.|[Keep critical paths isolated](docs/use-cases/keep-critical-paths-isolated.md)|
|An internet-facing service reaches into a service that holds restricted data.|[Enforce trust boundaries in code](docs/use-cases/enforce-trust-boundaries-in-code.md)|
|Code outside the owning service reads or writes the ledger database.|[Protect database ownership](docs/use-cases/protect-database-ownership.md)|
|A NetworkPolicy opens a port between workloads that must stay apart (advisory).|[Block network paths in Kubernetes](docs/use-cases/block-network-paths-in-kubernetes.md)|
|A security group rule in a Terraform plan opens the database to a public load balancer (advisory).|[Block network paths in the cloud](docs/use-cases/block-network-paths-in-the-cloud.md)|
|A RoleBinding lets one workload change another (advisory).|[Limit Kubernetes permissions](docs/use-cases/limit-kubernetes-permissions.md)|
|A service calls another service in production without HTTPS. The example shows an `unknown` result and the evidence that is missing.|[Require encrypted connections](docs/use-cases/require-encrypted-connections.md)|
|A deployed image has no verified lineage from a trusted build. The example shows an `unknown` result and the evidence to add.|[Trace images to trusted builds](docs/use-cases/trace-images-to-trusted-builds.md)|
|The code has dependencies that the architecture model does not declare.|[Keep the model in sync with code](docs/use-cases/keep-the-model-in-sync-with-code.md)|
|A change adds a significant relationship without an architecture decision.|[Require a decision for significant changes](docs/use-cases/require-a-decision-for-significant-changes.md)|
|An accepted decision is missing, no longer in force, or not enforced by a rule.|[Enforce architecture decisions](docs/use-cases/enforce-architecture-decisions.md)|
|A security review still counts after the code it approved has changed.|[Keep security reviews current](docs/use-cases/keep-security-reviews-current.md)|

## Quickstart

Install (needs git and CBM on the PATH; see [install](docs/how-to/install.md)). Use one of two methods:

- With Go 1.27.3 or later:

  ```bash
  go install github.com/inclaro/concordia/cmd/concordia@latest
  ```

- Or download a release archive from <https://github.com/inclaro/concordia/releases>, unpack it, and put `concordia` on your `PATH`. Or use the installer `install.sh` (Linux, macOS) or `install.ps1` (Windows) of the release; see [install](docs/how-to/install.md#method-1-installer).

Then, in the repository you want to check:

```bash
concordia init     # write concordia.yaml, a CALM model, bindings, and a policy pack
concordia index    # build and freeze the code graph
concordia check    # evaluate the rules; exit code 0 means no failures
```

Continue with [Getting started: your first architecture check](docs/tutorials/getting-started.md).

## Policy packs

Concordia ships these built-in packs. Run `concordia policy list` to see them, and `concordia policy add builtin:<name>` to enable one. Or start a workspace with `concordia init --preset core`.

|Pack|What it checks|Rules|
|---|---|---|
|`core`|Cycles, undeclared dependencies, layering, trust boundaries, drift|[Core rules](docs/reference/core-rules.md)|
|`infrastructure`|Deployment placement and traces from service to source|[Infrastructure rules](docs/reference/infrastructure-rules.md)|
|`adr-governance`|Decision register, lifecycle, and enforcement|[ADR governance](docs/reference/adr-governance.md)|
|`review-currency`|Approvals, dispositions, and capture of assessments|[Assessments](docs/reference/assessments.md)|
|`assurance`|Rendering and provenance of stronger evidence|[Assurance rules](docs/reference/assurance-rules.md)|

The `views` pack applies on its own when the workspace declares `views`; do not add it. You can tune single rules with overrides, and vendor shared or community packs as local files that are pinned by a sha256 digest (`concordia policy add <file-or-https-url>`). `check` never uses the network. See [Use policy packs](docs/how-to/use-policy-packs.md).

## Documentation

|Section|What it is|
|---|---|
|[Tutorials](docs/README.md#tutorials)|Learn by running Concordia on a sample project|
|[How-to guides](docs/README.md#how-to-guides)|Steps for specific tasks|
|[Reference](docs/README.md#reference)|Commands, file formats, and diagnostic codes|
|[Explanation](docs/README.md#explanation)|Why Concordia works the way it does|

## Development

Development uses [mise](https://mise.jdx.dev/). Run `mise trust mise.toml` and `mise run setup` once. `mise run tools:install` (part of `setup`) installs the pinned Go, Node, linters, and also `cosign`, `helm`, and `kustomize` into `.bin/`; the end-to-end and assurance tests use them. `mise run tools:cbm` (also part of `setup`) installs the pinned `codebase-memory-mcp` build into `.bin/`. To use Concordia without developing it, run `install.sh` (Linux, macOS) or `install.ps1` (Windows) from a release; they also run `concordia cbm install`. See [Install Concordia](docs/how-to/install.md). List every task with `mise tasks ls`. The main tasks are:

| Task | Purpose |
| --- | --- |
| `mise run fmt` | Format Go source with gofumpt, goimports, and gci policy |
| `mise run lint` | Verify the lint configuration and run strict Go linting |
| `mise run test` | Run race-enabled, shuffled tests |
| `mise run test-reports` | Write JSON, text, HTML, and atomic coverage reports under `dist/coverage/` |
| `mise run actionlint` | Validate GitHub Actions workflows |
| `mise run test-parity` | Record the CALM CLI results and compare Concordia with them (needs network) |
| `mise run tidy` | Tidy the application module |
| `mise run update-deps` | Update direct dependencies within their current major versions |
| `mise run markdownlint` | Validate README, contributor documentation, and `docs/` (not the tutorials) |
| `mise run security` | Run `govulncheck` over reachable code |
| `mise run build` | Build the local executable into `dist/` |
| `mise run install` | Install `concordia` into `.bin/`, so that it is on the mise `PATH` |
| `mise run tools:cbm` | Install the pinned `codebase-memory-mcp` build into `.bin/` (checks the SHA-256 of the archive and of the binary) |
| `mise run cbm:pin <tag>` | Pin another CBM release in `internal/cbmpin/cbm-pin.tsv` (needs `gh` and `.bin/cosign`; Linux and macOS) |
| `mise run test-e2e` | Run the end-to-end tests with the pinned `codebase-memory-mcp` from `.bin/` |
| `mise run record-cbm` | Record the CBM calls of the commerce and infra fixtures again (uses the pinned `codebase-memory-mcp`) |
| `mise run docs-verify` | Verify the tutorials with `showboat` (uses the pinned `codebase-memory-mcp`; needs `showboat`) |
| `mise run bench:smoke [--repo <id>]` | Run the reference solutions of the [concordia-bench](bench/README.md) tasks on ten real repositories (needs Docker; Linux and macOS) |
| `mise run ci` | Run all ordinary CI checks without release secrets |
| `mise run release:check` | Validate GoReleaser configuration |
| `mise run snapshot` | Build unpublished release artifacts locally |

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow.

## Versions and releases

Use conventional commits so `svu` can determine semantic versions. Inspect the current version with `mise run version`. `mise run tag` requires a clean Git working tree, computes the next version once, creates that tag, and pushes that exact tag.

Publishing is triggered only by `v*` tags. Releases need no repository secrets. GoReleaser signs the checksum file keyless with the GitHub OIDC identity of the workflow (Sigstore). It publishes a draft. The workflow verifies the signature and then makes the release public. `CODECOV_TOKEN` is optional. Only the scheduled CALM parity workflow uses `CALM_PARITY_TOKEN` (see CONTRIBUTING.md).

`mise run snapshot` builds unsigned artifacts. To verify a release, see [install](docs/how-to/install.md#method-3-release-archive).

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
