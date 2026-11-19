from concordia_bench.reward import score


def item(dimension, passed, weight=1.0, xfail=None):
    return {"dimension": dimension, "passed": passed, "weight": weight, "xfail": xfail}


def test_weighted_mean_overall_and_per_dimension():
    out = score([item("workspace", True, 2), item("workspace", False, 1), item("detection", True, 3)])
    assert out == {"reward": round(5 / 6, 4), "workspace": round(2 / 3, 4), "detection": 1.0}


def test_xfail_is_excluded_from_every_score():
    out = score([item("detection", True, 1), item("detection", False, 5, xfail="CBM gap"), item("reporting", False, 1, xfail="x")])
    assert out == {"reward": 1.0, "detection": 1.0}


def test_dimension_with_only_failures_scores_zero():
    out = score([item("workspace", False, 2), item("workspace", False), item("determinism", True)])
    assert out["workspace"] == 0.0
    assert out["determinism"] == 1.0
    assert out["reward"] == round(1 / 4, 4)


def test_weight_defaults_to_one_and_empty_is_zero():
    assert score([{"dimension": "workspace", "passed": True}, {"dimension": "workspace", "passed": False}])["reward"] == 0.5
    assert score([]) == {"reward": 0.0}
