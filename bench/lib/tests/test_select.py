import datetime

from concordia_bench import select

DOC = {
    "rules": [{"ruleId": "A", "verdict": "pass"}, {"ruleId": "B.1", "verdict": "violation"}],
    "assurance": {"evaluations": [{"ruleId": "N-1", "verdict": "unknown", "reason": {"code": "x"}}]},
    "runs": [{"results": [{"ruleId": "R"}, {"ruleId": "R"}, {"ruleId": "S"}]}],
    "summary": {"rules": {"unknown": 0}},
    "exceptions": {"orphanedWaivers": ["W-001"]},
}


def test_get_dotted_filter_index_and_star():
    assert select.get(DOC, "summary.rules.unknown") == 0
    assert select.get(DOC, "rules[ruleId=B.1].verdict") == "violation"
    assert select.get(DOC, "assurance.evaluations[ruleId=N-1].reason.code") == "x"
    assert select.get(DOC, "assurance.evaluations[reason.code=x].verdict") == "unknown"
    assert select.get(DOC, "runs[0].results[2].ruleId") == "S"
    assert select.get(DOC, "rules[*].ruleId") == ["A", "B.1"]
    assert select.get(DOC, "rules[*]") == DOC["rules"]


def test_get_missing_path_is_none():
    assert select.get(DOC, "rules[ruleId=Z].verdict") is None
    assert select.get(DOC, "summary.nope") is None
    assert select.get(DOC, "runs[7]") is None
    assert select.get(DOC, "summary.rules.unknown.deeper") is None
    assert not select.exists(DOC, "rules[ruleId=Z]")
    assert select.exists(DOC, "summary.rules.unknown")


def test_matches_list_is_any_of_and_dotted_keys():
    finding = {"ruleId": "X", "location": {"path": "a.go"}, "result": "unknown", "line": 3}
    assert select.matches(finding, {"ruleId": "X", "location.path": "a.go"})
    assert select.matches(finding, {"result": ["violation", "unknown"]})
    assert not select.matches(finding, {"result": ["violation", "pass"]})
    assert not select.matches(finding, {"location.path": "b.go"})
    assert select.matches(finding, {"line": "3"})
    assert select.matches({"expires": datetime.date(2099, 12, 31)}, {"expires": "2099-12-31"})
    assert not select.matches({"flag": True}, {"flag": "True"})


def test_problems_operators():
    ok = [
        {"path": "exceptions.orphanedWaivers", "contains": "W-001"},
        {"path": "summary.rules.unknown", "equals": 0},
        {"path": "rules[ruleId=A]", "exists": True},
        {"path": "rules[ruleId=Z]", "exists": False},
        {"path": "rules[ruleId=B.1].verdict", "oneOf": ["violation", "unknown"]},
        {"path": "runs[0].results", "countWhere": {"ruleId": "R"}, "equals": 2},
        {"path": "rules", "contains": {"ruleId": "A"}},
    ]
    assert select.problems(DOC, ok) == []
    bad = [
        {"path": "exceptions.orphanedWaivers", "contains": "W-002"},
        {"path": "summary.rules.unknown", "equals": 1},
        {"path": "rules[ruleId=A]", "exists": False},
        {"path": "rules[ruleId=A].verdict", "oneOf": ["violation"]},
        {"path": "runs[0].results", "countWhere": {"ruleId": "S"}, "equals": 2},
        {"path": "summary", "countWhere": {"x": 1}, "equals": 0},
        {"path": "summary"},
    ]
    assert len(select.problems(DOC, bad)) == len(bad)
