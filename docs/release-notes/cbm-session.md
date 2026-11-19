# CBM session release notes

This release changes how Concordia talks to codebase-memory-mcp (CBM). A command now starts one CBM process and sends every call to it, instead of starting a process for each call. This page lists what a user can see change.

## One CBM process for each command

- A Concordia command starts at most one CBM process for its tool calls, plus one `--version` call. The process starts on the first call and ends when the command ends. `watch` keeps one process for all its runs.
- Each call is faster by about 0.4 s, the start time of a CBM process. A command with ten calls saves about 4 s.
- Concordia runs CBM as an MCP server on stdio (`codebase-memory-mcp` with no arguments). Results, bodies, and the error codes `CO-OBS-001`, `CO-OBS-002`, and `CO-OBS-003` are the same as before.
- The CBM process runs in an empty directory in the user cache, `concordia/cbm-session`, and not in your repository. CBM indexes the working directory of a session when it starts (its `auto_index` setting is on by default), so a process that ran in your repository would index it. The first session creates one empty CBM project for that directory; later sessions reuse it.
- `--trace` writes the event `tools/call <tool>` for each call. It was `cli <tool>`.

## Time-outs and failures

- `observation.timeoutSeconds` and `observation.indexTimeoutSeconds` still limit one call. They now include the start of the process when the call needs one.
- A call that times out, a process that exits, and an answer that Concordia cannot read give `CO-OBS-001`, with the end of the stderr of CBM. Concordia kills that process, and the next call starts a new one.
- `observation.retries` still repeats read-only calls (`index_status`, `get_graph_schema`, `query_graph`, `list_projects`). Each repeat after a broken session uses a new process. `index_repository` and the other write tools are never repeated.

## Two CBM builds

CBM refuses to run next to a CBM daemon of another build, and it then waits for about 30 s before it fails. Concordia now says why: `CO-OBS-001` with a detail that the executable is a different build than the running CBM daemon, and that every CBM client must use one binary, followed by the end of the CBM message. Concordia does not retry this failure. Point `observation.executable` and the other CBM clients (for example your editor) at the same file. `concordia doctor` shows the digest of the executable that it found.

## For developers

- `observation.ProcessClient` has `Close()` and `Executable()`. The client of a command is closed when the command ends. `cmd/record-cbm` closes its clients too.
- `procexec.Start` runs a program with open pipes for stdin and stdout and keeps the end of its stderr. It is the base of the session.
- A test that needs a real CBM can run `CONCORDIA_REAL_CBM=<path> go test ./internal/observation -run TestProcessClientRealCBM`. It reads only and never indexes.
