import json
import subprocess

from conftest import results
from concordia_bench import checks
from concordia_bench.runner import Completed, Context

TYPED = {
    "ruleId": "KN-SEC-002",
    "kind": "obligation-violation",
    "subject": "kn-controller",
    "environment": "production",
    "result": "violation",
    "disposition": "active",
    "enforcement": "advisory",
}
CODE = {"ruleId": "FZF-LYR-001", "kind": "layer-violation", "location": {"path": "src/a.go"}, "disposition": "active"}


def doc(*findings, **extra):
    return {**results(*findings), **extra}


def run_results(make_ctx, check, **outcome):
    """Run the check against one fake `concordia check`: `code` (default 1), `document`, `stderr`."""
    document = outcome.get("document")
    ctx = make_ctx(check_results=[(outcome.get("code", 1), document if document is not None else doc(TYPED, CODE))])
    ctx.stderr = outcome.get("stderr", "")
    return ctx, checks.results(ctx, check)


def test_results_typed_finding_fields(make_ctx):
    check = {"expectExit": 1, "findings": [{"ruleId": "KN-SEC-002", "subject": "kn-controller", "environment": "production", "result": "violation"}]}
    assert run_results(make_ctx, check)[1][0]
    wrong_env = {"findings": [{"ruleId": "KN-SEC-002", "environment": "edge"}]}
    passed, detail = run_results(make_ctx, wrong_env)[1]
    assert not passed and "no finding matches" in detail and "KN-SEC-002@kn-controller/production" in detail


def test_results_exit_code_mismatch(make_ctx):
    assert not run_results(make_ctx, {"expectExit": 0})[1][0]
    assert run_results(make_ctx, {})[1][0]


def test_results_absent_fails_on_present_finding(make_ctx):
    assert not run_results(make_ctx, {"absent": [{"location.path": "src/a.go", "disposition": "active"}]})[1][0]
    assert run_results(make_ctx, {"absent": [{"location.path": "src/a.go", "disposition": "baselined"}]})[1][0]


def test_results_rules_areas_diagnostics_and_select(make_ctx):
    document = doc(
        TYPED,
        rules=[{"ruleId": "KN-SEC-002", "verdict": "violation"}],
        areas={"deploymentTrace": {"verdict": "unknown"}},
        diagnostics=[{"code": "CO-OBS-020"}],
        exceptions={"orphanedWaivers": ["WV-1"]},
    )
    check = {
        "rules": {"KN-SEC-002": "violation"},
        "areas": {"deploymentTrace": "unknown"},
        "diagnostics": ["CO-OBS-020"],
        "select": [{"path": "exceptions.orphanedWaivers", "contains": "WV-1"}],
        "counts": [{"match": {"ruleId": "KN-SEC-002"}, "equals": 1}],
    }
    assert run_results(make_ctx, check, document=document)[1][0]
    for bad in ({"rules": {"KN-SEC-002": "pass"}}, {"areas": {"deploymentTrace": "pass"}}, {"diagnostics": ["CO-OBS-021"]},
                {"counts": [{"match": {"ruleId": "KN-SEC-002"}, "equals": 2}]}):
        assert not run_results(make_ctx, bad, document=document)[1][0], bad


def test_results_diagnostic_on_console_of_refused_run(make_ctx):
    check = {"expectExit": 2, "diagnostics": ["CO-POL-018"]}
    assert run_results(make_ctx, check, code=2, document={}, stderr="error CO-POL-018: mandatory refused")[1][0]
    passed, detail = run_results(make_ctx, check, code=2, document={}, stderr="error CO-POL-017")[1]
    assert not passed and "CO-POL-018 not reported" in detail


