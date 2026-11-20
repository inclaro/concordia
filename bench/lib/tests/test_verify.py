import json

import pytest

from concordia_bench import verify
from concordia_bench.checks import CHECKS


def write_spec(tmp_path, text):
    path = tmp_path / "spec.yaml"
    path.write_text(text)
    return path


def test_load_spec_accepts_known_types(tmp_path):
    spec = write_spec(tmp_path, "repo: fzf\nchecks:\n  - {id: a, type: model_valid, dimension: workspace, weight: 2}\n")
    assert verify.load_spec(spec)["checks"][0]["type"] == "model_valid"


def test_load_spec_names_unknown_type(tmp_path):
    spec = write_spec(tmp_path, "repo: fzf\nchecks:\n  - {id: a, type: bogus_check, dimension: workspace}\n")
    with pytest.raises(ValueError, match="bogus_check"):
        verify.load_spec(spec)


@pytest.mark.parametrize(
    "text",
    [
        "checks: []\n",
        "repo: r\nchecks:\n  - {id: a, type: model_valid, dimension: nope}\n",
        "repo: r\nchecks:\n  - {id: a, type: model_valid}\n",
        "repo: r\nchecks:\n  - {id: a, type: model_valid, dimension: workspace}\n  - {id: a, type: model_valid, dimension: workspace}\n",
    ],
)
def test_load_spec_rejects_malformed(tmp_path, text):
    with pytest.raises(ValueError):
        verify.load_spec(write_spec(tmp_path, text))


def test_registry_covers_plan_types():
    assert set(CHECKS) == {
        "committed", "model_valid", "calm_nodes", "relationships", "bound_nodes", "rules_declared", "results",
        "command", "yaml_file", "deterministic", "review_report", "waived", "workflow", "report_outputs",
        "reports_on_mutation", "branches_unchanged",
    }


@pytest.fixture
def env(tmp_path, monkeypatch):
    app, logs = tmp_path / "app", tmp_path / "logs"
    app.mkdir()
    monkeypatch.setenv("CONCORDIA_BENCH_APP", str(app))
    monkeypatch.setenv("CONCORDIA_BENCH_LOGS", str(logs))
    return app, logs


def test_main_writes_zero_reward_on_bad_spec(env, tmp_path):
    _, logs = env
    spec = write_spec(tmp_path, "repo: r\nchecks:\n  - {id: a, type: bogus, dimension: workspace}\n")
    assert verify.main(["verify", str(spec)]) == 0
    assert json.loads((logs / "reward.json").read_text()) == {"reward": 0.0}
    assert verify.main(["verify", str(tmp_path / "missing.yaml")]) == 0


def test_main_scores_records_crashes_and_copies_artifacts(env, tmp_path):
    app, logs = env
    (app / "concordia.yaml").write_text("report:\n  outputs:\n    - {format: junit, path: r.xml}\n")
    (app / "build" / "concordia").mkdir(parents=True)
    (app / "build" / "concordia" / "results.json").write_text("{}")
    (app / "review.json").write_text("{}")
    spec = write_spec(
        tmp_path,
        """repo: r
checks:
  - {id: out, type: report_outputs, dimension: workspace, weight: 3, outputs: [{format: junit, path: r.xml}]}
  - {id: crash, type: review_report, dimension: reporting, path: missing.json, expectVerdict: pass, expectViolations: []}
  - {id: known, type: report_outputs, dimension: detection, xfail: gap, outputs: [{format: sarif, path: s}]}
""",
    )
    assert verify.main(["verify", str(spec)]) == 0
    assert json.loads((logs / "reward.json").read_text()) == {"reward": 0.75, "workspace": 1.0, "reporting": 0.0}
    details = {d["id"]: d for d in json.loads((logs / "reward-details.json").read_text())}
    assert details["out"]["passed"] and not details["crash"]["passed"]
    assert "No such file" in details["crash"]["detail"]
    assert details["known"]["xfail"] == "gap" and not details["known"]["passed"]
    for name in ("concordia.yaml", "review.json", "build/concordia/results.json"):
        assert (logs / "artifacts" / name).is_file()
