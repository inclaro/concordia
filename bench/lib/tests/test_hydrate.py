import importlib.util
from pathlib import Path

import yaml

SCRIPT = Path(__file__).resolve().parent.parent / "bin" / "hydrate-namespace.py"
spec = importlib.util.spec_from_file_location("hydrate_namespace", SCRIPT)
hydrate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hydrate)

MANIFEST = """\
apiVersion: apps/v1
kind: Deployment
metadata: {name: argocd-server}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: argocd-server}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: rb}
subjects:
  - {kind: ServiceAccount, name: argocd-server}
  - {kind: ServiceAccount, name: strimzi-cluster-operator, namespace: myproject}
  - {kind: User, name: alice}
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata: {name: vw}
webhooks:
  - name: w
    clientConfig: {service: {name: webhook}}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: kept, namespace: other}
"""


def hydrated(tmp_path, text=MANIFEST):
    source, target = tmp_path / "in.yaml", tmp_path / "out.yaml"
    source.write_text(text)
    assert hydrate.main(["hydrate", str(source), str(target), "argocd"]) == 0
    return {d["metadata"]["name"]: d for d in yaml.safe_load_all(target.read_text())}


def test_service_account_subject_gets_namespace_and_existing_is_kept(tmp_path):
    subjects = hydrated(tmp_path)["rb"]["subjects"]
    assert subjects[0]["namespace"] == "argocd"
    assert subjects[1]["namespace"] == "myproject"
    assert "namespace" not in subjects[2]


def test_cluster_scoped_kinds_stay_without_namespace(tmp_path):
    docs = hydrated(tmp_path)
    roles = [d for d in yaml.safe_load_all((tmp_path / "out.yaml").read_text()) if d["kind"] == "ClusterRole"]
    assert "namespace" not in roles[0]["metadata"]
    assert "namespace" not in docs["vw"]["metadata"]


def test_namespaced_objects_and_webhook_services(tmp_path):
    docs = hydrated(tmp_path)
    deployments = [d for d in yaml.safe_load_all((tmp_path / "out.yaml").read_text()) if d["kind"] == "Deployment"]
    assert deployments[0]["metadata"]["namespace"] == "argocd"
    assert docs["rb"]["metadata"]["namespace"] == "argocd"
    assert docs["kept"]["metadata"]["namespace"] == "other"
    assert docs["vw"]["webhooks"][0]["clientConfig"]["service"]["namespace"] == "argocd"
