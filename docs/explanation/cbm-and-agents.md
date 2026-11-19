# CBM and your AI agents

Many people run codebase-memory-mcp (CBM) in an AI agent, such as an editor or a coding assistant that is an MCP client, and run Concordia on the same machine. This page explains why both must use one CBM binary, how to check it, and how to align them. For the install steps, see [install](../how-to/install.md).

## One daemon for each user

CBM runs one daemon for each OS user. Every CBM process of that user, whether an agent starts it or Concordia does, talks to that daemon. A process of a different build than the running daemon is refused. The message looks like this:

```text
a conflicting CBM process is active ... point every client at one binary
```

CBM then waits for about 30 s before it fails. Concordia reports it as `CO-OBS-001`, with the end of the CBM message, and does not retry. See [CBM session](../release-notes/cbm-session.md#two-cbm-builds).

The rule is about the build, not the version number. Two files that both report `0.12.1` conflict when their bytes differ. On Linux, CBM publishes two builds of each release: a standard build (dynamically linked) and a `-portable` build (fully static). They are different builds. The upstream installer uses the `-portable` build on Linux, and so does Concordia's pin.

You cannot isolate Concordia from the agent. Setting `CBM_CACHE_DIR`, `HOME`, `XDG_RUNTIME_DIR`, or `XDG_CACHE_HOME` does not give a second daemon. So the CBM that Concordia uses must be byte-identical to the CBM that your agents use, whenever both run at the same time.

A CI runner has no agent daemon, so this does not apply there. See [run in CI](../how-to/run-in-ci.md).

## The pinned build

Concordia pins one CBM release, with the SHA-256 of the archive and of the binary for each platform. The pin is in `internal/cbmpin/cbm-pin.tsv`. `concordia cbm install` installs that build, and `concordia doctor` tells you if the executable it found is that build. The build is the one that Concordia is tested with.

## Check what you use

1. List every CBM on your `PATH`. The first one wins:

   ```bash
   which -a codebase-memory-mcp
   ```

2. Read the MCP configuration of each agent. Note the `command` that starts CBM. It can be an absolute path that is not the first one on your `PATH`.
3. Ask Concordia which file it uses, and its digest:

   ```bash
   concordia doctor
   ```

   The `cbm` line shows `path` and `sha256`. Compare with the digest of each file that an agent uses:

   ```bash
   sha256sum /path/to/codebase-memory-mcp
   ```

   Equal digests mean one build. `doctor` also says whether the file is the pinned build.

A global tool manager can change the result. For example, a global mise configuration can put another CBM first on the `PATH` in some shells and not in others. Check in the same environment that the agent and Concordia use.

## Align the builds

Point every agent configuration and Concordia at one binary. Use one of these ways:

- Install the pinned build with `concordia cbm install`. Then set the `command` in each agent's MCP configuration to that file. If another build is already on your `PATH` or in the install directory, `concordia cbm install` refuses with `CO-OBS-005` and does not overwrite it. Stop the agents, replace or remove the other build yourself, and run the command again.
- If the CBM of your agent is the pinned build (equal digest), set `observation.executable` in `concordia.yaml` to that file, or leave it as it is when the file is first on the `PATH`.

`concordia cbm install` does not configure agent MCP clients. The upstream command `codebase-memory-mcp install` does: it writes the configuration of your agents. Concordia never runs it. You choose which agent uses which file.

## Upgrades

Upgrade the CBM of your agents and the pin of Concordia together. The command `codebase-memory-mcp update` replaces the binary with a new build. After it, the agent and Concordia can use different builds, and `doctor` shows `degraded`. A new Concordia release moves the pin when it is tested with a newer CBM. See [CBM pinned install](../release-notes/cbm-pinned-install.md). Then run `concordia lock update` to record the new build in the lock, see [CBM build pin](../release-notes/cbm-build-pin.md).

## What Concordia does in your CBM

- Concordia runs its CBM session in an empty directory in the user cache (`~/.cache/concordia/cbm-session` on Linux). CBM indexes the working directory of a session when it starts, so a session in your repository would index it. In the empty directory, Concordia never indexes your repository by accident. Concordia indexes only when you run `concordia index`.
- Because there is one daemon, the projects that Concordia creates are visible in the `list_projects` call of your agent. They are normal CBM projects.
- `concordia cbm install` writes only the binary. It does not change any agent configuration.

## Related

- [Install](../how-to/install.md)
- [CBM session release notes](../release-notes/cbm-session.md)
- [`CO-OBS-005` and `CO-OBS-006`](../reference/diagnostics.md)
- [How Concordia works](how-concordia-works.md)
