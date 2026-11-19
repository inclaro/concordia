# CBM pinned install release notes

This release pins one codebase-memory-mcp (CBM) release build and adds a command to install it. Concordia and your AI agents must use one CBM build, so the pin and the install are now part of Concordia. This page lists what a user can see change.

## The pinned build

- Concordia is tested with CBM `v0.12.1`. The release, and the SHA-256 of the archive and of the binary for each platform, are in `internal/cbmpin/cbm-pin.tsv`. Linux uses the static `-portable` build, as the upstream installer does.
- `concordia doctor` compares the digest of the CBM that it found with the pin. A different build gives the `cbm` capability the status `degraded` and the detail `not the CBM build Concordia is tested with (pinned <version> sha256:<short>)`. An equal digest says that it is the pinned build. There is no new diagnostic code for `doctor`.

## The command `concordia cbm install`

- `concordia cbm install [--dir <dir>]` downloads the pinned archive over HTTPS, verifies the archive digest and the binary digest, and writes the binary to the directory with mode 0755. The default directory is `~/.local/bin` on Linux and macOS, and `%LOCALAPPDATA%\Programs\codebase-memory-mcp` on Windows. It warns when the directory is not on the `PATH`.
- If the pinned build is already installed, the command says so and exits 0.
- If another CBM is found, the command refuses. It never overwrites and has no force option. It gives `CO-OBS-005` (exit 5), with the found path and digest and the pinned version and digest.
- A download or verification failure gives `CO-OBS-006` (exit 6). The environment variable `CONCORDIA_CBM_DOWNLOAD_URL` replaces the base URL, for mirrors and tests.
- The command does not run `codebase-memory-mcp install`, so it does not configure your agents. See [CBM and your AI agents](../explanation/cbm-and-agents.md).

## Installers

The release has `install.sh` (Linux and macOS) and `install.ps1` (Windows). They download Concordia from the public GitHub release, verify the checksum file with cosign when cosign is on the `PATH`, install Concordia, and then run `concordia cbm install`. See [install](../how-to/install.md).

## For developers

- `mise run tools:cbm` installs the pinned build into `.bin/`. `mise run setup` runs it, and `test-e2e`, `record-cbm`, and `docs-verify` depend on it.
- `mise run cbm:pin <tag>` regenerates `cbm-pin.tsv` from a release. It verifies `checksums.txt` with cosign and needs `gh` and `.bin/cosign`.

## What needs your action

- If an AI agent already uses CBM, check that it uses the pinned build. See [CBM and your AI agents](../explanation/cbm-and-agents.md).
- In CI, install CBM with `concordia cbm install`. See [run in CI](../how-to/run-in-ci.md).
