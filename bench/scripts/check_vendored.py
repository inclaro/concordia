#!/usr/bin/env python3
"""Fail when a tracked bench file holds upstream code.

Usage:
    check_vendored.py [--upstream DIR] [--clone]

The bench never stores code of the repositories it tests: they are cloned at the pinned
SHAs of repos.json, and a file that starts from upstream content is rebuilt from that
checkout (derive.json). This check reads the upstream checkouts under DIR (default
$BENCH_UPSTREAM or /tmp/bench-full, one directory per repos.json id; `--clone` makes a
shallow clone of a missing one at its pinned SHA) and reports every bench file that git tracks
or would add (`git ls-files --cached --others --exclude-standard`) that belongs to a repository
(testdata/mutations/<repo>/... or
datasets/concordia-bench/<repo>-<scenario>/...) and of which at least THRESHOLD of the
significant lines (stripped, at least MIN_LINE characters), and at least MIN_MATCHES of them,
occur in that repository's checkout. MIN_MATCHES keeps out bench-authored stubs whose one or two
lines (an import, `apiVersion: rbac.authorization.k8s.io/v1`) also occur upstream.
Exits 1 when it reports a file.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

BENCH = Path(__file__).resolve().parent.parent
THRESHOLD = 0.3
MIN_LINE = 25
MIN_MATCHES = 5
SKIP = {"uv.lock"}
TEXT_LIMIT = 4 * 1024 * 1024


def significant(data: bytes) -> set[str]:
    if b"\0" in data[:8192]:
        return set()
    lines = (line.strip() for line in data.decode("utf-8", "replace").splitlines())
    return {line for line in lines if len(line) >= MIN_LINE}


def clone(repo: dict, dest: Path) -> None:
    dest.mkdir(parents=True)
    for args in (["init", "-q"], ["fetch", "-q", "--depth", "1", repo["url"], repo["sha"]], ["checkout", "-q", "FETCH_HEAD"]):
        subprocess.run(["git", *args], cwd=dest, check=True)


def _readable_text(path: Path) -> bool:
    """A regular file (not a symlink) small enough to read as text."""
    regular = path.is_file() and not path.is_symlink()
    return regular and path.stat().st_size <= TEXT_LIMIT


def upstream_lines(checkout: Path) -> set[str]:
    """The significant lines of every file of the checkout (outside .git)."""
    lines: set[str] = set()
    for root, dirs, files in os.walk(checkout):
        dirs[:] = [d for d in dirs if d != ".git"]
        for path in (Path(root, name) for name in files):
            if _readable_text(path):
                lines |= significant(path.read_bytes())
    return lines


def _task_repo(task: str, ids: list[str]) -> str | None:
    # The longest id first: "knative-serving-..." is not an "knative" task.
    return next((i for i in sorted(ids, key=len, reverse=True) if task.startswith(f"{i}-")), None)


def repo_of(path: str, ids: list[str]) -> str | None:
    """The repository that a bench path belongs to, from its mutation or task directory."""
    parts = path.split("/")
    if len(parts) <= 2:
        return None
    area, name = tuple(parts[:2]), parts[2]
    if area == ("testdata", "mutations"):
        return name if name in ids else None
    if area == ("datasets", "concordia-bench"):
        return _task_repo(name, ids)
    return None


def tracked_files() -> list[str]:
    """The bench files that git tracks or would add (untracked, not ignored)."""
    argv = ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "."]
    done = subprocess.run(argv, cwd=BENCH, capture_output=True, check=True)
    return [p for p in done.stdout.decode().split("\0") if p and Path(p).name not in SKIP]


def upstream_share(data: bytes, upstream: set[str]) -> float:
    """The share of the significant lines of `data` that occur upstream; 0 below MIN_MATCHES of them."""
    lines = significant(data)
    matches = len(lines & upstream)
    return matches / len(lines) if matches >= MIN_MATCHES else 0.0


def files_by_repo(ids: list[str]) -> dict[str, list[str]]:
    """The tracked bench files of each repository, by repository id."""
    by_repo: dict[str, list[str]] = {}
    for path in tracked_files():
        repo = repo_of(path, ids)
        if repo:
            by_repo.setdefault(repo, []).append(path)
    return by_repo


def ensure_checkout(repo: dict, checkout: Path, may_clone: bool) -> bool:
    """Whether the checkout exists, after shallow-cloning it when it is missing and `may_clone`."""
    if checkout.is_dir():
        return True
    if may_clone:
        clone(repo, checkout)
    return may_clone


def vendored(paths: list[str], upstream: set[str]) -> list[str]:
    """The paths (with their share) of which at least THRESHOLD of the significant lines occur upstream."""
    found = []
    for path in paths:
        file = BENCH / path
        ratio = upstream_share(file.read_bytes(), upstream) if file.is_file() else 0.0
        if ratio >= THRESHOLD:
            found.append(f"{path} ({ratio:.2f})")
    return found


def report(found: list[str], checked: int) -> int:
    for line in found:
        print(f"FAIL upstream content: {line}", file=sys.stderr)
    if found:
        print(f"{len(found)} file(s) hold upstream content: rebuild them with derive.json (see README.md)", file=sys.stderr)
        return 1
    print(f"ok: no tracked bench file holds upstream content ({checked} files checked)")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--upstream", type=Path, default=Path(os.environ.get("BENCH_UPSTREAM", "/tmp/bench-full")))
    parser.add_argument("--clone", action="store_true", help="shallow-clone missing checkouts at their pinned SHA")
    args = parser.parse_args(argv)
    repos = {r["id"]: r for r in json.loads((BENCH / "repos.json").read_text())}
    by_repo = files_by_repo(list(repos))
    found = []
    for repo, paths in sorted(by_repo.items()):
        checkout = args.upstream / repo
        if not ensure_checkout(repos[repo], checkout, args.clone):
            print(f"check_vendored: no checkout of {repo} in {args.upstream} (use --clone)", file=sys.stderr)
            return 2
        found += vendored(paths, upstream_lines(checkout))
    return report(found, sum(map(len, by_repo.values())))


if __name__ == "__main__":
    sys.exit(main())
