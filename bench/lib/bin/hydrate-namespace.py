#!/opt/venv/bin/python
"""hydrate-namespace.py <in.yaml> <out.yaml> <namespace>: give namespaced manifests an explicit namespace.

Upstream install manifests often leave `metadata.namespace` to `kubectl apply -n`. Concordia
derives placement facts only from an explicit namespace, so the bench commits hydrated copies:

1. every document of a namespaced kind without `metadata.namespace` gets <namespace>;
2. every ServiceAccount subject of a (Cluster)RoleBinding without a namespace gets <namespace>
   (an empty subject namespace leaves the principal unresolved);
3. every webhook service reference without a namespace gets <namespace>.

Existing namespaces are kept.
"""

from __future__ import annotations

import sys

import yaml

CLUSTER_SCOPED = frozenset({
    "Namespace", "ClusterRole", "ClusterRoleBinding", "CustomResourceDefinition",
    "MutatingWebhookConfiguration", "ValidatingWebhookConfiguration", "APIService", "PriorityClass",
    "StorageClass", "ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding", "MutatingAdmissionPolicy",
})
BINDINGS = frozenset({"RoleBinding", "ClusterRoleBinding"})


def _set_metadata(doc: dict, namespace: str) -> None:
    if doc.get("kind") in CLUSTER_SCOPED:
        return
    metadata = doc.setdefault("metadata", {})
    metadata.setdefault("namespace", namespace)


def _set_subjects(doc: dict, namespace: str) -> None:
    if doc.get("kind") not in BINDINGS:
        return
    for subject in doc.get("subjects") or []:
        if subject.get("kind") == "ServiceAccount" and not subject.get("namespace"):
            subject["namespace"] = namespace


def _set_webhooks(doc: dict, namespace: str) -> None:
    for webhook in doc.get("webhooks") or []:
        service = (webhook.get("clientConfig") or {}).get("service")
        if isinstance(service, dict) and not service.get("namespace"):
            service["namespace"] = namespace


def hydrate(doc: dict, namespace: str) -> dict:
    _set_metadata(doc, namespace)
    _set_subjects(doc, namespace)
    _set_webhooks(doc, namespace)
    return doc


def hydrate_text(text: str, namespace: str) -> str:
    docs = [hydrate(d, namespace) for d in yaml.safe_load_all(text) if isinstance(d, dict)]
    return "---\n".join(yaml.safe_dump(d, sort_keys=False) for d in docs)


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print(__doc__.splitlines()[0], file=sys.stderr)
        return 2
    source, target, namespace = argv[1:]
    with open(source, encoding="utf-8") as handle:
        text = handle.read()
    with open(target, "w", encoding="utf-8") as handle:
        handle.write(hydrate_text(text, namespace))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
