# CBM build pin release notes

This release records the build of the codebase-memory-mcp (CBM) executable that Concordia runs, and checks it against the lock. Two builds can report the same version number and differ. The version and the capability profile did not tell them apart. This page lists what a user can see change.

## The digest of the CBM executable

Concordia finds the executable that `observation.executable` names (default `codebase-memory-mcp`) as a shell does, resolves symbolic links, and hashes the file once for each connection. The digest has the form `sha256:<hex>`.

- `.concordia.lock` gains an optional `resolved.cbm.sha256`. `concordia init` and `concordia lock update` write it. A value that is not `sha256:` and 64 lower-case hex digits gives `CO-WRK-002` (exit 2). See [the lock file](../reference/workspace.md#concordialock).
- `concordia doctor` shows the resolved path and the digest of the `cbm` capability: a line `executable <path> sha256:<hex>` in the text output, and the keys `path` and `sha256` in the JSON. See [doctor](../reference/cli.md#doctor).
- `concordia --version` is unchanged: the last line is `cbm <version>` (or `cbm missing`). The digest is shown by `doctor`.
- `results.json` gains the optional key `toolchain.cbm.sha256`, with the same value in the run manifest. The evaluation bundle has it as `provenance.graph.cbmSha256`. `schemaVersion` stays `1.1`. See [results](../reference/results.md).

## A different build gives a warning

A lock whose `resolved.cbm.sha256` differs from the executable that is found gives the warning `CO-WRK-003`, as a different CBM version does. The message names the resolved path, the digest that was found, and the digest in the lock, and tells you to run `concordia lock update`. `doctor` shows the `lock` capability as `degraded` with the same facts.

- The warning never stops a run and never changes an exit code. This is the rule for the CBM version, not the stricter rule for renderers (`CO-PRV-003`).
- A lock without `sha256` gives no warning. It promised nothing about the executable.
- `lock update` lists the change in its [digest-impact report](../reference/cli.md#digest-impact-report) as the component `cbm-executable`. A lock that has no digest yet counts as `unrecorded`, so the first `lock update` after this release writes the report once.

## What changes in the commands

- `lock update` now finds the CBM version and the digest from the executable that the workspace names, not always from `codebase-memory-mcp` on the `PATH`.
- A test double of the CBM client has no executable file, so a run with one has no digest and no warning.

## What needs your action

- Run `concordia lock update` once to pin the build that you use. Commit `.concordia.lock`.
- When CI uses another CBM build than your machine, pin the one that CI uses, or expect the warning there.
