#!/usr/bin/env python3
"""Lint the concordia-bench task corpus without Docker or Harbor.

Usage:
    check_tasks.py datasets/concordia-bench

Exits 1 and lists every problem when any of these does not hold:
  * the task directories are exactly the tasks of bench/tasks.json;
  * each task directory has its required files, and solve.sh/test.sh are executable;
  * task.toml parses and has schema_version, [task].name, [metadata].difficulty
    and both timeouts;
  * spec.yaml loads with the verifier's own loader (unknown check types fail);
  * every mutation that a spec or a `make-branch.sh` line of a Dockerfile names
    exists in testdata/mutations/<repo>/ and has files/, derive.json or delete.txt;
  * every derive.json (of a mutation, or solution/workspace/.bench/derive.json) is a
    list of entries with a relative path, at most one of from/run, sha256 digests,
    and an existing edit script; every script under edits/ is used by an entry;
  * every rule id that a spec asserts is built in or declared by a reference
    policy file of the repository;
  * every adopt task has solution/workspace/concordia.yaml;
  * a configure task never replaces a file that its agent writes: no mutation
    that its spec applies writes a path (files/<path> or a derive.json path) that
    the reference workspace writes (solution/workspace/<path> or a .bench/derive.json path);
  * a use task (scenario `<stage>-gate`) checks that its branches are unchanged
    and reviews at least one branch that must pass.

It prints a coverage table of predicates and built-in packs. With ENFORCE_COVERAGE,
an unused predicate or pack, or a repository with fewer than MIN_RULE_IDS distinct
rule ids, is a problem too.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tomllib
from collections import defaultdict
from pathlib import Path

import yaml

BENCH = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(BENCH / "lib"))

from concordia_bench import derive  # noqa: E402
from concordia_bench.model import PREDICATES, rule_predicate  # noqa: E402
from concordia_bench.verify import load_spec  # noqa: E402

ENFORCE_COVERAGE = True
MIN_RULE_IDS = 15
# Built-in rule ids, plus the ids that the quality evaluation gives its area and scenario findings.
BUILTIN_PREFIXES = ("CORE-", "INFRA-", "ASR-", "GOV-ADR-", "REV-", "VIEW-", "quality:")
BUILTIN_PACKS = ("core", "infrastructure", "assurance", "adr-governance", "review-currency", "views")
REQUIRED_FILES = (
    "task.toml",
    "instruction.md",
    "environment/Dockerfile",
    "solution/solve.sh",
    "tests/test.sh",
    "tests/spec.yaml",
)
EXECUTABLES = ("solution/solve.sh", "tests/test.sh")
MAKE_BRANCH = re.compile(r"make-branch\.sh\s+(\S+)((?:\s+[\w.-]+)*)")
DIGEST = re.compile(r"^[0-9a-f]{64}$")
DERIVE_KEYS = {"path", "from", "run", "edit", "base_sha256", "sha256"}
WORKSPACE_SPEC = ".bench"


class Corpus:
    """The dataset, its task list and the mutation tree."""

    def __init__(self, dataset: Path, tasks: list[dict], mutations: Path):
        self.dataset = dataset
        self.tasks = tasks
        self.mutations = mutations
        self.by_name = {task_name(t): t for t in tasks}

    def workspace(self, name: str) -> Path:
        return self.dataset / name / "solution" / "workspace"


def task_name(task: dict) -> str:
    return f"{task['repo']}-{task['scenario']}"


def read_yaml(path: Path):
    try:
        return yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError):
        return None


def check_files(task: Path) -> list[str]:
    errors = [f"missing {name}" for name in REQUIRED_FILES if not (task / name).is_file()]
    for name in EXECUTABLES:
        path = task / name
        if path.is_file() and not os.access(path, os.X_OK):
            errors.append(f"{name} is not executable")
    return errors


def missing_keys(table: object, keys: tuple[str, ...], prefix: str) -> list[str]:
    if not isinstance(table, dict):
        return [f"task.toml: [{prefix}] is missing"]
    return [f"task.toml: [{prefix}] has no {key}" for key in keys if key not in table]


def check_config(task: Path) -> list[str]:
    path = task / "task.toml"
    if not path.is_file():
        return []
    try:
        config = tomllib.loads(path.read_text())
    except tomllib.TOMLDecodeError as exc:
        return [f"task.toml does not parse: {exc}"]
    errors = ["task.toml: schema_version is missing"] if "schema_version" not in config else []
    errors += missing_keys(config.get("task"), ("name",), "task")
    errors += missing_keys(config.get("metadata"), ("difficulty",), "metadata")
    errors += missing_keys(config.get("agent"), ("timeout_sec",), "agent")
    errors += missing_keys(config.get("verifier"), ("timeout_sec",), "verifier")
    return errors


def _is_relative_path(path: object) -> bool:
    if not isinstance(path, str) or not path:
        return False
    parsed = Path(path)
    return not parsed.is_absolute() and ".." not in parsed.parts


def _key_problems(entry: dict) -> list[str]:
    """Problems with the keys of a derive.json entry, each to follow `entry <index>`."""
    problems = [f" has unknown key {key!r}" for key in sorted(set(entry) - DERIVE_KEYS)]
    if not _is_relative_path(entry.get("path")):
        problems.append(" has no relative path")
    if {"from", "run"} <= entry.keys():
        problems.append(" has both from and run")
    return problems


def _value_problems(spec_dir: Path, entry: dict) -> list[str]:
    """Problems with the values of a derive.json entry, each to follow `entry <index>`."""
    problems = [f": {key} is not a string" for key in ("from", "run", "edit") if not isinstance(entry.get(key, ""), str)]
    problems += [f": {key} is not a sha256 hex digest" for key in ("base_sha256", "sha256") if not DIGEST.match(str(entry.get(key, "")))]
    edit = entry.get("edit")
    if isinstance(edit, str) and not (spec_dir / edit).is_file():
        problems.append(f": edit script {edit} does not exist")
    return problems


def _entry_errors(spec_dir: Path, index: int, entry: object) -> list[str]:
    if not isinstance(entry, dict):
        return [f"entry {index} is not an object"]
    return [f"entry {index}{problem}" for problem in _key_problems(entry) + _value_problems(spec_dir, entry)]


def _unused_edits(spec_dir: Path, entries: list) -> list[str]:
    """The edit scripts under spec_dir/edits that no entry uses."""
    edits = spec_dir / "edits"
    if not edits.is_dir():
        return []
    used = {entry.get("edit") for entry in entries if isinstance(entry, dict)}
    scripts = sorted(p.relative_to(spec_dir).as_posix() for p in edits.rglob("*") if p.is_file())
    return [f"edit script {p} is used by no entry" for p in scripts if p not in used]


def derive_errors(spec_dir: Path) -> list[str]:
    """Problems of spec_dir/derive.json (none when it does not exist), prefixed with its location."""
    spec = spec_dir / derive.SPEC
    if not spec.is_file():
        return []
    try:
        entries = json.loads(spec.read_text())
    except ValueError as exc:
        return [f"{spec}: does not parse: {exc}"]
    if not isinstance(entries, list):
        return [f"{spec}: is not a list of entries"]
    errors = [e for i, entry in enumerate(entries) for e in _entry_errors(spec_dir, i, entry)]
    errors += _unused_edits(spec_dir, entries)
    return [f"{spec}: {e}" for e in errors]


def derived_paths(spec_dir: Path) -> list[str]:
    """The paths that spec_dir/derive.json writes (empty when it is missing or malformed)."""
    try:
        return [e["path"] for e in derive.entries(spec_dir) if isinstance(e, dict) and isinstance(e.get("path"), str)]
    except (ValueError, TypeError, AttributeError):
        return []


def check_mutation(repo: str, name: str, mutations: Path) -> list[str]:
    root = mutations / repo / name
    if not (root / "message.txt").is_file():
        return [f"mutation {repo}/{name}: missing message.txt"]
    if not any((root / part).exists() for part in ("files", derive.SPEC, "delete.txt")):
        return [f"mutation {repo}/{name}: has none of files/, derive.json, delete.txt"]
    return derive_errors(root)


def spec_mutations(spec: dict) -> set[str]:
    names = set()
    for check in spec["checks"]:
        names.update(check.get("mutations") or [])
        if check.get("mutation"):
            names.add(check["mutation"])
    return names


def is_use_task(scenario: str) -> bool:
    """Use tasks gate prepared branches; every other task configures Concordia."""
    return scenario.endswith("gate")


def mutation_files(root: Path) -> list[str]:
    """The paths that a mutation writes: its files/ and its derive.json paths."""
    files = root / "files"
    copied = [str(p.relative_to(files)) for p in files.rglob("*") if p.is_file()] if files.is_dir() else []
    return sorted(set(copied) | set(derived_paths(root)))


def overwrite_errors(corpus: Corpus, name: str, spec: dict) -> list[str]:
    """A configure-task probe must be an engineer change, never a replaced agent-written file."""
    repo, workspace = corpus.by_name[name]["repo"], corpus.workspace(name)
    derived = set(derived_paths(workspace / WORKSPACE_SPEC))
    return [
        f"mutation {m} replaces {path}, which the agent writes in this task"
        for m in sorted(spec_mutations(spec))
        for path in mutation_files(corpus.mutations / repo / m)
        if path in derived or (path.split("/")[0] != WORKSPACE_SPEC and (workspace / path).exists())
    ]


def _clean_review(check: dict) -> bool:
    return check.get("expectVerdict") == "pass" and not check.get("expectViolations") and not check.get("expectErrors")


def _scored_reviews(spec: dict) -> list[dict]:
    """The branch reviews that count toward the score (not xfailed)."""
    return [c for c in spec["checks"] if c["type"] == "review_report" and c.get("branch") and not c.get("xfail")]


def _review_coverage_errors(scored: list[dict]) -> list[str]:
    """Scored reviews must include a clean branch and a branch with a finding or a refusal.

    Otherwise an agent that marks every branch `fail`, or every branch clean, scores full marks.
    A review of a branch that a Concordia defect breaks keeps the correct expectation and an xfail.
    """
    clean = [_clean_review(c) for c in scored]
    errors = [] if any(clean) else ["use task scores no review of a branch that must pass clean"]
    return errors + ([] if not all(clean) else ["use task scores no review of a branch with a finding or a refusal"])


def use_task_errors(spec: dict) -> list[str]:
    has_branch_check = any(c["type"] == "branches_unchanged" for c in spec["checks"])
    errors = [] if has_branch_check else ["use task has no branches_unchanged check"]
    return errors + _review_coverage_errors(_scored_reviews(spec))


def dockerfile_mutations(task: Path) -> set[str]:
    path = task / "environment" / "Dockerfile"
    text = path.read_text() if path.is_file() else ""
    return {name for match in MAKE_BRANCH.finditer(text) for name in match.group(2).split()}


def _item_rule_ids(check: dict) -> set[str]:
    items = (check.get("findings") or []) + (check.get("absent") or [])
    items += [c.get("match") or {} for c in check.get("counts") or []]
    return {i["ruleId"] for i in items if isinstance(i.get("ruleId"), str)}


def _rules_key_ids(rules) -> set[str]:
    if isinstance(rules, dict):  # results: {ruleId: verdict}
        return set(rules)
    return {r["id"] for r in rules or [] if isinstance(r, dict) and "id" in r}  # rules_declared


def _check_rule_ids(check: dict) -> set[str]:
    ids = _item_rule_ids(check) | _rules_key_ids(check.get("rules"))
    return ids | ({check["ruleId"]} if check.get("ruleId") else set())


def asserted_rule_ids(spec: dict) -> set[str]:
    """The rule ids that the checks of a spec assert on."""
    return {rule for check in spec["checks"] for rule in _check_rule_ids(check)}


def policy_rules(root: Path) -> list[dict]:
    rules = []
    for path in sorted(root.glob("policies/*.yaml")):
        doc = read_yaml(path)
        if isinstance(doc, dict):
            rules += [r for r in doc.get("rules") or [] if isinstance(r, dict)]
    return rules


def stage_chain(corpus: Corpus, name: str) -> list[str]:
    """The task, the tasks of its stage images, and the adopt task of its repository."""
    task = corpus.by_name[name]
    chain = [name, f"{task['repo']}-adopt"]
    stage = task.get("stage") or ""
    while stage not in ("", "adopted") and f"{task['repo']}-{stage}" not in chain:
        chain.append(f"{task['repo']}-{stage}")
        stage = corpus.by_name.get(chain[-1], {}).get("stage") or ""
    return chain


def declared_rule_ids(corpus: Corpus, name: str) -> set[str]:
    repo = corpus.by_name[name]["repo"]
    roots = [corpus.workspace(n) for n in stage_chain(corpus, name)]
    roots += [p / "files" for p in sorted((corpus.mutations / repo).glob("*"))]
    return {r.get("id") for root in roots for r in policy_rules(root)}


def check_rule_ids(corpus: Corpus, name: str, spec: dict) -> list[str]:
    declared = declared_rule_ids(corpus, name)
    return [
        f"spec.yaml: rule {rule} is neither built in nor declared by a reference policy"
        for rule in sorted(asserted_rule_ids(spec))
        if not rule.startswith(BUILTIN_PREFIXES) and rule not in declared
    ]


def load_task_spec(task: Path) -> tuple[dict | None, list[str]]:
    path = task / "tests" / "spec.yaml"
    if not path.is_file():
        return None, []
    try:
        return load_spec(path), []
    except Exception as exc:  # the loader raises ValueError, yaml errors, OSError
        return None, [f"spec.yaml: {exc}"]


def is_stage_gate(corpus: Corpus, entry: dict) -> bool:
    """A `<stage>-gate` task: reviews prepared branches on the reference state of another scenario of its repo."""
    stage = entry["scenario"].removesuffix("-gate")
    return stage != entry["scenario"] and f"{entry['repo']}-{stage}" in corpus.by_name


def check_spec(corpus: Corpus, name: str, spec: dict) -> list[str]:
    entry = corpus.by_name[name]
    errors = []
    if spec.get("repo") != entry["repo"]:
        errors.append(f"spec.yaml: repo {spec.get('repo')!r} does not match task repo {entry['repo']!r}")
    for mutation in sorted(spec_mutations(spec)):
        errors += check_mutation(entry["repo"], mutation, corpus.mutations)
    if is_stage_gate(corpus, entry):
        errors += use_task_errors(spec)
    elif not is_use_task(entry["scenario"]):
        errors += overwrite_errors(corpus, name, spec)
    return errors + check_rule_ids(corpus, name, spec)


def check_task(corpus: Corpus, name: str) -> tuple[list[str], dict | None]:
    task, entry = corpus.dataset / name, corpus.by_name[name]
    errors = check_files(task) + check_config(task)
    spec, spec_errors = load_task_spec(task)
    errors += spec_errors + (check_spec(corpus, name, spec) if spec else [])
    errors += [e for m in sorted(dockerfile_mutations(task)) for e in check_mutation(entry["repo"], m, corpus.mutations)]
    errors += derive_errors(corpus.workspace(name) / WORKSPACE_SPEC)
    if entry["scenario"] == "adopt" and not (corpus.workspace(name) / "concordia.yaml").is_file():
        errors.append("adopt task has no solution/workspace/concordia.yaml")
    return errors, spec


def check_directories(corpus: Corpus, repo: str | None = None) -> list[str]:
    present = {p.name for p in corpus.dataset.iterdir() if p.is_dir() and (repo is None or p.name.startswith(f"{repo}-"))}
    listed = set(corpus.by_name)
    errors = [f"{name}: listed in tasks.json but the directory is missing" for name in sorted(listed - present)]
    return errors + [f"{name}: directory is not listed in tasks.json" for name in sorted(present - listed)]


def workspace_packs(root: Path) -> set[str]:
    doc = read_yaml(root / "concordia.yaml")
    if not isinstance(doc, dict):
        return set()
    packs = {str(p.get("use", "")).removeprefix("builtin:") for p in (doc.get("policies") or {}).get("packs") or [] if isinstance(p, dict)}
    return packs | ({"views"} if doc.get("views") else set())


def coverage(corpus: Corpus) -> dict[str, set[str]]:
    """Predicate and pack name to the repositories whose reference workspaces use it."""
    used: dict[str, set[str]] = defaultdict(set)
    for name, task in corpus.by_name.items():
        root = corpus.workspace(name)
        for rule in policy_rules(root):
            used[rule_predicate(rule) or "?"].add(task["repo"])
        for pack in workspace_packs(root):
            used[f"builtin:{pack}"].add(task["repo"])
    return used


def coverage_rows() -> list[str]:
    return [*PREDICATES, *(f"builtin:{p}" for p in BUILTIN_PACKS)]


def print_coverage(used: dict[str, set[str]]) -> None:
    width = max(len(row) for row in coverage_rows())
    print("coverage:")
    for row in coverage_rows():
        print(f"  {row:<{width}}  {', '.join(sorted(used.get(row, ()))) or '-'}")


def pack_errors(used: dict[str, set[str]]) -> list[str]:
    return [f"coverage: {row} is used by no task" for row in coverage_rows() if not used.get(row)]


# Finding ids that the quality evaluation generates: valid in specs, but they are not rules.
GENERATED_PREFIXES = ("quality:",)


def rule_id_errors(rule_ids: dict[str, set[str]]) -> list[str]:
    """Each repository must assert at least MIN_RULE_IDS distinct rule ids (generated finding ids do not count)."""
    counted = {repo: {i for i in ids if not i.startswith(GENERATED_PREFIXES)} for repo, ids in rule_ids.items()}
    return [
        f"coverage: {repo} asserts {len(ids)} distinct rule ids, want at least {MIN_RULE_IDS}"
        for repo, ids in sorted(counted.items())
        if len(ids) < MIN_RULE_IDS
    ]


def check_dataset(corpus: Corpus, repo: str | None = None) -> list[str]:
    """All problems; with `repo`, only that repository's tasks and its rule-id minimum."""
    errors = check_directories(corpus, repo)
    rule_ids: dict[str, set[str]] = defaultdict(set)
    for name in corpus.by_name:
        if not (corpus.dataset / name).is_dir():
            continue
        task_errors, spec = check_task(corpus, name)
        errors += [f"{name}: {e}" for e in task_errors]
        if spec:
            rule_ids[corpus.by_name[name]["repo"]].update(asserted_rule_ids(spec))
    used = coverage(corpus)
    print_coverage(used)
    if repo:
        return errors + rule_id_errors(rule_ids)
    return errors + (pack_errors(used) + rule_id_errors(rule_ids) if ENFORCE_COVERAGE else [])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("dataset", type=Path, help="dataset directory, e.g. datasets/concordia-bench")
    parser.add_argument("--repo", help="lint only the tasks of this repository")
    args = parser.parse_args(argv)
    tasks = [t for t in json.loads((BENCH / "tasks.json").read_text()) if args.repo in (None, t["repo"])]
    corpus = Corpus(args.dataset, tasks, BENCH / "testdata" / "mutations")
    errors = check_dataset(corpus, args.repo)
    for error in errors:
        print(f"FAIL {error}", file=sys.stderr)
    if errors:
        print(f"{len(errors)} problem(s) in {args.dataset}", file=sys.stderr)
        return 1
    print(f"ok: {len(tasks)} tasks in {args.dataset}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
