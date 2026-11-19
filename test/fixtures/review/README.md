# Review fixtures

Fixtures of the review-currency suites (`test/e2e/review_test.go`, `test/e2e/review_docs_test.go`, and the replay
tests of `test/integration/review_*_test.go`).

- `cbm-0.12.1/`: the CBM 0.12.1 recordings that the replay tests use. `fixture.json` lists the variants.
- `workspace/`: the files that go over a copy of `test/fixtures/infra` to make the review workspace: two environments
  (`production` in `k8s/prod`, `staging` in `k8s/staging`), the same `api` Deployment and `payments` namespace in both,
  one image repository shared by both workloads, and a binding set. The manifests `k8s/prod/httproute.yaml`,
  `k8s/prod/deployment.yaml`, and `k8s/staging/serviceaccount.yaml` remove the dangling references of the infra fixture
  (a missing Gateway, a missing Secret, a missing ServiceAccount), because `assessment approve` refuses to approve a
  subject that it cannot compute.
- `variants/removed-binding/`: the binding set without the binding that attributes the `api` workloads to `api-service`.
- `assessments/`: assessment documents and the assumption `ASM-ROUTE-1`. `ASM-EDGE` and `ASM-AUTHZ` assess the runtime
  identity of the API service, `ASM-ROUTE` its public entry points, `ASM-SECRET` its secret references, `ASM-STAGING` the
  identity in staging, `ASM-EXPIRING` the identity with an approval that ends on 2026-11-10, `ASM-FLAG` carries
  `reviewed: true` and no approval, and `ASM-TERRAFORM` uses the selector `via: terraform`, which the capability
  profile of CBM 0.12.1 does not map.
- `records/approvals/`: approval records that `concordia assessment approve` wrote on this workspace with
  `CONCORDIA_NOW=2026-11-01T10:00:00Z`, the reviewer `alice@example.com`, and the authority `security-review`
  (`protected-review`). They bind the digests of the workspace as it is here. A change to a manifest, a binding, an
  assessment document, or the review authority changes a digest, and the record then has to be written again with the
  same command.