def test_results_without_results_json_fail_unless_refusal_expected(make_ctx):
    vacuous = {"absent": [{"ruleId": "INFRA-PLC-001", "result": "pass"}], "select": [{"path": "rules[ruleId=X]", "exists": False}]}
    passed, detail = run_results(make_ctx, vacuous, code=7, document={}, stderr="panic")[1]
    assert not passed and "no results.json (exit 7)" in detail
    assert run_results(make_ctx, {**vacuous, "noResults": True}, code=7, document={})[1][0]
    assert run_results(make_ctx, {**vacuous, "expectExit": 5}, code=5, document={})[1][0]


def test_results_mutations_in_order_reverted_in_reverse_on_branch(make_ctx):
    ctx, (passed, _) = run_results(make_ctx, {"branch": "feature/x", "mutations": ["a", "b"], "args": ["--changed", "main"]})
    assert passed
    assert ctx.calls[:4] == [["checkout", "feature/x"], ["apply", "a"], ["apply", "b"], ["check", "--changed", "main"]]
    assert [before for before, _ in ctx.reverted] == ["before-b", "before-a"]
    assert ctx.calls[-1] == ["checkout", "main"] and ctx.branch == "main"


def test_results_reverts_when_check_crashes(make_ctx):
    ctx = make_ctx(check_results=[])
    try:
        checks.results(ctx, {"mutations": ["a"]})
    except IndexError:
        pass
    assert ctx.reverted == [("before-a", ["src/a.go"])]


def test_command_exit_stdout_json_and_files(make_ctx, tmp_path):
    argv = ("concordia", "trace", "kn-activator", "--format", "json")
    out = json.dumps({"steps": [1, 2, 3, 4, 5], "node": "kn-activator"})
    ctx = make_ctx(runs={argv: Completed(0, out, "")})
    (tmp_path / "build").mkdir()
    (tmp_path / "build" / "r.sarif").write_text(json.dumps({"runs": [{"results": [{"ruleId": "X"}]}]}))
    check = {
        "argv": list(argv),
        "expectExit": 0,
        "stdoutContains": ["kn-activator"],
        "stdoutJson": [{"path": "steps", "length": 5}],
        "files": [{"path": "build/r.sarif", "json": [{"path": "runs[0].results", "countWhere": {"ruleId": "X"}, "equals": 1}]}],
    }
    assert checks.command(ctx, check)[0]
    assert not checks.command(ctx, {**check, "expectExit": 5})[0]
    assert not checks.command(ctx, {**check, "stdoutJson": [{"path": "node", "equals": "x"}]})[0]
    assert not checks.command(ctx, {**check, "files": [{"path": "build/missing.json", "json": []}]})[0]


def test_yaml_file_needs_committed_file(git_repo):
    (git_repo / "concordia.yaml").write_text("environments:\n  - {id: production, sources: [a]}\n")
    ctx = Context("fake", app=git_repo)
    check = {"path": "concordia.yaml", "select": [{"path": "environments[id=production].sources", "contains": "a"}]}
    passed, detail = checks.yaml_file(ctx, check)
    assert not passed and "not in HEAD" in detail
    subprocess.run(["git", "add", "concordia.yaml"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "ws"], cwd=git_repo, check=True)
    assert checks.yaml_file(ctx, check)[0]
    assert not checks.yaml_file(ctx, {**check, "select": [{"path": "environments[id=edge]", "exists": True}]})[0]


def test_deterministic_compares_digests_and_exit_codes(make_ctx):
    same = make_ctx(check_results=[(0, results(digest="sha256:x")), (0, results(digest="sha256:x"))])
    assert checks.deterministic(same, {})[0]
    differ = make_ctx(check_results=[(0, results(digest="sha256:x")), (0, results(digest="sha256:y"))])
    assert not checks.deterministic(differ, {})[0]
    empty = make_ctx(check_results=[(0, results(digest="")), (0, results(digest=""))])
    assert not checks.deterministic(empty, {})[0]
    codes = make_ctx(check_results=[(0, results(digest="sha256:x")), (1, results(digest="sha256:x"))])
    assert not checks.deterministic(codes, {})[0]
