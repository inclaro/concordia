import json
import subprocess

import yaml

from concordia_bench import checks
from concordia_bench.runner import Completed

from conftest import finding, results

CALM = {
    "nodes": [
        {"unique-id": "a", "node-type": "service", "concordia": {"tags": ["shared"]}},
        {"unique-id": "b", "node-type": "database"},
    ],
    "relationships": [
        {"unique-id": "a-uses-b", "relationship-type": {"connects": {"source": {"node": "a"}, "destination": {"node": "b"}}}},
        {"unique-id": "x", "relationship-type": {"interacts": {"actor": "a", "nodes": ["b"]}}},
    ],
}


def write(root, rel, text):
    path = root / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text if isinstance(text, str) else yaml.safe_dump(text))
    return path


def workspace(ctx, **extra):
    ctx.ws = {
        "architecture": {"source": "architecture/x.calm.json"},
        "bindings": {"files": ["bindings/*.yaml"]},
        "policies": {"files": ["policies/**/*.policy.yaml"]},
        **extra,
    }


def test_calm_nodes_types_and_tags(make_ctx, tmp_path):
    ctx = make_ctx()
    workspace(ctx)
    write(tmp_path, "architecture/x.calm.json", json.dumps(CALM))
    ok = {"nodes": [{"id": "a", "nodeType": "service", "tags": ["shared"]}, {"id": "b", "nodeType": "database"}]}
    assert checks.calm_nodes(ctx, ok)[0]
    assert not checks.calm_nodes(ctx, {"nodes": [{"id": "a", "nodeType": "database"}]})[0]
    assert not checks.calm_nodes(ctx, {"nodes": [{"id": "b", "nodeType": "database", "tags": ["shared"]}]})[0]
    assert not checks.calm_nodes(ctx, {"nodes": [{"id": "zzz", "nodeType": "service"}]})[0]


def test_relationships_need_connects_pairs(make_ctx, tmp_path):
    ctx = make_ctx()
    workspace(ctx)
    write(tmp_path, "architecture/x.calm.json", json.dumps(CALM))
    assert checks.relationships(ctx, {"connects": [["a", "b"]]})[0]
    assert not checks.relationships(ctx, {"connects": [["b", "a"]]})[0]


def test_bound_nodes(make_ctx, tmp_path):
    ctx = make_ctx()
    workspace(ctx)
    write(tmp_path, "bindings/b.yaml", {"bindings": [{"id": "bind-a", "architectureRef": "calm:node:a"}]})
    assert checks.bound_nodes(ctx, {"ids": ["a"]})[0]
    assert not checks.bound_nodes(ctx, {"ids": ["a", "b"]})[0]


POLICY = """apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: p, version: 1.0.0}
rules:
  - id: KN-SEC-002
    name: certs
    severity: error
    family: deployment-security
    subject: {calmNodes: [a]}
    obligation:
      type: forbidden
      relation: {kind: references-secret, direction: from-workload, target: {calmNodes: [b]}}
  - id: NU-EDG-001
    name: edge
    severity: warning
    subject: {calmNodes: [a]}
    require: {edge: {target: {calmNodes: [b]}, edges: [IMPORTS]}}
  - id: FZF-CYC-001
    name: acyclic
    severity: error
    subject: {calmNodes: [a]}
    require: {graphProperty: acyclic, edges: [IMPORTS]}
"""


def test_rules_declared_predicates_keys_and_packs(make_ctx, tmp_path):
    ctx = make_ctx()
    workspace(ctx)
    ctx.ws["policies"]["packs"] = [{"use": "builtin:core"}]
    write(tmp_path, "policies/p.policy.yaml", POLICY)
    ok = {
        "rules": [
            {"id": "KN-SEC-002", "predicate": "obligation", "severity": "error", "family": "deployment-security"},
            {"id": "NU-EDG-001", "predicate": "require.edge", "severity": "warning"},
            {"id": "FZF-CYC-001", "predicate": "require.graphProperty"},
        ],
        "packs": ["builtin:core"],
    }
    assert checks.rules_declared(ctx, ok)[0]
    for bad in (
        {"rules": [{"id": "NU-EDG-001", "predicate": "require.entity"}]},
        {"rules": [{"id": "KN-SEC-002", "predicate": "forbid"}]},
        {"rules": [{"id": "NU-EDG-001", "severity": "error"}]},
        {"rules": [{"id": "KN-SEC-002", "enforcement": "mandatory"}]},
        {"rules": [{"id": "MISSING-001"}]},
        {"packs": ["builtin:infrastructure"]},
    ):
        assert not checks.rules_declared(ctx, bad)[0], bad


def test_rules_declared_accepts_listed_equivalent_predicates(make_ctx, tmp_path):
    ctx = make_ctx()
    workspace(ctx)
    write(tmp_path, "policies/p.policy.yaml", POLICY)
    assert checks.rules_declared(ctx, {"rules": [{"id": "FZF-CYC-001", "predicate": ["budget", "require.graphProperty"]}]})[0]
    assert not checks.rules_declared(ctx, {"rules": [{"id": "FZF-CYC-001", "predicate": ["budget", "forbid"]}]})[0]


