# Contributing

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By taking part, you agree to follow it.

## Prerequisites

Development is supported natively on Windows, Linux, WSL, and macOS. Mise automatically runs PowerShell on Windows and Bash on Unix, so all documented project commands are identical across platforms.

## Set up the repository

Clone your repository, review and trust its mise config, then install the pinned repository-local development tools:

```console
mise trust mise.toml
mise run setup
```

All project-installed runtimes and executables live beneath the gitignored `.bin/` directory. `mise run tools:install` (part of `setup`) pins Go, Node, the linters, `cosign`, `helm`, and `kustomize` there. A few assurance tests run the real `cosign`, `helm`, and `kustomize` from `.bin/` (or the `PATH`) and are skipped when the tool is missing; the other signing tests use a fake `cosign`. Use `mise run`, `mise exec -- <command>`, or an activated mise shell to supply their PATH entries.

`mise run tools:cbm` (also part of `setup`) installs the pinned `codebase-memory-mcp` (CBM) build into `.bin/codebase-memory-mcp`, after it checks the SHA-256 of the archive and of the binary. The pin is in `internal/cbmpin/cbm-pin.tsv`; the `concordia` binary, `doctor`, and `concordia cbm install` read the same file. `test-e2e`, `record-cbm`, and `docs-verify` run `tools:cbm` first. The task does not run `codebase-memory-mcp install`, so it never changes the MCP clients of your AI agents. CBM runs one daemon for each user and refuses a different build, so an agent that uses another CBM build conflicts with the tests while both run; see [CBM and your AI agents](docs/explanation/cbm-and-agents.md). To pin a new CBM release, run `mise run cbm:pin <tag>` (needs `gh`; Linux and macOS). It verifies the release signature with `.bin/cosign` and rewrites the pin file.

To install Concordia itself from a release, use `install.sh` (Linux, macOS) or `install.ps1` (Windows). Both download the public release without a GitHub login, verify the release with `cosign` when it is on the `PATH`, and then run `concordia cbm install`. A release attaches both scripts.

Confirm the environment before changing code:

```console
mise tasks ls
mise run ci
```

## Make a change

Keep changes focused and use the standard library unless a dependency is necessary. Add tests for observable behavior, boundaries, or regressions rather than implementation details.

Format and validate the project before opening a pull request:

```console
mise run fmt
mise run ci
```

Use `mise run lint-fix` only when you intend to apply automatic lint fixes. CI never modifies source files.

## Run the end-to-end tests and verify the docs

`mise run test-e2e` sets `CONCORDIA_E2E=1` and needs `codebase-memory-mcp` on the `PATH`; without it the e2e tests skip or fail. Run it when you change indexing, evidence, or any command that reads the code graph. `mise run record-cbm` records the CBM calls of the fixtures again.

The tutorials are `showboat` documents. After changing a command, an output, or a tutorial, run `mise run docs-verify` (needs `codebase-memory-mcp` and `showboat`; not available on Windows). `mise run markdownlint` checks `README.md`, `CONTRIBUTING.md`, and `docs/`, except the tutorials. Docs examples are also checked by Go tests, so `mise run test` catches a YAML example that no longer validates.

## Specify a change

Open an issue to discuss a larger change before you write it. Update the pages under `docs/` in the same pull request as the behavior they describe.

## Keep `model validate` in parity with the CALM CLI

`concordia model validate` gives the results of `calm validate` of the [CALM CLI](https://github.com/finos/architecture-as-code). `test/parity/` holds the cases and the recorded CALM CLI results; `go test ./test/parity/` (part of `mise run test`) compares Concordia with them and needs no Node.js. To record again, on Linux or macOS with network access:

```console
mise run calm:update        # pin the latest cli-v* release in test/parity/upstream.json and build it
mise run calm:test-upstream # run the CALM CLI tests
mise run test-parity        # record the CALM CLI results and compare
```

`mise run calm:record` imports the upstream test fixtures, writes each `expected.json`, and syncs the CALM schemas. A case for a new rule goes in `test/parity/cases/concordia/<rule ID>/` (runs `fail` and `pass`); a run that cannot be compared goes in `test/parity/exclusions.json` with its reason.

The `CALM CLI parity` workflow compares Concordia with the recorded results on every pull request. Every week it also records the latest CALM CLI and opens a pull request when the results change. It opens that pull request with the `CALM_PARITY_TOKEN` repository secret, a fine-grained personal access token with contents and pull-requests write access, so CI runs on it. (The workflows of a pull request that the default `GITHUB_TOKEN` opens wait for a maintainer's approval; a stored GitHub App installation token expires after an hour.)

Workflows pin each action to a full commit SHA, with the release tag in a trailing comment. To update an action, resolve the new tag to its commit (`git ls-remote --tags https://github.com/<owner>/<action>`) and change both.

## Commit and submit

Use conventional commit messages so semantic version calculation remains predictable. Open a pull request with:

- the problem and chosen solution;
- the commands used for verification;
- release or compatibility impact, when applicable.

Do not commit `dist/`, `.tmp/`, `.bin/`, `mise.local.toml`, credentials, or local environment files.

## Releases

Maintainers create releases by pushing a `v*` tag. See the [release instructions](./README.md#versions-and-releases). Releases use GoReleaser (OSS) and a keyless Sigstore signature from the GitHub OIDC identity of the release workflow, so the release needs no signing key or license secret.

## License

When you contribute, you agree that your contributions are licensed under the Apache License 2.0 (section 5 of the license).
