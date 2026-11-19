"""Derived files: build a file from the pinned upstream checkout instead of storing a copy of it.

The bench never commits upstream code. A file that starts from upstream content (an upstream
file a mutation changes, a rendered chart, a copied manifest) is described in `derive.json`
and rebuilt in the app (the repository root) when it is needed:

    [{"path": "bench-env/x.yaml",           target, repo-relative
      "from": "manifests/install.yaml",     base: a file of the working tree (default: path)
      "run": "helm template ...",           or base: stdout of a bash command run in the app
      "edit": "edits/x.yaml.ed",            optional ed script (`diff -e base target`), relative to derive.json
      "base_sha256": "...",                 digest of the base
      "sha256": "..."}]                     digest of the result

An ed script holds line addresses and the new lines only, never the upstream lines it replaces.
A digest mismatch fails: the base is not the pinned content the entry was made from.

    python -m concordia_bench.derive apply <spec-dir> [<app>]
    python -m concordia_bench.derive make <spec-dir> <path> <target-file> [--from SRC | --run CMD] [--app DIR]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from collections.abc import Iterator
from pathlib import Path
from typing import NamedTuple

SPEC = "derive.json"
_COMMAND = re.compile(r"^(\d+)(?:,(\d+))?([acd])$")


class DeriveError(RuntimeError):
    pass


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class _EdCommand(NamedTuple):
    label: str
    start: int
    end: int
    op: str
    text: list[str]

    def fits(self, count: int) -> bool:
        """Whether the command addresses lines of a `count`-line file."""
        if self.op == "a":
            return self.start <= count
        return 1 <= self.start <= self.end <= count

    def apply(self, lines: list[str]) -> None:
        if not self.fits(len(lines)):
            raise DeriveError(f"ed command {self.label} is outside the {len(lines)}-line base")
        if self.op == "a":
            lines[self.start : self.start] = self.text
        else:
            lines[self.start - 1 : self.end] = self.text


def _text_block(commands: list[str], i: int) -> tuple[list[str], int]:
    """The text lines from commands[i] up to the closing '.', and the index after it."""
    try:
        close = commands.index(".", i)
    except ValueError:
        raise DeriveError("ed text block without a closing '.'") from None
    return [line + "\n" for line in commands[i:close]], close + 1


def _ed_commands(script: str) -> Iterator[_EdCommand]:
    commands = script.splitlines()
    i = 0
    while i < len(commands):
        match = _COMMAND.match(commands[i])
        if not match:
            raise DeriveError(f"unsupported ed command {commands[i]!r}")
        start, op = int(match.group(1)), match.group(3)
        text, i = _text_block(commands, i + 1) if op in "ac" else ([], i + 1)
        yield _EdCommand(match.group(0), start, int(match.group(2) or start), op, text)


def apply_ed(base: bytes, script: str) -> bytes:
    """Apply a `diff -e` script (commands a, c, d; bottom-up line numbers) to `base`."""
    lines = base.decode("utf-8").splitlines(keepends=True)
    for command in _ed_commands(script):
        command.apply(lines)
    return "".join(lines).encode("utf-8")


def _base(app: Path, entry: dict) -> bytes:
    if "run" in entry:
        done = subprocess.run(["bash", "-c", entry["run"]], cwd=app, capture_output=True)
        if done.returncode != 0:
            raise DeriveError(f"{entry['path']}: run exited {done.returncode}: {done.stderr.decode(errors='replace').strip()}")
        return done.stdout
    source = app / entry.get("from", entry["path"])
    if not source.is_file():
        raise DeriveError(f"{entry['path']}: base {source.relative_to(app)} does not exist")
    return source.read_bytes()


def build(app: Path, spec_dir: Path, entry: dict) -> bytes:
    base = _base(app, entry)
    if digest(base) != entry["base_sha256"]:
        raise DeriveError(f"{entry['path']}: the base is not the pinned content (sha256 {digest(base)})")
    result = apply_ed(base, (spec_dir / entry["edit"]).read_text(encoding="utf-8")) if entry.get("edit") else base
    if digest(result) != entry["sha256"]:
        raise DeriveError(f"{entry['path']}: the result does not match its sha256")
    return result


def entries(spec_dir: Path) -> list[dict]:
    spec = spec_dir / SPEC
    return json.loads(spec.read_text(encoding="utf-8")) if spec.is_file() else []


def apply(spec_dir: Path, app: Path) -> list[str]:
    """Write every entry of spec_dir/derive.json into app, in order. Returns the written paths."""
    written = []
    for entry in entries(spec_dir):
        data = build(app, spec_dir, entry)
        target = app / entry["path"]
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        written.append(entry["path"])
    return written


class Target(NamedTuple):
    """The content `data` to rebuild at `path`, from the app file `source` (default: path) or the stdout of `run`."""

    path: str
    data: bytes
    source: str | None = None
    run: str | None = None

    def base_entry(self) -> dict:
        entry: dict = {"path": self.path}
        if self.run:
            entry["run"] = self.run
        elif self.source and self.source != self.path:
            entry["from"] = self.source
        return entry


def _ed_script(path: str, base: bytes, target: bytes) -> bytes:
    if b"\n.\n" in b"\n" + target or not target.endswith(b"\n"):
        raise DeriveError(f"{path}: an ed script cannot express this target (a lone '.' line or no final newline)")
    with _tmp(base) as base_file, _tmp(target) as target_file:
        return subprocess.run(["diff", "-e", base_file, target_file], capture_output=True).stdout


def _write_edit(spec_dir: Path, target: Target, base: bytes) -> str | None:
    """Write the ed script that turns `base` into the target; None (and no script) when they are equal."""
    edit = Path("edits") / (target.path + ".ed")
    if base == target.data:
        (spec_dir / edit).unlink(missing_ok=True)  # an earlier entry's script
        return None
    script = _ed_script(target.path, base, target.data)
    (spec_dir / edit).parent.mkdir(parents=True, exist_ok=True)
    (spec_dir / edit).write_bytes(script)
    return edit.as_posix()


def _save(spec_dir: Path, entry: dict) -> None:
    """Replace the entry of the same path in spec_dir/derive.json, or append it."""
    spec = [e for e in entries(spec_dir) if e["path"] != entry["path"]] + [entry]
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / SPEC).write_text(json.dumps(spec, indent=2) + "\n", encoding="utf-8")


def make(spec_dir: Path, app: Path, target: Target) -> dict:
    """Append an entry that rebuilds the target from the app; verify it round-trips."""
    entry = target.base_entry()
    base = _base(app, entry)
    entry["base_sha256"] = digest(base)
    edit = _write_edit(spec_dir, target, base)
    if edit:
        entry["edit"] = edit
    entry["sha256"] = digest(target.data)
    if build(app, spec_dir, entry) != target.data:
        raise DeriveError(f"{target.path}: the entry does not reproduce the target")
    _save(spec_dir, entry)
    return entry


class _tmp:
    def __init__(self, data: bytes):
        import tempfile

        self._file = tempfile.NamedTemporaryFile(delete=False)
        self._file.write(data)
        self._file.close()

    def __enter__(self) -> str:
        return self._file.name

    def __exit__(self, *_exc) -> None:
        Path(self._file.name).unlink(missing_ok=True)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="derive")
    sub = parser.add_subparsers(dest="command", required=True)
    p_apply = sub.add_parser("apply")
    p_apply.add_argument("spec_dir", type=Path)
    p_apply.add_argument("app", type=Path, nargs="?", default=Path("."))
    p_make = sub.add_parser("make")
    p_make.add_argument("spec_dir", type=Path)
    p_make.add_argument("path")
    p_make.add_argument("target", type=Path)
    group = p_make.add_mutually_exclusive_group()
    group.add_argument("--from", dest="source")
    group.add_argument("--run")
    p_make.add_argument("--app", type=Path, default=Path("."))
    args = parser.parse_args(argv[1:])
    try:
        if args.command == "apply":
            for path in apply(args.spec_dir, args.app.resolve()):
                print(path)
        else:
            target = Target(args.path, args.target.read_bytes(), args.source, args.run)
            print(json.dumps(make(args.spec_dir, args.app.resolve(), target)))
    except (DeriveError, OSError, KeyError, json.JSONDecodeError) as err:
        print(f"derive: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
