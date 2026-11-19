You are a developer on the `browser-use` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on browser-use

The architecture team wants Concordia to guard the module structure of browser-use. Set up the Concordia workspace from the architecture description below. Concordia documentation to read: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/browser-use.calm.json`. Give every node a title-case `name` (for example `Bu Cli`) and a `description` (the role below). Put the tags in the node's `concordia.tags`. Name each relationship `<source>-uses-<destination>`.
- Observation: one project named `browser-use` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes: `browser_use/**/*.py`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one.
- Policy pack `browser-use-adopt` (version `1.0.0`) at `policies/browser-use-adopt.policy.yaml`. It holds only the rules below, each with severity `error` and owner `team-browser-use`. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `bu-cli` | service | - | `browser_use/cli.py`, `browser_use/__main__.py` | Command-line entry point. |
| `bu-mcp` | service | - | `browser_use/mcp/**` | MCP server and client. |
| `bu-agent` | service | - | `browser_use/agent/**` | Agent loop and prompts. |
| `bu-tools` | service | - | `browser_use/tools.py`, `browser_use/search.py` | Tool registry and actions. |
| `bu-repl` | service | - | `browser_use/repl/**` | Interactive REPL. |
| `bu-skills` | service | - | `browser_use/skills/**` | Reusable skills. |
| `bu-browser` | service | - | `browser_use/browser/**` | Browser session and DOM control. |
| `bu-llm` | service | - | `browser_use/llm/**` | LLM provider adapters. |
| `bu-telemetry` | service | - | `browser_use/telemetry/**` | Product telemetry. |
| `bu-credentials` | service | - | `browser_use/auth.py` | Credential store and API keys. |
| `bu-shared` | service | `shared` | `browser_use/*.py` | Shared configuration and utilities. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second. These are all the dependencies that exist in the code today.

- `bu-agent` uses `bu-browser`
- `bu-agent` uses `bu-llm`
- `bu-agent` uses `bu-tools`
- `bu-agent` uses `bu-repl`
- `bu-agent` uses `bu-telemetry`
- `bu-agent` uses `bu-credentials`
- `bu-agent` uses `bu-shared`
- `bu-llm` uses `bu-credentials`
- `bu-llm` uses `bu-shared`
- `bu-tools` uses `bu-shared`
- `bu-credentials` uses `bu-shared`
- `bu-cli` uses `bu-credentials`
- `bu-cli` uses `bu-shared`
- `bu-cli` uses `bu-telemetry`
- `bu-skills` uses `bu-shared`
- `bu-repl` uses `bu-shared`
- `bu-browser` uses `bu-shared`
- `bu-telemetry` uses `bu-shared`
- `bu-repl` uses `bu-telemetry`
- `bu-mcp` uses `bu-telemetry`
- `bu-mcp` uses `bu-shared`
- `bu-mcp` uses `bu-repl`
- `bu-mcp` uses `bu-credentials`
- `bu-mcp` uses `bu-browser`
- `bu-mcp` uses `bu-cli`
- `bu-shared` uses `bu-credentials`
- `bu-browser` uses `bu-credentials`

## Rules

- `BU-DEC-001` (name: "Module dependencies are declared in the architecture"): every import dependency between two services must be declared as a relationship in the architecture. Drift between code and model is the anti-pattern (architecture drift).
- `BU-ALW-001` (name: "The agent loop uses only its adapters and shared code"): the agent loop may import only the browser, LLM, tools, REPL, telemetry, credentials and shared nodes, and nothing else. The skills and the entry points must stay out of its reach (a missing anti-corruption layer otherwise). Both rules count import dependencies only.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- `concordia check` exits 0 on the committed HEAD of `main` without any baselined finding.
- Everything is committed on `main`.
