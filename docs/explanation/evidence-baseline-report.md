# Evidence baseline report

This report compares each gap of the infrastructure-evidence PRD (section 1.3, "Gaps in the as-built product") with the code at commit `78466fa` (HEAD when the change `add-evidence-foundation` started). It is the MIG-01 deliverable: it exists before any normalization behavior changes. Every path and line below refers to that commit, so the line numbers do not move when later work edits the files.

A gap is **confirmed** when the code at HEAD behaves as the PRD describes. It is **changed** when the code differs. The characterization tests named `TestBaselineGap...` pin each confirmed gap. The slice that closes a gap rewrites its test in the same commit.

## Gap table

| # | Gap | Code at HEAD | Status | Pinned by |
| --- | --- | --- | --- | --- |
| 1 | Concordia drops infrastructure edges. The table maps 22 native kinds. | `internal/observation/normalize.go:38` (`nativeKindTable`); `internal/observation/adapter.go:750-754` counts unmapped native types and creates no edge | Confirmed | `TestBaselineGapNativeKindTableHas22Entries`, `TestBaselineGapUnmappedNativeKindsAreOnlyCounted` |
| 2 | Infrastructure `IMPORTS` looks like a code import. | `internal/observation/normalize.go:39` (`IMPORTS` to `KindImports`, `syntactic`, no source check) | Confirmed | `TestBaselineGapTokenResolution` |
| 3 | `USAGE` loses its meaning. | `internal/observation/normalize.go:42` (`USAGE` to `ambiguous-usage`); `internal/conformance/evaluate.go:318` and `:390` count it only as `unresolvedReferences` | Confirmed | `TestBaselineGapTokenResolution` |
| 4 | `CONFIGURES` loses its strategy. | `internal/observation/normalize.go:56` (`CONFIGURES` to `configures`, no strategy) | Confirmed | `TestBaselineGapTokenResolution` |
| 5 | The kind `deploys` has no source. | `internal/observation/evidence.go:72` (`KindDeploys`); no `nativeKindTable` entry maps to it; `internal/policy/compile.go:809` raises CO-POL-009 for a kind that the provider does not supply | Confirmed | `TestBaselineGapNativeKindTableHas22Entries` |
| 6 | Concordia drops endpoints that have no binding. | `internal/conformance/reconcile.go:118` (missing endpoint: skipped, not counted); `:130` (`scan.Unbound++`, counted); `internal/conformance/evaluate.go:374` and `:388` | Confirmed | `TestBaselineGapScanEdgesSkipsMissingEndpointWithoutCount` |
| 7 | A pair result can hide a violation. | `internal/conformance/reconcile.go:182` (`permittingRelationships`); `:197` (`composed-of` and `deployed-in` permit both directions; the edge kind is not checked at pair level) | Confirmed | `TestBaselineGapDeployedInPermitsBothDirections` |
| 8 | Selectors are not sufficient. | `internal/policy/compile.go:51` (`selectorKeys`: `calmNodes`, `nodeTypes`, `tags`, `ref`); `:319-330` (other keys give CO-POL-002) | Confirmed | none; the existing compile tests cover the CO-POL-002 rejection |
| 9 | Bindings cannot identify deployed resources. | `internal/binding/resolve.go:26-30` (the five match kinds `sourceRoot`, `package`, `qualifiedName`, `buildTarget`, `otelService`) | Confirmed | none |
| 10 | Concordia does not model environments. | `internal/workspace/workspace.go:130-131` (`Runtime.Environment`) and `:593` (the only environment setting); `internal/projection/deployment.go:12` (the one deployment environment, "Default") | Confirmed | none |
| 11 | Coverage is based on files. | `internal/observation/coverage.go:110` (`ComputeFileCoverage`); `internal/observation/evidence.go:233` (`FileCoverage`); `:188-192` (`Capabilities`: one list of supported kinds, no coverage for each predicate) | Confirmed | `TestBaselineGapProviderCapsHasOnlySupportedCanonicalKinds` |
| 12 | `ProviderCaps` lists supported kinds only. | `internal/policy/types.go:243` (`ProviderCaps` has the field `SupportedCanonicalKinds` only) | Confirmed | `TestBaselineGapProviderCapsHasOnlySupportedCanonicalKinds` |
| 13 | The CBM version gate is a version range. | `internal/observation/client.go:52` (`supportedMinor = 12`) and `:58` (`AssessVersion`) | Changed (the range is 0.12.x, not 0.9.x); the range-based gate itself is confirmed | `TestBaselineGapAssessVersion` |
| 14 | Confidence is one scale. | `internal/observation/evidence.go:133` (`EvidenceEdge.Confidence`, the only quality value) | Confirmed | none |
| 15 | The deployment view needs CALM `deployed-in`, and Git does not track it. | `internal/projection/dsl.go:646` (CO-PRJ-008 without `deployed-in`); `internal/cli/init.go:50` (`build/concordia/` is ignored) | Confirmed | none |
| 16 | A waiver applies to one finding only. | `internal/exceptions/waivers.go:63-65` (`Waiver.Finding`, `Waiver.Rule`); `internal/conformance/fingerprint.go:13` (`Parts`) and `:28` (`Fingerprint`) | Confirmed | none; `test/golden/fingerprints.json` pins the fingerprint |
| 17 | Concordia does not model ADRs or assessments. | `internal/exceptions/waivers.go:63` is the only approval-like record; no file under `internal/` has an ADR, assessment, or approval type | Confirmed | none |

Rows 11 and 12 both belong to the PRD gap "Coverage is based on files". Row 12 has its own entry because a characterization test pins the type separately.

## Differences from the as-built specification

The PRD (section 1.2.1) lists two differences between the as-built specification [S18] at `55d4f8b` and the code. Both hold at HEAD.

### CBM version gate

[S18] says the adapter supports CBM 0.9.x. At HEAD, `AssessVersion` (`internal/observation/client.go:58`) gives these results:

- 0.12.x is `supported`, with no diagnostic.
- 0.13 and later, below 1.0, is `degraded`, with a CO-OBS-004 warning.
- Versions below 0.12, and 1.x, fail with CO-OBS-004 (exit code 5).

CBM 0.12.1 is therefore supported by version number now. The evidence extension still replaces the version range with a tested capability profile (EVD-01). `TestBaselineGapAssessVersion` pins the results for 0.12.x, 0.13.0, 0.11.0, and 1.0.0.

### CALM release lines

[S18] says CALM 1.2 is the only supported release and that a 1.3 `$schema` gives CO-INT-003. At HEAD, `CalmSchemaVersions` (`internal/workspace/lock.go:22`) is `["1.2", "1.3"]`. Decorators are validated against the release of the architecture. A 1.2 document cannot carry the 1.3 decorator fields `$schema` and `target-type` (CO-INT-008). Any other release gives CO-INT-003 (`internal/intent/schemas.go:113`). The fixture `test/fixtures/calm/decorator-1.3-fields-under-1.2.json` and the tests in `internal/cli/decorator_release_test.go` pin this behavior for acceptance fixture A20: exit 2 with CO-INT-008 under 1.2, a pass under 1.3, and CO-INT-003 under 1.4.
