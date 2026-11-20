"""Checks on the adopted workspace: files, model, bindings, rules, workflow."""

from __future__ import annotations

import os
from pathlib import Path

from . import model
from .outcome import exit_result, result


def _recorded_branches(record: Path) -> dict[str, str]:
    """`<branch> <commit>` lines that make-branch.sh wrote at image build."""
    lines = record.read_text(encoding="utf-8").splitlines() if record.is_file() else []
    return dict(line.split(" ", 1) for line in lines if " " in line)


def branches_unchanged(ctx, check):
    record = Path(os.environ.get("CONCORDIA_BENCH_STATE", "/opt/concordia-bench")) / "branches.tsv"
    want = _recorded_branches(record)
    if not want:
        return result([f"no branches recorded in {record}"], "")
    problems = []
    for branch, commit in sorted(want.items()):
        now = ctx.run(["git", "rev-parse", "--verify", "--quiet", f"refs/heads/{branch}"]).stdout.strip()
        if now != commit:
            problems.append(f"branch {branch} is at {now or 'nothing'}, built at {commit}")
    return result(problems, f"{len(want)} branches as built")


def _branch_problems(ctx, branch: str) -> list[str]:
    head = ctx.run(["git", "rev-parse", "--abbrev-ref", "HEAD"]).stdout.strip()
    return [] if head == branch else [f"on branch {head!r}, want {branch!r}"]


def _dirty_problems(ctx, paths: list[str]) -> list[str]:
    dirty = ctx.run(["git", "status", "--porcelain", "--ignored", "--", *paths]).stdout.strip()
    return [f"uncommitted or ignored files: {dirty}"] if dirty else []


def _untracked_problems(ctx, paths: list[str]) -> list[str]:
    absent = [p for p in paths if not ctx.run(["git", "ls-tree", "-r", "--name-only", "HEAD", "--", p]).stdout.strip()]
    return [f"not in HEAD: {', '.join(absent)}"] if absent else []


def committed(ctx, check):
    paths = check["paths"]
    problems = _branch_problems(ctx, check["branch"]) + _dirty_problems(ctx, paths) + _untracked_problems(ctx, paths)
    return result(problems, f"{', '.join(paths)} committed on {check['branch']}")


def model_valid(ctx, check):
    return exit_result(ctx.run(["concordia", "model", "validate", "--format", "json"]), "model valid")


def calm_nodes(ctx, check):
    by_id = {n.get("unique-id"): n for n in model.calm_document(ctx).get("nodes") or []}
    problems = [p for spec in check["nodes"] for p in model.node_problems(by_id.get(spec["id"]), spec)]
    return result(problems, f"{len(check['nodes'])} nodes as specified")


def relationships(ctx, check):
    pairs = model.connect_pairs(model.calm_document(ctx))
    problems = [f"missing connects {s} -> {d}" for s, d in check["connects"] if (s, d) not in pairs]
    return result(problems, f"{len(check['connects'])} relationships present")


def bound_nodes(ctx, check):
    # bind validate resolves against the observed graph, which must match HEAD.
    ctx.run(["concordia", "index"])
    done = ctx.run(["concordia", "bind", "validate"])
    if done.exit != 0:
        return exit_result(done, "")
    bound = model.bound_ids(ctx)
    problems = [f"node {i} is not bound" for i in check["ids"] if i not in bound]
    return result(problems, f"{len(check['ids'])} nodes bound")


def _predicate_problem(rule: dict, spec: dict) -> list[str]:
    """A spec predicate list means any of these equivalent encodings."""
    want = spec.get("predicate")
    if want is None:
        return []
    got = model.rule_predicate(rule)
    accepted = want if isinstance(want, list) else [want]
    return [] if got in accepted else [f"rule {spec['id']} predicate {got!r}, want {want!r}"]


def _rule_problems(rule: dict | None, spec: dict) -> list[str]:
    if rule is None:
        return [f"rule {spec['id']} not declared"]
    problems = _predicate_problem(rule, spec)
    for key in ("severity", "family", "enforcement"):
        if key in spec and rule.get(key) != spec[key]:
            problems.append(f"rule {spec['id']} {key} {rule.get(key)!r}, want {spec[key]!r}")
    return problems


def rules_declared(ctx, check):
    rules = model.declared_rules(ctx)
    problems = [p for spec in check.get("rules") or [] for p in _rule_problems(rules.get(spec["id"]), spec)]
    packs = model.pack_uses(ctx)
    problems += [f"pack {pack} not in policies.packs" for pack in check.get("packs") or [] if pack not in packs]
    return result(problems, "rules and packs declared")


def workflow(ctx, check):
    runs = model.workflow_runs(ctx.app / check["path"])
    problems = [
        f"no run step containing {parts}"
        for parts in check["requireRuns"]
        if not any(all(part in step for part in parts) for step in runs)
    ]
    return result(problems, "workflow runs the required steps")


def report_outputs(ctx, check):
    configured = model.report_outputs(ctx)
    wanted = [(o["format"], o["path"]) for o in check["outputs"]]
    problems = [f"report output {fmt} {path} not configured" for fmt, path in wanted if (fmt, path) not in configured]
    return result(problems, "report outputs configured")
