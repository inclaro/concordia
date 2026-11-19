# Evidence foundation release notes

This release replaces the as-built evidence contract with one new contract. There is no compatibility mode, no mode switch, and no migration. This page lists what a user can see change, with the `core` rules whose results can change.

## Policy tokens have one meaning

A token resolves to a fixed set of semantic evidence kinds, the same in every workspace and run. See [Token meanings](../reference/policies.md#token-meanings).

- `IMPORTS` matches code imports only. A Terraform module import and a Kustomize import are `includes-infrastructure`. An `IMPORTS` edge from a file that is neither code nor a Kustomize file is `ambiguous` and no rule counts it (EVD-06).
- `USAGE` matches code usage only (`ambiguous-usage`, a native `USAGE` edge with no `ref`). A `USAGE` edge with a `ref` has its own token.
- `CONFIGURES` matches all five strategy kinds unless a rule names one strategy token.
- `deploys` matches `deploys-artifact`. A rule that uses it gets evidence from native `DEPLOYS` edges and ends as `pass` or `violation`; `CO-POL-009` is not raised for it when the profile supports `deploys-artifact` (EVD-07).

## The pair reconciliation considers code kinds only

`reconciliation[]` in `results.json` is built from code kinds. Facts of the kinds `deploys-artifact`, `selects-workload`, `uses-image`, and every other infrastructure, configuration, reference, documentation, diagram, protocol, and test kind never create or change an entry. The coverage count `unresolvedReferences` counts code `ambiguous-usage` edges only.

## Fingerprints have a new definition

Every fingerprint differs from the fingerprint that an earlier release gave the same finding: the hashed input starts with a version tag, and the edge kind is the semantic kind. For findings of typed obligations the environment and the occurrence discriminator are hashed too, so the same rule and subject give one fingerprint for each environment. There is no fingerprint migration. A baseline or a waiver that was written by an earlier release does not match the new fingerprints: create the baseline again with `concordia baseline create`, and add the waivers again. See [Fingerprints, baselines, and waivers](../explanation/fingerprints-baselines-waivers.md).

## `core` rules whose results can change

The `core` pack results can change when normalization changes. The change is limited to repositories that have Terraform or Kustomize files, or `IMPORTS` edges from files that are not code. A repository without them gets the same results from every rule below. The rules and the cause:

| Rule | Why the result can change |
|---|---|
| `CORE-CYC-001` | It lists `IMPORTS`. A cycle that needed an infrastructure import disappears. |
| `CORE-DECL-001` | It lists `IMPORTS`. An infrastructure import between two bound nodes is no longer an undeclared dependency. |
| `CORE-DB-001` | It lists `IMPORTS`. An infrastructure import from a client to a database is no longer a finding. |
| `CORE-BND-001` | It lists `IMPORTS`. An infrastructure import between two services is no longer a forbidden import. |
| `CORE-LYR-001` | It lists `IMPORTS`. An infrastructure import between two layers is no longer a layer violation. |
| `CORE-LYR-002` | It lists `IMPORTS`. An infrastructure import from the domain is no longer a finding. |
| `CORE-LYR-003` | It lists `IMPORTS`. An infrastructure import from shared code is no longer a finding. |
| `CORE-SEC-001` | It lists `IMPORTS`. An infrastructure import into a restricted node is no longer a finding. |
| `CORE-OBS-001` | Its default evidence is the supported code kinds. An infrastructure import no longer satisfies a required relationship, so an `unobserved` finding can appear. |

The commerce example (`test/golden/commerce/**`) has no Terraform or Kustomize files and its policy file does not use the `core` pack, so the golden run shows no change in the verdict of any rule. The golden files changed for these reasons only: the `diagnostics` entry `CO-OBS-016` (native edge kind `HAS_BRANCH`, kept as unsupported evidence), the manifest `status` key, the new fingerprints, and the new coverage entries of the evidence model. The golden run is regenerated with `CONCORDIA_UPDATE_GOLDEN=1 CONCORDIA_E2E=1 go test ./test/e2e -run TestGolden`; it rewrites only the golden files whose comparison fails.
