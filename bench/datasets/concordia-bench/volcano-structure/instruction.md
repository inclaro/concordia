You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the structure rules to Volcano

Concordia is already adopted on Volcano (`concordia check` exits 0). The scheduler team now wants two structural guards in a second policy pack.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `results.md`, `exceptions.md`.

## Policy

Add the pack `volcano-structure` (version `1.0.0`) at `policies/volcano-structure.policy.yaml`. Both rules have severity `error`, owner `team-volcano`, a one-sentence rationale and a remediation. The existing nodes, bindings and the `volcano-adopt` pack stay as they are.

- `VC-DNY-001` "Admission cannot reach scheduling logic": no chain of imports or calls, of up to three steps, may lead from the admission node `vc-admission` to the scheduler node `vc-scheduler` or to any of the six plugin nodes (`vc-plugin-gang`, `vc-plugin-drf`, `vc-plugin-proportion`, `vc-plugin-binpack`, `vc-plugin-predicates`, `vc-plugin-capacity`). A chain that passes through other code, bound or not, counts. Imports and calls both count.
- `VC-BUD-001` "The framework hub does not gain dependents": the scheduler framework node `vc-framework` is a hub that several components already use (this rule watches the admission component only, because the daemons that register HTTP routes make Concordia report binding gaps that this budget cannot judge). Count the imports and calls from `vc-admission` to `vc-framework` that exist today, and let the rule tolerate exactly that number, so that one more such dependency is reported. Concordia reports the count when a budget is exceeded: run the rule with a budget of 0 first to read it. Imports and calls both count. With exactly the current count as the budget the rule has no violation left to report, and Concordia then reports it as unknown, because some of the admission calls end on HTTP route nodes that bind to no architecture node. Do not bind those route nodes: configure the rule so that an unknown result is reported as a warning and does not fail the check.

## Acceptance criteria

- The new rules are declared and valid.
- Existing violations that the new rules find are recorded in the baseline `exceptions/concordia.baseline.json`, not fixed. The baseline must be regenerated so it also holds every earlier finding.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
