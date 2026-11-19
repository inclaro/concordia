"""Reward scoring: weighted means per dimension and overall."""

from __future__ import annotations

DIMENSIONS = ("workspace", "conformance", "detection", "deployment", "assurance", "governance", "reporting", "determinism")


def _mean(results: list[dict]) -> float:
    total = sum(r.get("weight", 1.0) for r in results)
    if not total:
        return 0.0
    passed = sum(r.get("weight", 1.0) for r in results if r["passed"])
    return round(passed / total, 4)


def score(results: list[dict]) -> dict:
    """Return `reward` plus one key per dimension with scored checks. `xfail` checks are skipped."""
    scored = [r for r in results if not r.get("xfail")]
    out = {"reward": _mean(scored)}
    for dimension in DIMENSIONS:
        group = [r for r in scored if r["dimension"] == dimension]
        if group:
            out[dimension] = _mean(group)
    return out
