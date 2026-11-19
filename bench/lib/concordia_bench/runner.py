"""Process, git and workspace access for the verifier checks."""

from __future__ import annotations

import json
import os
import shutil
import subprocess
from pathlib import Path
from typing import NamedTuple

import yaml

from . import derive

DEFAULT_TIMEOUT = 900
RESULTS_PATH = "build/concordia/results.json"


class Completed(NamedTuple):
    exit: int
    stdout: str
    stderr: str


def app_dir() -> Path:
    return Path(os.environ.get("CONCORDIA_BENCH_APP", "/workspace/repo"))


def logs_dir() -> Path:
    return Path(os.environ.get("CONCORDIA_BENCH_LOGS", "/logs/verifier"))


def home_dir() -> Path:
    return Path(os.environ.get("CONCORDIA_BENCH_HOME", "/opt/concordia-bench"))


def _text(value) -> str:
    if value is None:
        return ""
    return value if isinstance(value, str) else value.decode("utf-8", "replace")


def child_env(extra: dict | None = None) -> dict:
    """The environment of a child process: without CI (approvals refuse to run under CI), plus `extra`."""
    env = {k: v for k, v in os.environ.items() if k != "CI"}
    env.update({k: str(v) for k, v in (extra or {}).items()})
    return env


def run(argv, cwd=None, timeout: float = DEFAULT_TIMEOUT, env: dict | None = None) -> Completed:
    """Run a command. Never raises on a non-zero exit; a timeout gives exit -1."""
    try:
        done = subprocess.run(
            list(argv),
            cwd=cwd or app_dir(),
            env=child_env(env),
            capture_output=True,
            text=True,
            errors="replace",
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as err:
        return Completed(-1, _text(err.stdout), _text(err.stderr) + f"\ntimeout after {timeout}s")
    except OSError as err:
        return Completed(-1, "", str(err))
    return Completed(done.returncode, done.stdout, done.stderr)


def concordia_argv(offline: bool, *args: str) -> list[str]:
    return ["concordia", *(["--offline"] if offline else []), *args]


def load_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}


class CheckRun(NamedTuple):
    """Options of one `concordia check` run."""

    offline: bool = False
    index: bool = True
    args: tuple = ()
    env: dict | None = None


def concordia_run(options: CheckRun, app: Path | None = None) -> tuple[Completed, dict]:
    """Index (optionally), check, and return the completed check and results.json (or {})."""
    app = app or app_dir()
    results = app / RESULTS_PATH
    results.unlink(missing_ok=True)
    if options.index:
        run(concordia_argv(options.offline, "index"), cwd=app, env=options.env)
    done = run(concordia_argv(options.offline, "check", "--no-color", *options.args), cwd=app, env=options.env)
    return done, load_json(results)


def concordia_check(offline: bool, index: bool = True, app: Path | None = None) -> tuple[int, dict]:
    """Index (optionally), check, and return the exit code and results.json (or {})."""
    done, results = concordia_run(CheckRun(offline, index), app)
    return done.exit, results


def _git(app: Path, *args: str) -> str:
    done = run(["git", *args], cwd=app)
    if done.exit != 0:
        raise RuntimeError(f"git {' '.join(args[:2])} failed ({done.exit}): {done.stderr.strip()}")
    return done.stdout.strip()


def mutation_dir(repo: str, name: str, home: Path | None = None) -> Path:
    return (home or home_dir()) / "mutations" / repo / name


def mutation_files(source: Path) -> list[str]:
    if not source.is_dir():
        return []
    return sorted(str(p.relative_to(source)) for p in source.rglob("*") if p.is_file())


def mutation_deletions(base: Path) -> list[str]:
    """The repo-relative paths of delete.txt (one per line; blank lines and # comments ignored)."""
    listing = base / "delete.txt"
    if not listing.is_file():
        return []
    lines = (line.strip() for line in listing.read_text(encoding="utf-8").splitlines())
    return [line for line in lines if line and not line.startswith("#")]


def revert(before: str, files: list[str], app: Path | None = None) -> None:
    # reset restores overwritten and deleted files; clean removes the created ones, also when an
    # upstream .gitignore matches them (a failed mutation leaves them unstaged).
    app = app or app_dir()
    _git(app, "reset", "-q", "--hard", before)
    if files:
        _git(app, "clean", "-fdqx", "--", *files)


def _commit_mutation(app: Path, base: Path, files: list[str], deletions: list[str]) -> None:
    message = (base / "message.txt").read_text(encoding="utf-8").strip()
    if files:
        shutil.copytree(base / "files", app, dirs_exist_ok=True)
        _git(app, "add", "-f", "--", *files)
    derived = derive.apply(base, app)
    if derived:
        _git(app, "add", "-f", "--", *derived)
    if deletions:
        _git(app, "rm", "-q", "-r", "--", *deletions)
    _git(app, "commit", "-q", "--no-verify", "-m", message)


def apply_mutation(repo: str, name: str, app: Path | None = None, home: Path | None = None) -> tuple[str, list[str]]:
    """Commit the mutation over the app: copy files/, rebuild the derive.json files, delete the delete.txt paths.

    Returns the previous HEAD and the changed (created, overwritten, deleted) files.
    """
    app = app or app_dir()
    base = mutation_dir(repo, name, home)
    files = mutation_files(base / "files")
    deletions = mutation_deletions(base)
    written = sorted(set(files) | {entry["path"] for entry in derive.entries(base)})
    changed = written + [path for path in deletions if path not in written]
    before = _git(app, "rev-parse", "HEAD")
    try:
        _commit_mutation(app, base, files, deletions)
    except Exception:
        revert(before, changed, app)
        raise
    return before, changed


def workspace(app: Path | None = None) -> dict:
    text = ((app or app_dir()) / "concordia.yaml").read_text(encoding="utf-8")
    return yaml.safe_load(text) or {}


class Context:
    """What the checks see: process runner, concordia, mutations, workspace."""

    def __init__(self, repo: str, app: Path | None = None, home: Path | None = None):
        self.repo = repo
        self.app = app or app_dir()
        self.home = home or home_dir()

    def run(self, argv, cwd=None, timeout: float = DEFAULT_TIMEOUT, env: dict | None = None) -> Completed:
        return run(argv, cwd=cwd or self.app, timeout=timeout, env=env)

    def concordia_check(self, offline: bool, index: bool = True) -> tuple[int, dict]:
        return concordia_check(offline, index=index, app=self.app)

    def concordia_run(self, options: CheckRun) -> tuple[Completed, dict]:
        return concordia_run(options, app=self.app)

    def checkout(self, branch: str, create: bool = False) -> str:
        """Check out `branch` (creating it from HEAD when `create`); return what was checked out before."""
        previous = _git(self.app, "rev-parse", "--abbrev-ref", "HEAD")
        if previous == "HEAD":
            previous = _git(self.app, "rev-parse", "HEAD")
        _git(self.app, "checkout", "-q", *(["-b"] if create else []), branch)
        return previous

    def delete_branch(self, branch: str) -> None:
        _git(self.app, "branch", "-q", "-D", branch)

    def apply_mutation(self, name: str) -> tuple[str, list[str]]:
        return apply_mutation(self.repo, name, self.app, self.home)

    def revert(self, before: str, files: list[str]) -> None:
        revert(before, files, self.app)

    def workspace(self) -> dict:
        return workspace(self.app)
