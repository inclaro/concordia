#!/usr/bin/env python3
"""Summarise a Harbor job of the concordia-bench corpus.

Reads every ``<job-dir>/*/result.json`` (one per trial) and prints a table per
task, then the mean ``reward`` per repository and per scenario. A trial without
a verifier reward (agent or environment error) counts as 0.0 in the means.

Usage:
    analyze_results.py jobs/<job-name>
    analyze_results.py --latest jobs [--csv jobs/latest.csv] [--require 1.0]

With ``--require X`` the exit status is 1 when any trial has no ``reward`` or a
``reward`` below X, or when the job has no trials at all.
"""

from __future__ import annotations

import argparse
import csv
import json
import sys
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path

from tabulate import tabulate

BENCH = Path(__file__).resolve().parent.parent
DIMENSIONS = ("workspace", "conformance", "detection", "deployment", "assurance", "governance", "reporting", "determinism")
SCORES = ("reward", *DIMENSIONS)
FLOAT_FORMAT = ".4f"


def load_task_index(path: Path = BENCH / "tasks.json") -> dict[str, tuple[str, str]]:
    """Task directory name to (repo, scenario), from bench/tasks.json."""
    return {f"{t['repo']}-{t['scenario']}": (t["repo"], t["scenario"]) for t in json.loads(path.read_text())}


TASKS = load_task_index()


@dataclass
class Trial:
    """One agent attempt at one task, as recorded in a trial result.json."""

    task: str
    agent: str
    model: str
    started_at: str
    rewards: dict[str, float]
    attempt: int = 0

    @property
    def repo(self) -> str:
        return TASKS.get(self.task, (self.task, ""))[0]

    @property
    def scenario(self) -> str:
        return TASKS.get(self.task, ("", "unknown"))[1]

    @property
    def reward(self) -> float | None:
        return self.rewards.get("reward")


def task_name(result: dict) -> str:
    """Task directory name, e.g. ``fzf-adopt``."""
    path = (result.get("task_id") or {}).get("path")
    if path:
        return Path(path).name
    return str(result.get("task_name", "unknown")).rsplit("/", 1)[-1]


def _section(result: dict, key: str) -> dict:
    """A nested object of a result.json; missing or null sections read as empty."""
    return result.get(key) or {}


def parse_trial(result: dict) -> Trial:
    rewards = _section(_section(result, "verifier_result"), "rewards")
    agent_config = _section(_section(result, "config"), "agent")
    return Trial(
        task=task_name(result),
        agent=_section(result, "agent_info").get("name") or "unknown",
        model=agent_config.get("model_name") or "",
        started_at=result.get("started_at") or "",
        rewards={key: float(value) for key, value in rewards.items()},
    )


def number_attempts(trials: list[Trial]) -> None:
    """Number attempts per (agent, model, task) by start time, from 1."""
    groups: dict[tuple[str, str, str], list[Trial]] = defaultdict(list)
    for trial in trials:
        groups[(trial.agent, trial.model, trial.task)].append(trial)
    for group in groups.values():
        group.sort(key=lambda trial: trial.started_at)
        for index, trial in enumerate(group, start=1):
            trial.attempt = index


def load_trials(job_dir: Path) -> list[Trial]:
    trials = [
        parse_trial(json.loads(path.read_text()))
        for path in sorted(job_dir.glob("*/result.json"))
    ]
    number_attempts(trials)
    trials.sort(key=lambda t: (t.agent, t.model, t.task, t.attempt))
    return trials


def latest_job(jobs_dir: Path) -> Path:
    """Most recently modified job directory under ``jobs_dir``."""
    jobs = [path for path in jobs_dir.iterdir() if path.is_dir()]
    if not jobs:
        raise SystemExit(f"no job directories in {jobs_dir}")
    return max(jobs, key=lambda path: path.stat().st_mtime)


def trial_row(trial: Trial) -> list:
    return [
        trial.agent,
        trial.model,
        trial.task,
        trial.attempt,
        *(trial.rewards.get(name) for name in SCORES),
    ]


