"""Lookups and summaries of the findings of a results.json."""

from __future__ import annotations


def _findings(results: dict) -> list[dict]:
    return results.get("findings") or []


def finding_path(finding: dict) -> str:
    return (finding.get("location") or {}).get("path", "")


def _label(finding: dict) -> str:
    where = finding_path(finding) or "/".join(str(finding[k]) for k in ("subject", "environment") if finding.get(k))
    kind = finding.get("kind") or finding.get("result") or ""
    return f"{finding.get('ruleId')}@{where}:{kind}[{finding.get('disposition')}]"


def summarize(results: dict, limit: int = 12) -> str:
    # Active findings first: on large repositories hundreds of baselined ones would hide them.
    ordered = sorted(_findings(results), key=lambda f: f.get("disposition") != "active")
    items = [_label(f) for f in ordered]
    more = f" (+{len(items) - limit} more)" if len(items) > limit else ""
    return "findings: " + (", ".join(items[:limit]) or "none") + more


def at(results: dict, rule_id: str, path: str) -> list[dict]:
    return [f for f in _findings(results) if f.get("ruleId") == rule_id and finding_path(f) == path]
