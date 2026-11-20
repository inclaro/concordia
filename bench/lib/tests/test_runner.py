import subprocess

import pytest

from concordia_bench import runner


def make_mutation(home, repo="r", name="m"):
    base = home / "mutations" / repo / name
    (base / "files" / "pkg").mkdir(parents=True)
    (base / "files" / "pkg" / "new.go").write_text("package pkg\n")
    (base / "files" / "keep.txt").write_text("mutated\n")
    (base / "message.txt").write_text("feat: mutate\n")


def head(app):
    return subprocess.run(["git", "rev-parse", "HEAD"], cwd=app, capture_output=True, text=True, check=True).stdout.strip()


def test_run_never_raises_and_reports_timeout(tmp_path):
    assert runner.run(["sh", "-c", "echo out; echo err >&2; exit 3"], cwd=tmp_path) == runner.Completed(3, "out\n", "err\n")
    assert runner.run(["sleep", "5"], cwd=tmp_path, timeout=0.2).exit == -1
    assert runner.run(["definitely-not-a-binary"], cwd=tmp_path).exit == -1


def test_apply_mutation_commits_and_revert_restores(git_repo, tmp_path):
    home = tmp_path / "home"
    make_mutation(home)
    before = head(git_repo)
    sha, files = runner.apply_mutation("r", "m", git_repo, home)
    assert sha == before
    assert files == ["keep.txt", "pkg/new.go"]
    assert head(git_repo) != before
    log = subprocess.run(["git", "log", "-1", "--format=%s"], cwd=git_repo, capture_output=True, text=True).stdout.strip()
    assert log == "feat: mutate"
    assert (git_repo / "pkg" / "new.go").exists()
    runner.revert(sha, files, git_repo)
    assert head(git_repo) == before
    assert not (git_repo / "pkg" / "new.go").exists()
    assert (git_repo / "keep.txt").read_text() == "keep\n"


def test_apply_mutation_failure_leaves_repo_clean(git_repo, tmp_path):
    home = tmp_path / "home"
    make_mutation(home)
    (home / "mutations" / "r" / "m" / "message.txt").unlink()
    before = head(git_repo)
    try:
        runner.apply_mutation("r", "m", git_repo, home)
    except OSError:
        pass
    assert head(git_repo) == before
    assert not (git_repo / "pkg").exists()


def test_apply_mutation_deletes_listed_paths_and_revert_restores(git_repo, tmp_path):
    (git_repo / "gone").mkdir()
    (git_repo / "gone" / "a.go").write_text("package gone\n")
    subprocess.run(["git", "add", "-A"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "gone"], cwd=git_repo, check=True)
    base = tmp_path / "home" / "mutations" / "r" / "del"
    base.mkdir(parents=True)
    (base / "message.txt").write_text("chore: delete\n")
    (base / "delete.txt").write_text("# comment\ngone/a.go\n\nkeep.txt\n")
    before = head(git_repo)
    sha, files = runner.apply_mutation("r", "del", git_repo, tmp_path / "home")
    assert files == ["gone/a.go", "keep.txt"]
    assert not (git_repo / "gone" / "a.go").exists() and not (git_repo / "keep.txt").exists()
    tracked = subprocess.run(["git", "ls-files"], cwd=git_repo, capture_output=True, text=True).stdout.split()
    assert "keep.txt" not in tracked
    runner.revert(sha, files, git_repo)
    assert head(git_repo) == before
    assert (git_repo / "gone" / "a.go").read_text() == "package gone\n"
    assert (git_repo / "keep.txt").read_text() == "keep\n"


def test_run_drops_ci_and_merges_extra_env(tmp_path, monkeypatch):
    monkeypatch.setenv("CI", "true")
    done = runner.run(["sh", "-c", 'echo "${CI-unset} $EXTRA"'], cwd=tmp_path, env={"EXTRA": "x"})
    assert done.stdout == "unset x\n"


def test_context_creates_and_deletes_a_new_branch_from_head(git_repo):
    from concordia_bench.outcome import on_branch

    ctx = runner.Context("r", app=git_repo)
    (git_repo / "oracle.txt").write_text("approved\n")
    subprocess.run(["git", "add", "-A"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "oracle"], cwd=git_repo, check=True)
    with on_branch(ctx, "feature/bench-change", create=True):
        current = subprocess.run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=git_repo, capture_output=True, text=True).stdout.strip()
        assert current == "feature/bench-change"
        assert (git_repo / "oracle.txt").exists()  # branched from the oracle's HEAD
    branches = subprocess.run(["git", "branch", "--list", "feature/*"], cwd=git_repo, capture_output=True, text=True).stdout
    assert branches.strip() == ""
    assert head(git_repo) == subprocess.run(["git", "rev-parse", "main"], cwd=git_repo, capture_output=True, text=True).stdout.strip()


