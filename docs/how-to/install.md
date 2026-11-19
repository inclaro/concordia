# Install Concordia

This guide shows you how to install Concordia and confirm that its toolchain works.

## Prerequisites

- Go 1.27.3 or later, to install with `go install`. A release archive does not need Go.
- git.
- [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp) (CBM). Concordia pins one CBM release: `v0.12.1`. On Linux it uses the fully static `-portable` build, as the upstream installer does. The pin, with the digests of the archive and of the binary for each platform, is in `internal/cbmpin/cbm-pin.tsv`. `concordia cbm install` installs the pinned build, and `concordia doctor` shows whether the CBM that it found is the pinned build. If an AI agent already uses CBM, read [If an AI agent already uses CBM](#if-an-ai-agent-already-uses-cbm).
- Optional: the Structurizr CLI, only to render diagrams other than DSL. See [render diagrams](render-diagrams.md).
- Optional: [cosign](https://docs.sigstore.dev/cosign/) 3, to verify signed approvals, signed waivers, and in-toto statements, and to sign them. Set `COSIGN` to its path if it is not on the `PATH`. See [review and approve assessments](review-and-approve-assessments.md) and [add stronger evidence](add-stronger-evidence.md).
- Optional: `helm` or `kustomize`, only when a source of `assurance.rendering` uses it. Pin the renderer with `concordia lock update`. See [add stronger evidence](add-stronger-evidence.md).
- Optional: `bwrap` (bubblewrap) on Linux, to run the renderers in an OS sandbox. See [assurance reference](../reference/assurance.md#confinement).

## Install

Use one of these three methods. Then install the pinned CBM build (see [Install CBM](#install-cbm)), unless the installer did it.

### Method 1: installer

The installers `install.sh` (Linux and macOS) and `install.ps1` (Windows) are assets of each GitHub release of `inclaro/concordia`. They download the public release and need no GitHub login. On Linux and macOS, they need `curl` and `tar`:

```bash
curl -fsSLO https://github.com/inclaro/concordia/releases/latest/download/install.sh && bash install.sh
```

On Windows, run in PowerShell:

```powershell
Invoke-WebRequest https://github.com/inclaro/concordia/releases/latest/download/install.ps1 -OutFile install.ps1; ./install.ps1
```

`install.sh` takes `--version <tag>` and `--dir <dir>`; `install.ps1` takes `-Version <tag>` and `-Dir <dir>`. The default version is the latest release. The default directory is `~/.local/bin`, or `%LOCALAPPDATA%\Programs\codebase-memory-mcp` on Windows, the same directory as the CBM default.

The installer:

1. Downloads the archive for your platform, `concordia_checksums.txt`, and its signature bundle.
2. Verifies the checksum file with cosign, with the Sigstore keyless signature of the release workflow, when cosign is on the `PATH`. Without cosign, it checks the checksums only and prints a warning: the checksums then prove that the download is intact, not who made it.
3. Installs `concordia` into the directory.
4. Runs `concordia cbm install --dir <dir>`, which installs the pinned CBM build.

The installer never overwrites a different CBM. When another CBM build is found, `concordia cbm install` refuses, the installer reports it, and it exits with a non-zero code after Concordia is installed. See [If an AI agent already uses CBM](#if-an-ai-agent-already-uses-cbm).

### Method 2: go install

Install with `go install`:

```bash
go install github.com/inclaro/concordia/cmd/concordia@latest
```

`go install` writes `concordia` to `$(go env GOBIN)`, or to `$(go env GOPATH)/bin` when `GOBIN` is not set. Make sure that this directory is on your `PATH`.

### Method 3: release archive

1. Open the releases page <https://github.com/inclaro/concordia/releases>.
2. Download the archive for your operating system and architecture, `concordia_checksums.txt`, and `concordia_checksums.txt.sigstore.json` from the release page. There is no key file: the release is signed keyless with the GitHub OIDC identity of the release workflow.
3. Verify the signature of the checksum file, then the archive against the checksum file. Replace `<tag>` with the release tag, for example `v0.1.0`:

   ```bash
   cosign verify-blob --bundle concordia_checksums.txt.sigstore.json --certificate-identity https://github.com/inclaro/concordia/.github/workflows/release.yml@refs/tags/<tag> --certificate-oidc-issuer https://token.actions.githubusercontent.com concordia_checksums.txt
   sha256sum --check --ignore-missing concordia_checksums.txt
   ```

   `cosign verify-blob` prints `Verified OK`. It needs network access: cosign fetches the Sigstore trusted root to check the signature's entry in the public transparency log. Without network access, the verification fails, and this is not a problem of the archive.
4. Unpack the archive and put the `concordia` executable in a directory on your `PATH`.

### Install CBM

After method 2 or 3, run:

```bash
concordia cbm install
```

The command downloads the pinned archive over HTTPS, verifies the digest of the archive and of the binary, and writes `codebase-memory-mcp` with mode 0755 to `~/.local/bin` (`%LOCALAPPDATA%\Programs\codebase-memory-mcp` on Windows). Use `--dir <dir>` for another directory. It warns when the directory is not on your `PATH`. If you prefer, install CBM yourself, but use the pinned build.

`concordia cbm install` does not configure the MCP clients of your AI agents. The upstream command `codebase-memory-mcp install` does, and Concordia never runs it. See [the CLI reference](../reference/cli.md) for the command and [`CO-OBS-005` and `CO-OBS-006`](../reference/diagnostics.md) for its errors.

### If an AI agent already uses CBM

CBM runs one daemon for each user, and it refuses a process of a different build, even with the same version number. When your agent uses another build, `concordia cbm install` refuses with `CO-OBS-005` and exit code 5, and it never replaces your file. Point your agent and Concordia at one binary. See [CBM and your AI agents](../explanation/cbm-and-agents.md).

### Development install

To change Concordia itself, clone the repository and use [mise](https://mise.jdx.dev/):

```bash
mise trust mise.toml
mise run setup
mise run install
```

`mise run setup` installs the development tools into `.bin/`, including the pinned CBM build (`.bin/codebase-memory-mcp`, task `tools:cbm`). `mise run install` builds `concordia` into `.bin/`, so that it is on the `PATH` inside `mise exec` and `mise run`. See [CONTRIBUTING.md](../../CONTRIBUTING.md).

## Confirm the install

1. Print the versions:

   ```bash
   concordia --version
   ```

   The output names the Concordia, CALM, adapter, and CBM versions.

2. Check the toolchain from inside any directory:

   ```bash
   concordia doctor
   ```

   `doctor` prints one line for each capability, with its status:

   | Capability | Meaning | Required |
   |---|---|---|
   | `workspace` | `concordia.yaml` loads and `build/` or the workspace root is writable | yes |
   | `calm-schema` | CALM schema versions (1.2, 1.3) | yes |
   | `cbm` | CBM version, whether it is the pinned build, and whether the workspace project is indexed | yes |
   | `git` | git is on the `PATH` | yes |
   | `java` | Java is on the `PATH` | no |
   | `structurizr` | Structurizr CLI found (`STRUCTURIZR_CLI`, `structurizr-cli`, `structurizr.sh`, or `structurizr`) | no |
   | `cosign` | cosign found (`COSIGN` or `cosign` on the `PATH`) | no |
   | `helm`, `kustomize` | The renderer is found, with its version and the start of its digest. Required when a declared rendering source uses it | no |
   | `network-policy` | `offline` with `--offline`, otherwise `local-only` | no |
   | `lock` | `.concordia.lock` exists and matches the installed CBM version and digest | no |

   A renderer that a declared rendering source uses, and that is missing, makes `doctor` exit 5 like a required capability.

   Each status is `available`, `degraded`, `missing`, or `incompatible`.

   A CBM that is not the pinned build gives the `cbm` capability the status `degraded`, with the detail `not the CBM build Concordia is tested with (pinned <version> sha256:<short>)`.

   `doctor` exits 5 when a required capability is `missing` or `incompatible`. Add `--strict` to also exit 5 on any `degraded` capability, or `--json` for machine-readable output.

   Outside a workspace, `workspace` shows `missing` and `doctor` exits 5. Run `doctor` again after [you set up a workspace](set-up-a-workspace.md).

## Next steps

- [Set up a workspace](set-up-a-workspace.md)
- [Getting started tutorial](../tutorials/getting-started.md)
- [`doctor` in the CLI reference](../reference/cli.md)
