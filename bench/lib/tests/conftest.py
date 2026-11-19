from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

from concordia_bench.runner import Completed

FIXTURES = Path(__file__).parent / "fixtures"


def finding(rule_id, path, disposition="active", fingerprint="sha256:aa"):
    return {
        "ruleId": rule_id,
        "fingerprint": fingerprint,
        "kind": "forbidden-edge",
        "location": {"path": path, "line": 3},
        "disposition": disposition,
    }


def results(*findings, exit_code=1, digest="sha256:d1"):
    return {
        "schemaVersion": "1.1",
        "summary": {"exitCode": exit_code, "verdict": "violation", "resultDigest": digest},
        "findings": list(findings),
    }


class FakeCtx:
    """Replaces the process, concordia and mutation access of a real Context."""

    repo = "fake"

    def __init__(self, app, check_results=None, files=("src/a.go",), runs=None):
        self.app = Path(app)
        self.check_results = list(check_results or [])
        self.files = list(files)
        self.runs = runs or {}
        self.calls = []
        self.reverted = []
        self.ws = {}
        self.branch = "main"
        self.stderr = ""

    def run(self, argv, cwd=None, timeout=0, env=None):
        self.calls.append(list(argv))
        return self.runs.get(tuple(argv), Completed(0, "", ""))

    def concordia_check(self, offline, index=True):
        self.calls.append(["check", offline, index])
        return self.check_results.pop(0)

    def concordia_run(self, options):
        self.calls.append(["check", *options.args])
        code, doc = self.check_results.pop(0)
        return Completed(code, "", self.stderr), doc

    def checkout(self, branch, create=False):
        self.calls.append(["checkout", branch, *(["create"] if create else [])])
        previous, self.branch = self.branch, branch
        return previous

    def delete_branch(self, branch):
        self.calls.append(["delete-branch", branch])

    def apply_mutation(self, name):
        self.calls.append(["apply", name])
        return f"before-{name}", self.files

    def revert(self, before, files):
        self.reverted.append((before, files))

    def workspace(self):
        return self.ws


@pytest.fixture
def fixtures():
    return FIXTURES


@pytest.fixture
def make_ctx(tmp_path):
    def build(**kwargs):
        return FakeCtx(tmp_path, **kwargs)

    return build


@pytest.fixture
def git_repo(tmp_path):
    app = tmp_path / "app"
    app.mkdir()
    for args in (["init", "-q", "-b", "main"], ["config", "user.email", "t@t.invalid"], ["config", "user.name", "T"]):
        subprocess.run(["git", *args], cwd=app, check=True)
    (app / "keep.txt").write_text("keep\n")
    subprocess.run(["git", "add", "-A"], cwd=app, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "base"], cwd=app, check=True)
    return app
