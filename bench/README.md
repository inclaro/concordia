# concordia-bench

concordia-bench is an end-to-end acceptance test of Concordia. Each task gives a coding agent a developer ticket for one of ten real open-source repositories (`repos.json`), inside a Harbor (`harbor` 0.25.0) container. The agent has only the ticket, Concordia's documentation and `--help`. A verifier then runs Concordia on the real code and checks the documented findings, verdicts, unknowns and reports.

The tasks (`tasks.json`) cover every rule predicate, every built-in pack, typed deployment obligations, assurance, ADR governance, review currency, views, trust context, exceptions, enforcement roll-out, reporting and CI.

The benchmark is not a model leaderboard. A high agent score is the desired outcome.

## Task types

Concordia serves two roles in a team, and the tasks test both.

| Type | Role | Starting folder | Agent's job | Verifier checks |
| --- | --- | --- | --- | --- |
| Configure | Architect setting up the gate | The repository, with the reference configuration of earlier stages | Turn the ticket's intent into Concordia configuration | Concordia's results on the agent's own configuration, at HEAD and after engineer changes (mutations) |
| Use (`<stage>-gate`, `gate`, `pr-gate`) | Engineer or reviewer using the gate | The repository with the reference configuration on `main`, and teammate branches | Gate each branch with Concordia and write a review (and waivers, where the ticket asks) | The review matches what Concordia must report on each branch, and the branches are unchanged |

A configure-task mutation is an engineer change: it adds or changes code or manifest files that the agent did not write. It never replaces a configuration file that the agent wrote, because the check would then grade the reference configuration instead of the agent's (`check_tasks.py` rejects it). A change to the Concordia configuration itself, such as an ADR edit, a relaxed rule or a new reviewer, is tested in a use task, where the configuration on `main` is the reference one.

## Runs

| Command | What it proves |
| --- | --- |
| `mise run bench:smoke [--repo <id>]` | Every task's reference solution scores 1.0: Concordia gives the documented results on real code with real configuration. No LLM. A task below 1.0 is a regression. |
| `mise run bench:run [--repo <id>]` | A real agent harness (terminus-2) reaches a working configuration from the ticket and the docs alone. Needs `OPENROUTER_API_KEY` in `bench/.env`. |
| `mise run bench:check` | The corpus is consistent and covers every predicate and built-in pack. |
| `mise run bench:test` | The verifier library tests pass. |

`mise run bench:analyze` prints the mean reward per task, repository, scenario and reward dimension.

## No upstream code in this repository

The bench stores no code of the ten repositories that it tests. `Dockerfile.repo` clones each repository at the commit pinned in `repos.json` when the image is built. You are responsible for complying with the licenses of the repositories that you clone.

Some mutations and reference workspaces start from an upstream file: a changed upstream source file, a chart rendered with `helm template`, a copied manifest. The bench does not store that file. It stores a recipe in `derive.json` that rebuilds the file from the pinned checkout when the file is needed:

```json
[{"path": "bench-env/x.yaml", "from": "manifests/install.yaml", "edit": "edits/bench-env/x.yaml.ed", "base_sha256": "…", "sha256": "…"}]
```

- `from` names a file of the working tree to start from (default: `path`). `run` instead names a bash command that runs in the repository root, and its stdout is the base.
- `edit` names an ed script (`diff -e base target`). An ed script holds line addresses and the new lines only, never the upstream lines that it replaces.
- `base_sha256` and `sha256` pin the base and the result. A mismatch fails the build: the base is not the pinned content that the entry was made from.

Entries apply in order. A mutation keeps its `derive.json` and `edits/` next to `message.txt`. The runner copies `files/`, applies `derive.json`, deletes the `delete.txt` paths, and commits. A reference workspace keeps them in `solution/workspace/.bench/`. `lib/bin/copy-workspace.sh` copies the workspace without `.bench/` and then applies its `derive.json`. `apply-workspace.sh` and `adopt-workspace.sh` call it.

To author an entry, start from the state that the entry applies to: the repository as the task image has it, plus the earlier mutations of the same branch. Then write the target file and run:

```bash
PYTHONPATH=lib python3 -m concordia_bench.derive make <spec-dir> <path> <target-file> [--from SRC | --run CMD] --app <repo-checkout>
```

`make` writes the ed script, adds or replaces the entry, and checks that the entry rebuilds the target. Make `run` entries inside the task image, which has `helm`, `kustomize` and `/opt/venv/bin/python`. `python -m concordia_bench.derive apply <spec-dir> <app>` rebuilds the files and prints their paths. `mise run bench:check` validates every `derive.json`.

`python3 scripts/check_vendored.py [--upstream DIR] [--clone]` enforces the rule. It reads the upstream checkouts in `DIR` (default `$BENCH_UPSTREAM` or `/tmp/bench-full`, one directory per repository id); `--clone` makes a shallow clone of a missing checkout at its pinned SHA. It fails when 30% or more of the significant lines of a bench file (stripped lines of 25 characters or more, and at least 5 of them) occur in the checkout of that file's repository.

## Triage of agent trials below 1.0

Every agent trial below 1.0 has one of four causes. Find it from the trial's `verifier/reward-details.json`, the configuration that the agent left in `verifier/artifacts/`, the Concordia output, and the docs page that the ticket names.

| Cause | Meaning | Action |
| --- | --- | --- |
| Concordia defect | Concordia behaves differently from its documentation on real input. | Record it in [DEFECTS.md](DEFECTS.md) and fix Concordia. While the fix is pending, mark the check `xfail` with a message that starts with the defect ID. Do not work around the defect in tickets or reference solutions. |
| Docs or usability gap | The agent made a reasonable attempt, and the docs, `--help` or the error messages did not lead it to a working configuration. | Record it in [DEFECTS.md](DEFECTS.md) and fix the docs or the error message. |
| Benchmark defect | The ticket and the verifier disagree, or the verifier rejects a configuration that meets the ticket. | Fix the ticket or the spec. Accept every equivalent encoding: a `rules_declared` predicate and a finding `kind` may be lists. |
| Agent mistake | The docs and the ticket state the requirement clearly, and the agent ignored or misread it. | None. |

Do not lower an assertion to make a trial pass. A spec may accept an alternative only when that alternative meets the ticket.

[DEFECTS.md](DEFECTS.md) lists the open defects, the benchmark defects still to fix, and the triage of the latest agent baseline.
