You are a developer on the `browser-use` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the structure rules for browser-use

Concordia already guards the module dependencies of browser-use (`policies/browser-use-adopt.policy.yaml`, pack `browser-use-adopt`). The architecture team now wants three stricter structure rules in a second pack. Documentation: `/opt/concordia/docs/reference/policies.md`, `exceptions.md`, `results.md`.

## Workspace

- New policy pack `browser-use-structure` (version `1.0.0`) at `policies/browser-use-structure.policy.yaml`. Register it in `concordia.yaml` next to the existing pack. Every rule has severity `error` and owner `team-browser-use`.
- Existing violations of the new rules in the current code are recorded in the baseline `exceptions/concordia.baseline.json`, not fixed. Do not change the architecture model or the bindings.

## Rules

- `BU-IND-001` (name: "Peer adapters do not depend on each other"): the browser, LLM, tools, skills and telemetry nodes are peer adapters and must not depend on each other, whether by imports or by calls. Calls that the index resolves only heuristically count too. This prevents a distributed monolith.
- `BU-FBD-001` (name: "The REPL uses the browser only through its facade"): the REPL node must not import the browser node, except for the two public files of the browser package: `browser_use/browser/__init__.py` and `browser_use/browser/browser.py`. It counts imports only. Importing browser internals is a leaky abstraction.
- `BU-OWN-001` (name: "Only the entry point, the agent and the LLM adapters read credentials"): only the CLI, agent and LLM nodes may import the credentials node. The credential store is a sensitive module whose access must stay concentrated (data ownership). It counts imports only.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
