"""Validation of SARIF and JUnit reports. Each function returns a problem text, or "" when fine."""

from __future__ import annotations

import json
import xml.etree.ElementTree as ET
from pathlib import Path


def _sarif_results(doc: dict) -> list[dict]:
    return [r for run in doc.get("runs") or [] for r in run.get("results") or []]


def _first_uri(result: dict) -> str:
    locations = result.get("locations") or [{}]
    return ((locations[0].get("physicalLocation") or {}).get("artifactLocation") or {}).get("uri", "")


def sarif_problem(path: Path, rule_id: str, uri: str) -> str:
    try:
        doc = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as err:
        return f"SARIF {path.name} unreadable: {err}"
    if doc.get("version") != "2.1.0":
        return f"SARIF version is {doc.get('version')!r}, want '2.1.0'"
    if any(r.get("ruleId") == rule_id and _first_uri(r) == uri for r in _sarif_results(doc)):
        return ""
    return f"SARIF has no result {rule_id} at {uri}"


def _has_failure(case: ET.Element) -> bool:
    return case.find("failure") is not None


def junit_problem(path: Path, rule_id: str) -> str:
    try:
        root = ET.parse(path).getroot()
    except (OSError, ET.ParseError) as err:
        return f"JUnit {path.name} unreadable: {err}"
    cases = [c for c in root.iter("testcase") if c.get("name", "").startswith(rule_id + " ")]
    if any(_has_failure(c) for c in cases):
        return ""
    return f"JUnit has no failing testcase for {rule_id}"
