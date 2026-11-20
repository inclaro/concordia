import json

from concordia_bench import checks, reports

RULE = "FZF-LYR-001"
PATH = "src/util/bench_violation.go"


def test_sarif_fixture_matches_rule_and_uri(fixtures):
    sarif = fixtures / "results.sarif"
    assert reports.sarif_problem(sarif, RULE, PATH) == ""
    assert "no result" in reports.sarif_problem(sarif, RULE, "src/other.go")
    assert "no result" in reports.sarif_problem(sarif, "OTHER", PATH)


def test_sarif_rejects_wrong_version_and_garbage(tmp_path):
    bad = tmp_path / "bad.sarif"
    bad.write_text(json.dumps({"version": "2.0.0", "runs": []}))
    assert "version" in reports.sarif_problem(bad, RULE, PATH)
    bad.write_text("{nope")
    assert "unreadable" in reports.sarif_problem(bad, RULE, PATH)
    assert "unreadable" in reports.sarif_problem(tmp_path / "missing.sarif", RULE, PATH)


def test_junit_needs_failing_testcase_for_rule(fixtures, tmp_path):
    junit = fixtures / "results.xml"
    assert reports.junit_problem(junit, RULE) == ""
    assert "no failing" in reports.junit_problem(junit, "FZF-BND-001")
    assert "no failing" in reports.junit_problem(junit, "FZF")
    broken = tmp_path / "broken.xml"
    broken.write_text("<testsuites>")
    assert "unreadable" in reports.junit_problem(broken, RULE)


def test_reports_on_mutation(make_ctx, fixtures, tmp_path):
    check = {"mutation": "violation-b", "ruleId": RULE, "path": PATH, "sarif": "results.sarif", "junit": "results.xml"}

    def writes_reports(ctx):
        original = ctx.concordia_check

        def check_and_write(offline, index=True):
            for name in ("results.sarif", "results.xml"):
                (tmp_path / name).write_text((fixtures / name).read_text())
            return original(offline, index)

        ctx.concordia_check = check_and_write
        return ctx

    assert checks.reports_on_mutation(writes_reports(make_ctx(check_results=[(1, {})])), check)[0]
    assert not checks.reports_on_mutation(writes_reports(make_ctx(check_results=[(0, {})])), check)[0]
    # reports left over from an earlier run do not count: the check deletes them first
    (tmp_path / "results.sarif").write_text((fixtures / "results.sarif").read_text())
    (tmp_path / "results.xml").write_text((fixtures / "results.xml").read_text())
    assert not checks.reports_on_mutation(make_ctx(check_results=[(1, {})]), check)[0]


def review(tmp_path, doc):
    (tmp_path / "review.json").write_text(json.dumps(doc))
    return {"path": "review.json", "expectVerdict": "fail", "expectViolations": [{"ruleId": "R1", "path": "a.go"}]}


def run_review(make_ctx, tmp_path, doc):
    return checks.review_report(make_ctx(), review(tmp_path, doc))


def test_review_report_exact_set(make_ctx, tmp_path):
    ok = {"verdict": "fail", "violations": [{"ruleId": "R1", "path": "a.go"}]}
    assert run_review(make_ctx, tmp_path, ok)[0]


def test_review_report_duplicate_pair_is_fine(make_ctx, tmp_path):
    dup = {"verdict": "fail", "violations": [{"ruleId": "R1", "path": "a.go"}, {"ruleId": "R1", "path": "a.go"}]}
    assert run_review(make_ctx, tmp_path, dup)[0]


def test_review_report_extra_or_missing_pair_or_verdict_fails(make_ctx, tmp_path):
    extra = {"verdict": "fail", "violations": [{"ruleId": "R1", "path": "a.go"}, {"ruleId": "R2", "path": "a.go"}]}
    assert not run_review(make_ctx, tmp_path, extra)[0]
    assert not run_review(make_ctx, tmp_path, {"verdict": "fail", "violations": []})[0]
    assert not run_review(make_ctx, tmp_path, {"verdict": "pass", "violations": [{"ruleId": "R1", "path": "a.go"}]})[0]


BRANCH_CHECK = {"path": "review.json", "branch": "pr/bad", "expectVerdict": "fail", "expectViolations": [], "expectErrors": ["CO-POL-005"]}


def test_review_report_per_branch_with_refusal_codes(make_ctx, tmp_path):
    """A use-task review has one entry per branch; a refused configuration is reported by its codes."""
    def run(entries):
        (tmp_path / "review.json").write_text(json.dumps({"branches": entries}))
        return checks.review_report(make_ctx(), BRANCH_CHECK)[0]

    refused = {"verdict": "fail", "violations": [], "errors": ["CO-POL-005"]}
    assert run({"pr/bad": refused, "pr/ok": {"verdict": "pass", "violations": []}})
    assert not run({"pr/ok": refused})  # the reviewed branch is missing
    assert not run({"pr/bad": {"verdict": "fail", "violations": []}})  # refusal not reported
    assert not run({"pr/bad": {**refused, "errors": ["CO-POL-005", "CO-POL-017"]}})  # extra code
