"""Paths into parsed JSON/YAML documents, and the operators of `select` expectations.

A path is a dotted list of keys. A key may carry one bracket suffix:

* `[k=v]`   the first list item whose dotted key `k` equals `v` (compared as text);
* `[3]`     the list item at that index;
* `[*]`     the whole list; later keys map over its items.

Examples: `rules[ruleId=X].verdict`, `findings[ruleId=X]`, `runs[0].results`.
"""

from __future__ import annotations

import re

_MISSING = object()
_SEGMENT = re.compile(r"^(?P<key>[^\[]*)(?:\[(?P<sel>[^\]]*)\])?$")


def split_path(path: str) -> list[str]:
    """Split on dots outside brackets."""
    parts, depth, current = [], 0, []
    for char in path:
        depth += {"[": 1, "]": -1}.get(char, 0)
        if char == "." and depth == 0:
            parts.append("".join(current))
            current = []
        else:
            current.append(char)
    parts.append("".join(current))
    return [p for p in parts if p]


def _scalar(value) -> bool:
    return value is not None and not isinstance(value, (bool, dict, list))


def same(actual, expected) -> bool:
    """Equality that tolerates YAML scalar typing (dates, numbers written as text)."""
    if actual == expected:
        return True
    return all((_scalar(actual), _scalar(expected), str(actual) == str(expected)))


def _pick(value, selector: str):
    if not isinstance(value, list):
        return _MISSING
    if selector == "*":
        return value
    if "=" in selector:
        key, want = selector.split("=", 1)
        return next((item for item in value if same(get(item, key), want)), _MISSING)
    if selector.lstrip("-").isdigit():
        index = int(selector)
        return value[index] if -len(value) <= index < len(value) else _MISSING
    return _MISSING


def _step(value, segment: str):
    match = _SEGMENT.match(segment)
    if match is None:
        return _MISSING
    key, selector = match.group("key"), match.group("sel")
    if key:
        if not isinstance(value, dict) or key not in value:
            return _MISSING
        value = value[key]
    return value if selector is None else _pick(value, selector)


def _spread(value, rest: list[str]):
    """After `[*]`: resolve the remaining segments on each item, dropping the items where they are missing."""
    if not rest:
        return value
    mapped = (_resolve(item, rest) for item in value)
    return [item for item in mapped if item is not _MISSING]


def _resolve(value, segments: list[str]):
    for position, segment in enumerate(segments):
        value = _step(value, segment)
        if value is _MISSING:
            return _MISSING
        if segment.endswith("[*]"):
            return _spread(value, segments[position + 1:])
    return value


def get(doc, path: str):
    """The value at `path`, or None when any step is missing."""
    value = _resolve(doc, split_path(path))
    return None if value is _MISSING else value


def exists(doc, path: str) -> bool:
    return _resolve(doc, split_path(path)) is not _MISSING


def matches(item, expected: dict) -> bool:
    """Every dotted key of `expected` equals the item's value; an expected list means any of."""
    for key, want in expected.items():
        actual = get(item, key)
        options = want if isinstance(want, list) else [want]
        if not any(same(actual, option) for option in options):
            return False
    return True


def _contains(value, want) -> bool:
    if isinstance(value, str):
        return str(want) in value
    if isinstance(value, list):
        if isinstance(want, dict):
            return any(isinstance(item, dict) and matches(item, want) for item in value)
        return any(same(item, want) for item in value)
    if isinstance(value, dict):
        return str(want) in value
    return False


def _count_problem(value, sel: dict) -> str:
    if not isinstance(value, list):
        return f"{sel['path']}: not a list ({value!r:.80})"
    count = sum(1 for item in value if isinstance(item, dict) and matches(item, sel["countWhere"]))
    return "" if count == sel.get("equals") else f"{sel['path']}: {count} items match {sel['countWhere']}, want {sel.get('equals')}"


def _length_problem(value, sel: dict) -> str:
    size = len(value) if isinstance(value, (list, dict, str)) else None
    return "" if size == sel["length"] else f"{sel['path']}: length {size}, want {sel['length']}"


def _equals_problem(value, sel: dict) -> str:
    return "" if same(value, sel["equals"]) else f"{sel['path']}: {value!r:.200} != {sel['equals']!r}"


def _contains_problem(value, sel: dict) -> str:
    return "" if _contains(value, sel["contains"]) else f"{sel['path']}: {value!r:.200} does not contain {sel['contains']!r}"


def _one_of_problem(value, sel: dict) -> str:
    return "" if any(same(value, o) for o in sel["oneOf"]) else f"{sel['path']}: {value!r:.200} not one of {sel['oneOf']!r}"


# Checked in this order; `equals` is also the count of `countWhere`, so countWhere comes first.
_OPERATORS = (
    ("countWhere", _count_problem),
    ("length", _length_problem),
    ("equals", _equals_problem),
    ("contains", _contains_problem),
    ("oneOf", _one_of_problem),
)


def _operator_problem(value, present: bool, sel: dict) -> str:
    if "exists" in sel:
        return "" if present == bool(sel["exists"]) else f"{sel['path']}: exists is {present}, want {sel['exists']}"
    for key, check in _OPERATORS:
        if key in sel:
            return check(value, sel)
    return f"{sel['path']}: no operator (exists, {', '.join(k for k, _ in _OPERATORS)})"


def problems(doc, selections: list[dict]) -> list[str]:
    """One problem text per `select` item that does not hold on `doc`."""
    out = []
    for sel in selections or []:
        value = _resolve(doc, split_path(sel["path"]))
        present = value is not _MISSING
        problem = _operator_problem(None if not present else value, present, sel)
        if problem:
            out.append(problem)
    return out
