"""Commit verifier-owned mutations on the current branch: `python -m concordia_bench.mutate <name>...`."""

from __future__ import annotations

import sys

from .runner import apply_mutation, home_dir


def main(argv: list[str]) -> int:
    names = argv[1:]
    if not names:
        print("usage: mutate <name>...", file=sys.stderr)
        return 2
    repo = (home_dir() / "repo-id").read_text(encoding="utf-8").strip()
    for name in names:
        try:
            _, files = apply_mutation(repo, name)
        except (OSError, RuntimeError) as err:
            print(f"mutate: {repo}/{name}: {err}", file=sys.stderr)
            return 1
        print(f"{name}: {' '.join(files)}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