def trial_table(trials: list[Trial]) -> str:
    headers = ["agent", "model", "task", "attempt", *SCORES]
    rows = [trial_row(trial) for trial in trials]
    return tabulate(
        rows, headers=headers, floatfmt=FLOAT_FORMAT, missingval="", disable_numparse=False
    )


def mean_rows(trials: list[Trial], key) -> list[list]:
    """Mean reward per (agent, model, key(trial)); a missing reward is 0.0."""
    groups: dict[tuple[str, str, str], list[float]] = defaultdict(list)
    for trial in trials:
        groups[(trial.agent, trial.model, key(trial))].append(trial.reward or 0.0)
    return [
        [agent, model, label, len(values), sum(values) / len(values)]
        for (agent, model, label), values in sorted(groups.items())
    ]


def mean_table(trials: list[Trial], label: str, key) -> str:
    headers = ["agent", "model", label, "trials", "mean reward"]
    return tabulate(mean_rows(trials, key), headers=headers, floatfmt=FLOAT_FORMAT)


def _mean_or_empty(values: list[float]):
    return sum(values) / len(values) if values else ""


def dimension_rows(trials: list[Trial]) -> list[list]:
    """Mean of each dimension per (agent, model) over the trials that score it; empty when none does."""
    groups: dict[tuple[str, str], list[Trial]] = defaultdict(list)
    for trial in trials:
        groups[(trial.agent, trial.model)].append(trial)
    return [
        [agent, model, *(_mean_or_empty([t.rewards[d] for t in group if d in t.rewards]) for d in DIMENSIONS)]
        for (agent, model), group in sorted(groups.items())
    ]


def dimension_table(trials: list[Trial]) -> str:
    return tabulate(dimension_rows(trials), headers=["agent", "model", *DIMENSIONS], floatfmt=FLOAT_FORMAT)


def render_report(trials: list[Trial]) -> str:
    sections = [
        trial_table(trials),
        mean_table(trials, "repo", lambda trial: trial.repo),
        mean_table(trials, "scenario", lambda trial: trial.scenario),
        dimension_table(trials),
    ]
    return "\n\n".join(sections)


def csv_row(trial: Trial) -> list:
    scores = [trial.rewards.get(name, "") for name in SCORES]
    return [trial.agent, trial.model, trial.task, trial.repo, trial.scenario, trial.attempt, *scores]


def write_csv(trials: list[Trial], path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["agent", "model", "task", "repo", "scenario", "attempt", *SCORES])
        writer.writerows(csv_row(trial) for trial in trials)


def failing_trials(trials: list[Trial], minimum: float) -> list[Trial]:
    return [t for t in trials if t.reward is None or t.reward < minimum]


def describe(trial: Trial) -> str:
    reward = "no reward" if trial.reward is None else f"reward {trial.reward:.4f}"
    return f"{trial.agent} {trial.task} attempt {trial.attempt}: {reward}"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("job_dir", nargs="?", type=Path, help="Harbor job directory")
    source.add_argument("--latest", type=Path, metavar="JOBS_DIR", help="use the newest job in JOBS_DIR")
    parser.add_argument("--csv", type=Path, help="also write a CSV file")
    parser.add_argument("--require", type=float, metavar="MIN", help="exit 1 if any trial reward is below MIN")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    job_dir = latest_job(args.latest) if args.latest else args.job_dir
    trials = load_trials(job_dir)
    print(f"job: {job_dir}")
    if trials:
        print(render_report(trials))
    if args.csv:
        write_csv(trials, args.csv)
    if args.require is None:
        return 0
    bad = failing_trials(trials, args.require) if trials else []
    if not trials:
        print("no trials found", file=sys.stderr)
    for trial in bad:
        print(f"FAIL {describe(trial)}", file=sys.stderr)
    return 1 if bad or not trials else 0


if __name__ == "__main__":
    sys.exit(main())
