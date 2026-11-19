"""Readers for the workspace files that the checks inspect (CALM, bindings, policies, waivers, workflows)."""

from __future__ import annotations

import glob
import json
from pathlib import Path

import yaml

NODE_REF = "calm:node:"
DEFAULT_WAIVERS = "exceptions/waivers.yaml"


def read_yaml(path: Path) -> dict:
    return yaml.safe_load(path.read_text(encoding="utf-8")) or {}


def expand(app: Path, patterns: list[str]) -> list[Path]:
    found = {Path(p) for pattern in patterns for p in glob.glob(str(app / pattern), recursive=True)}
    return sorted(p for p in found if p.is_file())


def calm_document(ctx) -> dict:
    source = ctx.workspace()["architecture"]["source"]
    return json.loads((ctx.app / source).read_text(encoding="utf-8"))


def node_problems(node: dict | None, spec: dict) -> list[str]:
    if node is None:
        return [f"missing node {spec['id']}"]
    problems = []
    if node.get("node-type") != spec["nodeType"]:
        problems.append(f"node {spec['id']} has type {node.get('node-type')!r}, want {spec['nodeType']!r}")
    tags = set((node.get("concordia") or {}).get("tags") or [])
    absent = set(spec.get("tags") or []) - tags
    if absent:
        problems.append(f"node {spec['id']} lacks tags {sorted(absent)}")
    return problems


def _connection(relationship: dict) -> tuple[str, str] | None:
    connects = (relationship.get("relationship-type") or {}).get("connects")
    if not connects:
        return None
    return (connects["source"]["node"], connects["destination"]["node"])


def connect_pairs(doc: dict) -> set[tuple[str, str]]:
    pairs = (_connection(r) for r in doc.get("relationships") or [])
    return {pair for pair in pairs if pair}


def _configured_files(ctx, section: str) -> list[Path]:
    patterns = (ctx.workspace().get(section) or {}).get("files") or []
    return expand(ctx.app, patterns)


def bound_ids(ctx) -> set[str]:
    refs = set()
    for path in _configured_files(ctx, "bindings"):
        for binding in read_yaml(path).get("bindings") or []:
            ref = str(binding.get("architectureRef", ""))
            if ref.startswith(NODE_REF):
                refs.add(ref[len(NODE_REF):])
    return refs


PREDICATES = (
    "forbid", "layerOrder", "declaredOnly", "observedRequired", "require.graphProperty", "require.edge",
    "require.entity", "require.effectiveAccess", "require.checkResult", "require.claim", "denyPath", "allowOnly",
    "independent", "publicApiOnly", "ownership", "budget", "obligation", "decisions", "assessmentCurrency",
)


def _has_predicate(rule: dict, key: str) -> bool:
    head, _, form = key.partition(".")
    value = rule.get(head)
    if not form:
        return value is not None
    return isinstance(value, dict) and form in value


def rule_predicate(rule: dict) -> str | None:
    """The predicate key of a policy rule: a top-level key, or `require.<form>`."""
    return next((key for key in PREDICATES if _has_predicate(rule, key)), None)


def pack_uses(ctx) -> list[str]:
    packs = (ctx.workspace().get("policies") or {}).get("packs") or []
    return [p.get("use") for p in packs if isinstance(p, dict)]


def _policy_paths(ctx) -> list[Path]:
    """The files of `policies.files`, and the local packs of `policies.packs` (`use: <path>`)."""
    local = [use for use in pack_uses(ctx) if isinstance(use, str) and not use.startswith("builtin:")]
    return _configured_files(ctx, "policies") + expand(ctx.app, local)


def declared_rules(ctx) -> dict[str, dict]:
    """Every rule of the workspace's policy files and local packs, by id."""
    return {rule.get("id"): rule for path in _policy_paths(ctx) for rule in read_yaml(path).get("rules") or []}


def waiver_entries(ctx) -> list[dict]:
    configured = (ctx.workspace().get("exceptions") or {}).get("waivers") or DEFAULT_WAIVERS
    path = ctx.app / configured
    return (read_yaml(path).get("waivers") or []) if path.is_file() else []


def workflow_runs(path: Path) -> list[str]:
    doc = read_yaml(path)
    steps = (s for job in (doc.get("jobs") or {}).values() for s in job.get("steps") or [])
    return [str(s["run"]) for s in steps if s.get("run")]


def report_outputs(ctx) -> list[tuple[str, str]]:
    outputs = (ctx.workspace().get("report") or {}).get("outputs") or []
    return [(o.get("format"), o.get("path")) for o in outputs]