def test_concordia_check_deletes_stale_results(tmp_path, monkeypatch):
    stale = tmp_path / runner.RESULTS_PATH
    stale.parent.mkdir(parents=True)
    stale.write_text('{"stale": true}')
    monkeypatch.setattr(runner, "run", lambda argv, cwd=None, timeout=0, env=None: runner.Completed(7, "", ""))
    assert runner.concordia_check(True, app=tmp_path) == (7, {})


def test_concordia_check_reads_results_and_passes_offline(tmp_path, monkeypatch):
    seen = []

    def fake(argv, cwd=None, timeout=0, env=None):
        seen.append(argv)
        if "check" in argv:
            (tmp_path / runner.RESULTS_PATH).write_text('{"schemaVersion": "1.1"}')
        return runner.Completed(1, "", "")

    monkeypatch.setattr(runner, "run", fake)
    (tmp_path / "build" / "concordia").mkdir(parents=True)
    code, results = runner.concordia_check(True, app=tmp_path)
    assert (code, results) == (1, {"schemaVersion": "1.1"})
    assert seen == [["concordia", "--offline", "index"], ["concordia", "--offline", "check", "--no-color"]]
    seen.clear()
    runner.concordia_check(False, index=False, app=tmp_path)
    assert seen == [["concordia", "check", "--no-color"]]
    seen.clear()
    runner.concordia_run(runner.CheckRun(index=False, args=("--changed", "main")), app=tmp_path)
    assert seen == [["concordia", "check", "--no-color", "--changed", "main"]]


def test_workspace_parses_concordia_yaml(tmp_path):
    (tmp_path / "concordia.yaml").write_text("architecture: {source: a.json}\n")
    assert runner.workspace(tmp_path) == {"architecture": {"source": "a.json"}}


def test_env_overrides(monkeypatch, tmp_path):
    monkeypatch.setenv("CONCORDIA_BENCH_APP", str(tmp_path / "a"))
    monkeypatch.setenv("CONCORDIA_BENCH_LOGS", str(tmp_path / "l"))
    monkeypatch.setenv("CONCORDIA_BENCH_HOME", str(tmp_path / "h"))
    assert (runner.app_dir(), runner.logs_dir(), runner.home_dir()) == (tmp_path / "a", tmp_path / "l", tmp_path / "h")


def test_apply_mutation_derives_files_and_revert_restores(git_repo, tmp_path):
    from concordia_bench import derive

    (git_repo / "upstream.yaml").write_text("a: 1\nb: 2\n")
    subprocess.run(["git", "add", "-A"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "upstream"], cwd=git_repo, check=True)
    base = tmp_path / "home" / "mutations" / "r" / "derived"
    base.mkdir(parents=True)
    (base / "message.txt").write_text("feat: derived\n")
    # In order: the copy reads upstream.yaml before the second entry changes it.
    derive.make(base, git_repo, derive.Target("ignored/copy.yaml", b"a: 1\nb: 2\nc: 4\n", source="upstream.yaml"))
    derive.make(base, git_repo, derive.Target("upstream.yaml", b"a: 1\nb: 3\n"))
    (git_repo / ".gitignore").write_text("ignored/\n")
    subprocess.run(["git", "add", "-A"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "ignore"], cwd=git_repo, check=True)
    before = head(git_repo)
    sha, files = runner.apply_mutation("r", "derived", git_repo, tmp_path / "home")
    assert files == ["ignored/copy.yaml", "upstream.yaml"]
    committed = subprocess.run(["git", "show", "--name-only", "--format=", "HEAD"], cwd=git_repo, capture_output=True, text=True).stdout.split()
    assert committed == ["ignored/copy.yaml", "upstream.yaml"]  # force-added despite .gitignore
    assert (git_repo / "upstream.yaml").read_text() == "a: 1\nb: 3\n"
    runner.revert(sha, files, git_repo)
    assert head(git_repo) == before
    assert (git_repo / "upstream.yaml").read_text() == "a: 1\nb: 2\n"
    assert not (git_repo / "ignored" / "copy.yaml").exists()


def test_apply_mutation_with_a_stale_base_leaves_repo_clean(git_repo, tmp_path):
    from concordia_bench import derive

    make_mutation(tmp_path / "home")
    base = tmp_path / "home" / "mutations" / "r" / "m"
    derive.make(base, git_repo, derive.Target("keep.txt", b"keep\nmore\n"))
    (git_repo / "keep.txt").write_text("drifted\n")
    subprocess.run(["git", "commit", "-q", "-am", "drift"], cwd=git_repo, check=True)
    before = head(git_repo)
    with pytest.raises(derive.DeriveError):
        runner.apply_mutation("r", "m", git_repo, tmp_path / "home")
    assert head(git_repo) == before
    assert not (git_repo / "pkg").exists()
    assert (git_repo / "keep.txt").read_text() == "drifted\n"