def test_rules_declared_reads_local_packs(make_ctx, tmp_path):
    """A pack file listed in policies.packs (`use: <path>`, with sha256) is a declared policy too."""
    ctx = make_ctx()
    workspace(ctx)
    ctx.ws["policies"] = {"files": [], "packs": [{"use": "builtin:core"}, {"use": "policies/p.policy.yaml", "sha256": "sha256:00"}]}
    write(tmp_path, "policies/p.policy.yaml", POLICY)
    assert checks.rules_declared(ctx, {"rules": [{"id": "NU-EDG-001", "predicate": "require.edge"}], "packs": ["builtin:core"]})[0]


def test_branches_unchanged_compares_build_time_commits(make_ctx, tmp_path, monkeypatch):
    state = tmp_path / "state"
    state.mkdir()
    (state / "branches.tsv").write_text("main c0\npr/a c1\n")
    monkeypatch.setenv("CONCORDIA_BENCH_STATE", str(state))

    def at(main, branch):
        return make_ctx(runs={
            ("git", "rev-parse", "--verify", "--quiet", "refs/heads/main"): Completed(0, f"{main}\n", ""),
            ("git", "rev-parse", "--verify", "--quiet", "refs/heads/pr/a"): Completed(0 if branch else 1, f"{branch}\n", ""),
        })

    assert checks.branches_unchanged(at("c0", "c1"), {})[0]
    assert not checks.branches_unchanged(at("c0", "c9"), {})[0]  # a commit on the teammate branch
    assert not checks.branches_unchanged(at("c8", "c1"), {})[0]  # a commit on main
    assert not checks.branches_unchanged(at("c0", ""), {})[0]  # branch deleted
    (state / "branches.tsv").unlink()
    assert not checks.branches_unchanged(at("c0", "c1"), {})[0]  # nothing recorded is a broken task


def test_bound_nodes_fails_when_validate_fails(make_ctx):
    ctx = make_ctx(runs={("concordia", "bind", "validate"): Completed(2, "", "bad")})
    workspace(ctx)
    assert not checks.bound_nodes(ctx, {"ids": []})[0]


def test_workflow_requires_substrings_in_one_run_step(make_ctx, tmp_path):
    ctx = make_ctx()
    wf = {"on": "push", "jobs": {"c": {"steps": [{"uses": "x"}, {"run": "concordia index"}, {"run": "concordia --offline check --format sarif"}]}}}
    write(tmp_path, "wf.yml", wf)
    assert checks.workflow(ctx, {"path": "wf.yml", "requireRuns": [["concordia index"], ["concordia", "--offline", "check"]]})[0]
    assert not checks.workflow(ctx, {"path": "wf.yml", "requireRuns": [["index", "--offline"]]})[0]


def test_report_outputs(make_ctx):
    ctx = make_ctx()
    ctx.ws = {"report": {"outputs": [{"format": "junit", "path": "a.xml"}, {"format": "sarif", "path": "a.sarif"}]}}
    assert checks.report_outputs(ctx, {"outputs": [{"format": "junit", "path": "a.xml"}]})[0]
    assert not checks.report_outputs(ctx, {"outputs": [{"format": "junit", "path": "b.xml"}]})[0]
    ctx.ws = {}
    assert not checks.report_outputs(ctx, {"outputs": [{"format": "junit", "path": "a.xml"}]})[0]


WAIVED = {"ruleId": "R", "path": "a.go", "issue": "BENCH-1", "expires": "2099-12-31"}


def waived_ctx(make_ctx, tmp_path, waiver, disposition="waived"):
    """An active finding fails the check run (exit 1); a waived one passes it."""
    code = 1 if disposition == "active" else 0
    ctx = make_ctx(check_results=[(code, results(finding("R", "a.go", disposition, "sha256:f1"), exit_code=code))])
    workspace(ctx)
    write(tmp_path, "exceptions/waivers.yaml", {"waivers": [waiver]})
    return ctx


def test_waived_accepts_matching_waiver_with_yaml_date(make_ctx, tmp_path):
    waiver = "waivers:\n  - {id: W1, finding: 'sha256:f1', rule: R, issue: BENCH-1, expires: 2099-12-31}\n"
    ctx = waived_ctx(make_ctx, tmp_path, {})
    write(tmp_path, "exceptions/waivers.yaml", waiver)
    assert checks.waived(ctx, WAIVED)[0]


def test_waived_rejects_wrong_issue_expiry_or_disposition(make_ctx, tmp_path):
    good = {"finding": "sha256:f1", "issue": "BENCH-1", "expires": "2099-12-31"}
    assert checks.waived(waived_ctx(make_ctx, tmp_path, good), WAIVED)[0]
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, {**good, "issue": "X-1"}), WAIVED)[0]
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, {**good, "expires": "2030-01-01"}), WAIVED)[0]
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, good, disposition="active"), WAIVED)[0]
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, {**good, "finding": "sha256:other"}), WAIVED)[0]


