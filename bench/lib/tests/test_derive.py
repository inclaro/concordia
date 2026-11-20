import json
import shutil
import subprocess

import pytest

from concordia_bench import derive

BASE = "one\ntwo\nthree\nfour\nfive\n"


def gnu_diff_e(tmp_path, base: str, target: str) -> str:
    (tmp_path / "b").write_text(base)
    (tmp_path / "t").write_text(target)
    return subprocess.run([shutil.which("diff"), "-e", tmp_path / "b", tmp_path / "t"], capture_output=True, text=True).stdout


@pytest.mark.parametrize(
    "target",
    [
        "zero\none\ntwo\nthree\nfour\nfive\n",  # add at the top (0a)
        "one\ntwo\nthree\nfour\nfive\nsix\n",  # add at the bottom
        "one\nTWO\nthree\nfour\nFIVE\nfive2\n",  # change, one line into two
        "one\nfive\n",  # delete a range
        "one\nX\nthree\nfive\nY\n",  # several commands: line numbers are applied bottom-up
        "",  # delete everything
    ],
)
def test_apply_ed_reproduces_gnu_diff_e(tmp_path, target):
    script = gnu_diff_e(tmp_path, BASE, target)
    assert derive.apply_ed(BASE.encode(), script) == target.encode()


def test_apply_ed_rejects_bad_scripts():
    with pytest.raises(derive.DeriveError, match="unsupported"):
        derive.apply_ed(BASE.encode(), "1s/one/two/\n")
    with pytest.raises(derive.DeriveError, match="closing"):
        derive.apply_ed(BASE.encode(), "1c\nx\n")
    with pytest.raises(derive.DeriveError, match="outside"):
        derive.apply_ed(BASE.encode(), "9d\n")
    with pytest.raises(derive.DeriveError, match="outside"):
        derive.apply_ed(BASE.encode(), "9a\nx\n.\n")


def make_app(tmp_path):
    app = tmp_path / "app"
    (app / "manifests").mkdir(parents=True)
    (app / "manifests" / "install.yaml").write_text(BASE)
    return app


def test_make_from_round_trips_and_apply_writes(tmp_path):
    app, spec = make_app(tmp_path), tmp_path / "spec"
    target = b"one\ntwo\nbench\nfour\nfive\n"
    entry = derive.make(spec, app, derive.Target("bench-env/install.yaml", target, source="manifests/install.yaml"))
    assert entry["from"] == "manifests/install.yaml" and entry["edit"] == "edits/bench-env/install.yaml.ed"
    script = (spec / entry["edit"]).read_text()
    assert "three" not in script  # the script holds the new lines only
    assert json.loads((spec / "derive.json").read_text()) == [entry]
    assert derive.apply(spec, app) == ["bench-env/install.yaml"]
    assert (app / "bench-env" / "install.yaml").read_bytes() == target


def test_make_run_without_edit_and_replace(tmp_path):
    app, spec = make_app(tmp_path), tmp_path / "new" / ".bench"
    run = "sed 's/^/- /' manifests/install.yaml"
    rendered = subprocess.run(["bash", "-c", run], cwd=app, capture_output=True).stdout
    derive.make(spec, app, derive.Target("out.yaml", rendered + b"extra\n", run=run))
    entry = derive.make(spec, app, derive.Target("out.yaml", rendered, run=run))  # replaces the entry, needs no edit
    assert "edit" not in entry and entry["run"] == run
    assert not (spec / "edits" / "out.yaml.ed").exists()
    assert derive.entries(spec) == [entry]
    (app / "out.yaml").unlink(missing_ok=True)
    assert derive.apply(spec, app) == ["out.yaml"]
    assert (app / "out.yaml").read_bytes() == rendered


def test_apply_runs_entries_in_order(tmp_path):
    app, spec = make_app(tmp_path), tmp_path / "spec"
    derive.make(spec, app, derive.Target("manifests/install.yaml", b"one\nfive\n"))
    (app / "manifests" / "install.yaml").write_bytes(b"one\nfive\n")
    derive.make(spec, app, derive.Target("copy.yaml", b"one\nfive\nsix\n", source="manifests/install.yaml"))
    (app / "manifests" / "install.yaml").write_text(BASE)
    assert derive.apply(spec, app) == ["manifests/install.yaml", "copy.yaml"]
    assert (app / "copy.yaml").read_text() == "one\nfive\nsix\n"


def test_digest_mismatch_fails(tmp_path):
    app, spec = make_app(tmp_path), tmp_path / "spec"
    derive.make(spec, app, derive.Target("x.yaml", b"one\n", source="manifests/install.yaml"))
    (app / "manifests" / "install.yaml").write_text(BASE + "drift\n")
    with pytest.raises(derive.DeriveError, match="not the pinned content"):
        derive.apply(spec, app)
    (app / "manifests" / "install.yaml").write_text(BASE)
    (spec / "edits" / "x.yaml.ed").write_text("2,5d\n1c\nother\n.\n")
    with pytest.raises(derive.DeriveError, match="does not match its sha256"):
        derive.apply(spec, app)
    assert not (app / "x.yaml").exists()


def test_failing_run_and_missing_base_fail(tmp_path):
    app = make_app(tmp_path)
    with pytest.raises(derive.DeriveError, match="run exited 3"):
        derive.make(tmp_path / "s", app, derive.Target("x", b"x\n", run="exit 3"))
    with pytest.raises(derive.DeriveError, match="does not exist"):
        derive.make(tmp_path / "s", app, derive.Target("x", b"x\n", source="missing.yaml"))


def test_cli_apply_prints_written_paths(tmp_path, capsys):
    app, spec = make_app(tmp_path), tmp_path / "spec"
    derive.make(spec, app, derive.Target("a.yaml", b"a\n", source="manifests/install.yaml"))
    assert derive.main(["derive", "apply", str(spec), str(app)]) == 0
    assert capsys.readouterr().out == "a.yaml\n"
    assert derive.main(["derive", "apply", str(tmp_path / "none"), str(app)]) == 0
    assert capsys.readouterr().out == ""
