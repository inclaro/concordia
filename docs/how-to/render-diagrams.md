# How to render diagrams

This guide shows you how to project your architecture and its conformance status into Structurizr DSL, and how to export diagrams in other formats.

## Prerequisites

- A workspace with a valid architecture. See [set up a workspace](set-up-a-workspace.md).
- For any format other than `dsl`: the Structurizr CLI (see [install Structurizr through Docker](#install-the-structurizr-cli-through-docker)).

## Write the DSL workspace

The `dsl` format needs no other tool. It is the default:

```bash
concordia render --format dsl
```

The command runs the check, then writes `build/concordia/structurizr/workspace.dsl` (the `projection.output` directory). Options:

| Option | Effect |
|---|---|
| `--view <view>` | Write one view: `landscape`, `context`, `container`, `component`, or `drift`. |
| `--focus <calm-id>` | Limit the views to the system or container that contains this CALM node. |
| `--results <path>` | Use an existing `results.json` instead of running the check. |
| `--workspace-out <path>` | Write the DSL to another path. |
| `--architecture <path>` | Use another CALM file. |

## Install the Structurizr CLI through Docker

1. Save this script as `structurizr` in a directory on your PATH, and make it executable (`chmod +x`):

   ```bash
   #!/usr/bin/env bash
   exec docker run --rm --network none -u "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" structurizr/cli:2025.11.09 "$@"
   ```

   - The tag is pinned because `structurizr/cli:latest` is a deprecation stub that writes no files. It is also the version that Concordia checks for committed exports (see [the pinned version](#the-pinned-version-and-confined-rendering)).
   - `--network none` gives the container no network. Concordia cannot deny the network to a process that it starts, so the wrapper is where you do that. The export needs no network.
   - `-u "$(id -u):$(id -g)"` makes the export files belong to you. Without it, they belong to root.
   - The `-v "$PWD:$PWD" -w "$PWD"` pair mounts the current directory at the same path in the container, so the paths that Concordia passes resolve.

2. On WSL without Docker Desktop integration, call `docker.exe` and convert the mount path:

   ```bash
   #!/usr/bin/env bash
   exec "/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe" run --rm --network none -u "$(id -u):$(id -g)" -v "$(wslpath -w "$PWD"):$PWD" -w "$PWD" structurizr/cli:2025.11.09 "$@"
   ```

3. Concordia looks for the CLI in this order: the `STRUCTURIZR_CLI` variable, then `structurizr-cli`, `structurizr.sh`, and `structurizr` on the PATH. To keep the script off the PATH:

   ```bash
   export STRUCTURIZR_CLI=/path/to/structurizr-wrapper
   ```

## Export a diagram format

```bash
concordia render --format mermaid
```

The formats are `mermaid`, `plantuml`, `dot`, `json`, and `static`. Concordia validates the DSL with the CLI, then exports to `build/concordia/structurizr/<format>/`. If no CLI is found, the command writes nothing and fails with `CO-PRJ-001` (`Structurizr CLI not available`) and exit code 5; its hint says to install the CLI or set `STRUCTURIZR_CLI`. If validation or export fails, the command fails with `CO-PRJ-002`. This ad-hoc flow accepts any CLI version.

## The pinned version and confined rendering

Exports of declared views are committed files, so Concordia regenerates and compares them with one CLI version: `2025.11.09`, the tag in the wrapper above. If `structurizr version` prints another version, the command fails with `CO-DGM-004` and exit code 5, and a view check reports the export as `unknown` instead of `pass`. `concordia doctor` shows the version and warns when it differs from the pin. A Concordia release can change the pin; then the exports regenerate.

A view declares one or more of these export formats: `json`, `mermaid`, `plantuml`, and `dot`. `static` (the HTML site) is not allowed, because it has no canonical form. Concordia removes the renderer metadata from the `json` export (the workspace `id`, the `structurizr.dsl` and `structurizr.inspection.*` properties, and generation fields such as `lastModifiedDate`) and writes it with sorted keys. The committed file then does not change from one run to the next.

The CLI runs confined, within these limits:

| What Concordia does | What it does not do |
|---|---|
| Runs the CLI in a fresh scratch directory and copies the output out. | Does not prevent the CLI from reading files that its user can read. |
| Passes only `PATH` and `JAVA_HOME`, with `HOME` set to the scratch directory. No proxy variable, credential, or `JAVA_TOOL_OPTIONS` reaches the CLI. | Does not deny network access. Go has no portable way to do that. Use the `--network none` wrapper. |
| Refuses `!include`, `!script`, `!plugin`, `!docs`, `!adrs`, `!import`, and URLs in the DSL before it starts the CLI (`CO-DGM-003`, exit code 2). | Does not scan anything that the CLI loads by other means. |

To let the DSL include a file, list its workspace path in `projection.allowIncludes`. The path must be relative and stay inside the workspace. URLs and inline scripts are always refused. If a listed file cannot be read, the export is `unknown` with the reason `include not available`.

## Read the conformance status

Every relationship gets a status tag, and the DSL styles it by colour and line:

| Tag | Meaning | Style |
|---|---|---|
| `concordia:conforming` | Declared and observed, with no violation. | Green, solid |
| `concordia:declared-only` | Declared, but no observed evidence. | Blue, dashed |
| `concordia:observed-only` | Observed, but not declared. | Orange, dotted |
| `concordia:violating` | Has an `active` or `expired-waiver` finding, or contradicts the architecture. | Red, thick |
| `concordia:unknown` | The evidence is not enough to decide. | Purple, dotted |

To see these differences only, render the drift view:

```bash
concordia render --view drift --format mermaid
```

## Read the placement status

The deployment view has one deployment environment for each environment that the workspace declares, named with the environment ID, in ID order. A workspace that declares no environment gets one environment named `Default`. It shows the placement of the CALM `deployed-in` relationships without status tags, as before, and it never shows observed placement, because an observed placement always belongs to a declared environment.

In a declared environment, each instance and each deployment node carries one of the same five tags as the relationships, and the DSL styles the tag on the element. Concordia decides the tag for one element in one environment from the container that CALM declares for it (`deployed-in`), the placement records of that environment, the gaps, and the coverage of `placed-in`. It reads no rule result, so the pack `infrastructure` can be off, and a waiver, a baseline, or an expiry does not change the view.

| Tag | On a placement |
|---|---|
| `concordia:conforming` | A workload of the element is placed in the declared container. This holds whatever the coverage. |
| `concordia:declared-only` | CALM declares the placement, the coverage of `placed-in` for the environment is `complete`, and no record or gap exists for the element. |
| `concordia:observed-only` | A record exists and CALM declares no container for the element. The instance shows in its observed target. |
| `concordia:violating` | A workload is placed in another container, or in the reverse direction (the workload is bound to the container and the hosting target to the element). The declared instance of the element is not `conforming`. |
| `concordia:unknown` | The declared placement has no record, and a gap names the element or the coverage is not `complete`. |

Records of another environment never count. A service that is `conforming` in `staging` and has no record in `prod` is `declared-only` or `unknown` in `prod`, depending on the coverage. Each workload is one instance, and the element takes the worst status of its instances (`violating`, `unknown`, `observed-only`, `declared-only`, `conforming`). A deployment node carries the worst status of what it deploys.

A gap is a `placed-in` fact that gives no placement: the workload or the hosting target has no binding (`binding-gap`), the bindings are ambiguous (`ambiguous-binding`), or the fact has no declared environment (`environment-unresolved`). A gap is never attached to an element by its name. It appears in the `gaps` list of the view projection with its manifest location. A gap that names a bound element also makes the declared instance of that element `unknown`.

An environment with no declared placement and no record, but with a gap that names a bound element, still gets a view. The view shows an `unknown` instance of the element under a deployment node that stands for the unresolved hosting target. The node is named with the identity of the hosting target of the gap, or `unresolved hosting target` when none is known. Concordia writes the view and prints no `CO-PRJ-008`. When no gap names a bound element (every gap is an unbound workload or has no node), the view has no element, so the DSL has no view for that environment. `CO-PRJ-008` is still not printed, and the projection lists the gaps.

## Confirm the result

`concordia render --format dsl` prints `workspace written: <path>`. For an export, it also prints `exported <format>: <directory>`, and the directory contains the diagram files.

## Next steps

- [Keep diagrams current](keep-diagrams-current.md): commit generated views and check that they stay current.
- [Run in CI](run-in-ci.md): publish the diagrams as artifacts.
- [Architecture reference](../reference/architecture.md): how CALM node types map to C4 levels.
- [CLI reference](../reference/cli.md#render)