def test_waived_fails_when_no_such_finding(make_ctx, tmp_path):
    ctx = waived_ctx(make_ctx, tmp_path, {"finding": "sha256:f1", "issue": "BENCH-1", "expires": "2099-12-31"})
    assert not checks.waived(ctx, {**WAIVED, "path": "other.go"})[0]


def test_waived_scoped_matches_by_rule_and_owner(make_ctx, tmp_path):
    scoped = {"id": "WV-2", "rule": "R", "scope": {"subject": "kn-queue"}, "owner": "team-kn", "issue": "BENCH-1", "expires": "2099-12-31"}
    check = {"ruleId": "R", "scope": True, "owner": "team-kn", "issue": "BENCH-1", "expires": "2099-12-31"}
    assert checks.waived(waived_ctx(make_ctx, tmp_path, scoped), check)[0]
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, {**scoped, "owner": "other"}), check)[0]
    finding_only = {"finding": "sha256:f1", "rule": "R", "issue": "BENCH-1", "expires": "2099-12-31"}
    assert not checks.waived(waived_ctx(make_ctx, tmp_path, finding_only), check)[0]


def test_waived_reads_register_and_validates_on_the_checked_branch(make_ctx, tmp_path):
    ctx = waived_ctx(make_ctx, tmp_path, {"finding": "sha256:f1", "issue": "BENCH-1", "expires": "2099-12-31"})
    seen = []
    original = ctx.checkout

    def checkout(branch, create=False):
        # The register only exists while the feature branch is checked out.
        path = tmp_path / "exceptions" / "waivers.yaml"
        if branch == "feature/x":
            write(tmp_path, "exceptions/waivers.yaml", {"waivers": [{"finding": "sha256:f1", "issue": "BENCH-1", "expires": "2099-12-31"}]})
        else:
            path.unlink(missing_ok=True)
        seen.append(branch)
        return original(branch, create)

    ctx.checkout = checkout
    (tmp_path / "exceptions" / "waivers.yaml").unlink()
    passed, detail = checks.waived(ctx, {**WAIVED, "branch": "feature/x", "args": ["--changed", "main"]})
    assert passed, detail
    assert seen == ["feature/x", "main"]
    assert ["concordia", "waiver", "validate"] in ctx.calls and ["check", "--changed", "main"] in ctx.calls


def test_committed_checks_branch_status_and_head(tmp_path, git_repo):
    from concordia_bench.runner import Context

    ctx = Context("fake", app=git_repo)
    assert checks.committed(ctx, {"paths": ["keep.txt"], "branch": "main"})[0]
    assert not checks.committed(ctx, {"paths": ["keep.txt"], "branch": "other"})[0]
    assert not checks.committed(ctx, {"paths": ["nope"], "branch": "main"})[0]
    (git_repo / "keep.txt").write_text("changed\n")
    assert not checks.committed(ctx, {"paths": ["keep.txt"], "branch": "main"})[0]
    subprocess.run(["git", "checkout", "-q", "-b", "feature"], cwd=git_repo, check=True)
    assert not checks.committed(ctx, {"paths": [], "branch": "main"})[0]


def test_committed_fails_on_ignored_or_untracked_paths(make_ctx):
    status = ("git", "status", "--porcelain", "--ignored", "--", "architecture")
    tree = ("git", "ls-tree", "-r", "--name-only", "HEAD", "--", "architecture")
    branch = ("git", "rev-parse", "--abbrev-ref", "HEAD")
    check = {"paths": ["architecture"], "branch": "main"}
    clean = {branch: Completed(0, "main\n", ""), tree: Completed(0, "architecture/x.calm.json\n", ""), status: Completed(0, "", "")}
    assert checks.committed(make_ctx(runs=clean), check)[0]
    ignored = {**clean, status: Completed(0, "!! architecture/x.calm.json\n", "")}
    passed, detail = checks.committed(make_ctx(runs=ignored), check)
    assert not passed and "!!" in detail
    untracked = {**clean, tree: Completed(0, "", "")}
    passed, detail = checks.committed(make_ctx(runs=untracked), check)
    assert not passed and "not in HEAD" in detail


def test_committed_detects_gitignored_file_in_real_repo(git_repo):
    from concordia_bench.runner import Context

    (git_repo / ".gitignore").write_text("*.json\n")
    (git_repo / "architecture").mkdir()
    (git_repo / "architecture" / "x.calm.json").write_text("{}")
    subprocess.run(["git", "add", "-A"], cwd=git_repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "ignore"], cwd=git_repo, check=True)
    assert not checks.committed(Context("fake", app=git_repo), {"paths": ["architecture"], "branch": "main"})[0]
