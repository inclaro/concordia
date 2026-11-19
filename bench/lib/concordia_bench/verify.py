"""Verifier entry point: `python -m concordia_bench.verify /tests/spec.yaml`."""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

import yaml

from .checks import CHECKS
from .reward import DIMENSIONS, score
from .runner import Context, app_dir, logs_dir

# Everything an agent may author for Concordia, so a trial's workspace can be replayed for triage.
ARTIFACTS = (
    "concordia.yaml",
    ".concordia.lock",
    ".cbmignore",
    "architecture",
    "bindings",
    "policies",
    "exceptions",
    "assessments",
    "quality",
    "bench-env",
    "bench-values",
    "gitops",
    "docs/adr",
    "docs/architecture",
    "build/concordia",
    "build/results",
    ".github/workflows",
    "review.json",
)
DETAIL_LIMIT = 2000


def _require_keys(check: dict) -> None:
    for key in ("id", "type", "dimension"):
        if key not in check:
            raise ValueError(f"check {check!r} has no {key!r}")


def _require_known(check: dict) -> None:
    if check["type"] not in CHECKS:
        raise ValueError(f"unknown check type {check['type']!r} in check {check['id']!r}")
    if check["dimension"] not in DIMENSIONS:
        raise ValueError(f"unknown dimension {check['dimension']!r} in check {check['id']!r}")


def _validate_check(check: dict, seen: set) -> None:
    _require_keys(check)
    _require_known(check)
    if check["id"] in seen:
        raise ValueError(f"duplicate check id {check['id']!r}")
    seen.add(check["id"])


def _has_spec_shape(spec) -> bool:
    return isinstance(spec, dict) and bool(spec.get("repo")) and isinstance(spec.get("checks"), list)


def load_spec(path) -> dict:
    """Parse a spec.yaml and validate its check types. Raises ValueError on a bad spec."""
    spec = yaml.safe_load(Path(path).read_text(encoding="utf-8"))
    if not _has_spec_shape(spec):
        raise ValueError("spec needs 'repo' and a 'checks' list")
    seen: set = set()
    for check in spec["checks"]:
        _validate_check(check, seen)
    return spec


def run_check(ctx, check: dict) -> dict:
    try:
        passed, detail = CHECKS[check["type"]](ctx, check)
    except Exception as err:  # a crashing check is a failed check
        passed, detail = False, f"{type(err).__name__}: {err}"
    return {
        "id": check["id"],
        "type": check["type"],
        "dimension": check["dimension"],
        "weight": float(check.get("weight", 1.0)),
        "passed": bool(passed),
        "xfail": check.get("xfail"),
        "detail": str(detail)[:DETAIL_LIMIT],
    }


def _copy_artifact(app: Path, target: Path, name: str) -> None:
    source = app / name
    if source.is_dir():
        shutil.copytree(source, target / name, dirs_exist_ok=True)
    elif source.is_file():
        (target / name).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target / name)


def collect_artifacts(app: Path, logs: Path) -> None:
    for name in ARTIFACTS:
        try:
            _copy_artifact(app, logs / "artifacts", name)
        except OSError:
            continue


def write_outputs(logs: Path, rewards: dict, details: list[dict]) -> None:
    logs.mkdir(parents=True, exist_ok=True)
    (logs / "reward.json").write_text(json.dumps(rewards, indent=2) + "\n", encoding="utf-8")
    (logs / "reward-details.json").write_text(json.dumps(details, indent=2) + "\n", encoding="utf-8")


def main(argv: list[str]) -> int:
    logs = logs_dir()
    try:
        spec = load_spec(argv[1])
    except (ValueError, OSError, IndexError, yaml.YAMLError) as err:
        print(f"verify: cannot load spec: {err}", file=sys.stderr)
        write_outputs(logs, {"reward": 0.0}, [])
        return 0
    ctx = Context(spec["repo"])
    details = [run_check(ctx, check) for check in spec["checks"]]
    for item in details:
        print(f"{'PASS' if item['passed'] else 'FAIL'} {item['id']}: {item['detail']}")
    write_outputs(logs, score(details), details)
    collect_artifacts(app_dir(), logs)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
