"""Generic checks on Concordia output: `results` (results.json), `command` (any command) and `yaml_file`."""

from __future__ import annotations

import json
from pathlib import Path

import yaml

from . import findings, select
from .outcome import prepared, result
from .runner import CheckRun
from .workspace_checks import _untracked_problems


def check_options(check: dict) -> CheckRun:
    return CheckRun(
        offline=check.get("offline", False),
        index=check.get("index", True),
        args=tuple(str(a) for a in check.get("args") or ()),
        env=check.get("env"),
    )


def exit_problems(code: int, check: dict) -> list[str]:
    if "expectExit" not in check or code == check["expectExit"]:
        return []
    return [f"exit {code}, want {check['expectExit']}"]


def output_problems(done, check: dict) -> list[str]:
    wanted = [("stdout", done.stdout, s) for s in check.get("stdoutContains") or []]
    wanted += [("stderr", done.stderr, s) for s in check.get("stderrContains") or []]
    return [f"{stream} does not contain {text!r}" for stream, out, text in wanted if text not in out]


def _missing(items: list[dict], wanted: list[dict]) -> list[str]:
    return [f"no finding matches {want}" for want in wanted if not any(select.matches(f, want) for f in items)]


def _unwanted(items: list[dict], absent: list[dict]) -> list[str]:
    counts = ((spec, sum(1 for f in items if select.matches(f, spec))) for spec in absent)
    return [f"{hits} finding(s) match absent {spec}" for spec, hits in counts if hits]


def _finding_problems(doc: dict, check: dict) -> list[str]:
    items = doc.get("findings") or []
    return _missing(items, check.get("findings") or []) + _unwanted(items, check.get("absent") or [])


def _count_problems(doc: dict, check: dict) -> list[str]:
    items = doc.get("findings") or []
    problems = []
    for count in check.get("counts") or []:
        got = sum(1 for f in items if select.matches(f, count["match"]))
        if got != count["equals"]:
            problems.append(f"{got} findings match {count['match']}, want {count['equals']}")
    return problems


def _verdict_problems(doc: dict, check: dict) -> list[str]:
    wanted = [(f"rules[ruleId={rule}].verdict", want) for rule, want in (check.get("rules") or {}).items()]
    wanted += [(f"areas.{area}.verdict", want) for area, want in (check.get("areas") or {}).items()]
    problems = []
    for path, want in wanted:
        got = select.get(doc, path)
        options = want if isinstance(want, list) else [want]
        if not any(select.same(got, option) for option in options):
            problems.append(f"{path} is {got!r}, want {want!r}")
    return problems


def _diagnostic_problems(doc: dict, done, check: dict) -> list[str]:
    # A refused run (exit 2) writes no results.json; its diagnostics are on the console.
    codes = {d.get("code") for d in doc.get("diagnostics") or [] if isinstance(d, dict)}
    console = done.stdout + done.stderr
    return [f"diagnostic {code} not reported" for code in check.get("diagnostics") or [] if code not in codes and code not in console]


def expectation_problems(doc: dict, done, check: dict) -> list[str]:
    """Every mismatch between a results.json (and its run) and the expectations of a `results` check."""
    return (
        exit_problems(done.exit, check)
        + output_problems(done, check)
        + _finding_problems(doc, check)
        + _count_problems(doc, check)
        + _verdict_problems(doc, check)
        + _diagnostic_problems(doc, done, check)
        + select.problems(doc, check.get("select") or [])
    )


# Exit codes of refused runs, which write no results.json (invalid input, toolchain).
REFUSED_EXITS = (2, 5)


def _no_results_expected(check: dict) -> bool:
    return bool(check.get("noResults")) or check.get("expectExit") in REFUSED_EXITS


def _missing_results_problems(doc: dict, done, check: dict) -> list[str]:
    """A run without results.json fails the check unless it is an expected refusal (or `noResults: true`):
    otherwise `absent`, `counts … equals: 0` and `exists: false` would hold vacuously on a crash."""
    if doc:
        return []
    if _no_results_expected(check):
        return []
    return [f"no results.json (exit {done.exit}); console: {(done.stderr or done.stdout).strip()[-400:]}"]


def results(ctx, check):
    with prepared(ctx, check):
        done, doc = ctx.concordia_run(check_options(check))
    problems = _missing_results_problems(doc, done, check) + expectation_problems(doc, done, check)
    return result(problems, f"results as expected (exit {done.exit})", findings.summarize(doc))


def parse_file(path: Path):
    text = path.read_text(encoding="utf-8")
    return json.loads(text) if path.suffix in (".json", ".sarif") else yaml.safe_load(text)


def _file_problems(app: Path, spec: dict) -> list[str]:
    path = app / spec["path"]
    try:
        doc = parse_file(path)
    except (OSError, ValueError, yaml.YAMLError) as err:
        return [f"{spec['path']} unreadable: {err}"]
    selections = (spec.get("json") or []) + (spec.get("yaml") or [])
    return [f"{spec['path']}: {p}" for p in select.problems(doc, selections)]


def _stdout_json_problems(done, check: dict) -> list[str]:
    if not check.get("stdoutJson"):
        return []
    try:
        doc = json.loads(done.stdout)
    except ValueError as err:
        return [f"stdout is not JSON: {err}"]
    return select.problems(doc, check["stdoutJson"])


def command(ctx, check):
    with prepared(ctx, check):
        done = ctx.run([str(a) for a in check["argv"]], env=check.get("env"))
        problems = exit_problems(done.exit, check) + output_problems(done, check) + _stdout_json_problems(done, check)
        for spec in check.get("files") or []:
            problems += _file_problems(ctx.app, spec)
    console = f"exit {done.exit}: {(done.stderr or done.stdout).strip()[-400:]}"
    return result(problems, f"{' '.join(map(str, check['argv']))} as expected", console)


def yaml_file(ctx, check):
    problems = _untracked_problems(ctx, [check["path"]])
    if not problems:
        problems = _file_problems(ctx.app, {"path": check["path"], "yaml": check.get("select") or []})
    return result(problems, f"{check['path']} as expected")
