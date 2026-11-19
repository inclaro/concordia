"""Checks of Concordia's behaviour: determinism, review reports, waivers, report outputs."""

from __future__ import annotations

import json

from . import findings, model, reports
from .outcome import exit_problems, exit_result, mutated, prepared, result
from .result_checks import check_options
from .runner import concordia_argv


def _offline(check: dict) -> bool:
    return check.get("offline", False)


def deterministic(ctx, check):
    offline = _offline(check)
    indexed = ctx.run(concordia_argv(offline, "index"))
    if indexed.exit != 0:
        return exit_result(indexed, "")
    (code1, first), (code2, second) = (ctx.concordia_check(offline, index=False) for _ in range(2))
    digests = [r.get("summary", {}).get("resultDigest") for r in (first, second)]
    problems = exit_problems(code2, code1)
    if not digests[0] or digests[0] != digests[1]:
        problems.append(f"result digests differ or are empty: {digests}")
    return result(problems, f"stable digest {digests[0]}")


def _violation_pairs(items: list[dict]) -> set[tuple[str, str]]:
    return {(v.get("ruleId"), v.get("path")) for v in items}


def _review_entry(doc: dict, branch: str | None) -> dict | None:
    """The review of one branch (`branches.<name>`), or the flat document without a branch."""
    if branch is None:
        return doc
    return (doc.get("branches") or {}).get(branch)


def _review_problems(entry: dict, check: dict) -> list[str]:
    problems = []
    if entry.get("verdict") != check["expectVerdict"]:
        problems.append(f"verdict {entry.get('verdict')!r}, want {check['expectVerdict']!r}")
    got, want = _violation_pairs(entry.get("violations") or []), _violation_pairs(check["expectViolations"])
    if got != want:
        problems.append(f"violations {sorted(got)}, want {sorted(want)}")
    errors, want_errors = set(entry.get("errors") or []), set(check.get("expectErrors") or [])
    if errors != want_errors:
        problems.append(f"errors {sorted(errors)}, want {sorted(want_errors)}")
    return problems


def review_report(ctx, check):
    doc = json.loads((ctx.app / check["path"]).read_text(encoding="utf-8"))
    branch = check.get("branch")
    entry = _review_entry(doc, branch)
    if entry is None:
        return result([f"no review for branch {branch}"], "")
    return result(_review_problems(entry, check), f"review report as expected{f' for {branch}' if branch else ''}")


def _entry_problems(waiver: dict | None, label: str, check: dict) -> list[str]:
    if waiver is None:
        return [f"no waiver for {label}"]
    problems = []
    for key in ("issue", "expires", "owner"):
        if key in check and str(waiver.get(key)) != str(check[key]):
            problems.append(f"waiver {key} {waiver.get(key)!s}, want {check[key]!s}")
    return problems


def _scoped_waiver(ctx, rule_id: str) -> dict | None:
    return next((w for w in model.waiver_entries(ctx) if w.get("rule") == rule_id and w.get("scope")), None)


def _waiver_problems(ctx, check, found: list[dict]) -> list[str]:
    where = f" at {check['path']}" if check.get("path") else ""
    if not found:
        return [f"no {check['ruleId']} finding{where}"]
    problems = [f"finding {f.get('fingerprint')} is {f.get('disposition')}" for f in found if f.get("disposition") != "waived"]
    if check.get("scope"):
        return problems + _entry_problems(_scoped_waiver(ctx, check["ruleId"]), f"rule {check['ruleId']} (scoped)", check)
    waivers = {w.get("finding"): w for w in model.waiver_entries(ctx)}
    for finding in found:
        fingerprint = finding.get("fingerprint")
        problems += _entry_problems(waivers.get(fingerprint), str(fingerprint), check)
    return problems


def _rule_findings(results: dict, check: dict) -> list[dict]:
    if check.get("path"):
        return findings.at(results, check["ruleId"], check["path"])
    return [f for f in results.get("findings") or [] if f.get("ruleId") == check["ruleId"]]


def waived(ctx, check):
    """The findings of `ruleId` (at `path`, when given) are waived by a finding waiver, or by a scoped one (`scope: true`).

    Validation, the check run and the register lookup all happen on the check's `branch`, with its `args`/`env`."""
    with prepared(ctx, check):
        validated = ctx.run(["concordia", "waiver", "validate"])
        if validated.exit != 0:
            return exit_result(validated, "")
        done, results = ctx.concordia_run(check_options(check))
        problems = exit_problems(done.exit, check.get("expectExit", 0))
        problems += _waiver_problems(ctx, check, _rule_findings(results, check))
    return result(problems, f"{check['ruleId']} waived", findings.summarize(results))


def _report_problems(ctx, check) -> list[str]:
    sarif = reports.sarif_problem(ctx.app / check["sarif"], check["ruleId"], check["path"])
    junit = reports.junit_problem(ctx.app / check["junit"], check["ruleId"])
    return [p for p in (sarif, junit) if p]


def reports_on_mutation(ctx, check):
    for key in ("sarif", "junit"):
        (ctx.app / check[key]).unlink(missing_ok=True)
    with mutated(ctx, check["mutation"]):
        code, _ = ctx.concordia_check(_offline(check))
        problems = exit_problems(code, 1) + _report_problems(ctx, check)
    return result(problems, "SARIF and JUnit report the violation")
