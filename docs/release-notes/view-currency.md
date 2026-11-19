# View currency release notes

This release adds checks that tell you whether a diagram exists in the committed revision, whether it was generated from the current inputs, and whether it still shows the architecture. It also reworks the deployment view. This page lists what a user can see change.

## The `views` workspace key

A new top-level key `views` declares each view: a stable `id`, `kind`, `origin` (`generated` or `authored`), `owner`, `scope`, `abstraction`, `environment`, coverage, source, output paths, projection rules, mapping, and comparison policy. The schema is closed, and a definition that is not valid is `CO-DGM-001` (exit 2). A profile cannot set `views`. See [`views`](../reference/workspace.md#views).

- An authored diagram maps its elements to CALM identities by explicit, stable mappings. A provider ordinal is never a view ID or a mapping key.
- An image-only diagram (`png`, `jpeg`, `gif`) gets the presence and provenance checks only, unless supplemental structured mappings exist.
- A generated view writes to a Git-tracked path, with a manifest next to each artifact. `projection.output` keeps its default `build/concordia/structurizr`.
- Concordia reports suggested default views (system context, container, production deployment) as the information diagnostic `CO-DGM-005`. A missing suggested view never fails a run.

## Three checks and a pack

Each view gets three independent checks, one rule each, in the built-in pack `views` (version `1.0.0`, family `view-currency`).

| Rule | Check | Finding kind |
|---|---|---|
| `VIEW-PRES-001` | Presence: source, outputs, exports, and manifest are in the tracked revision. | `view-absent` |
| `VIEW-FRESH-001` | Freshness: the artifact and manifest match a regeneration from the current inputs. | `view-stale` |
| `VIEW-CORR-001` | Correspondence: the diagram matches the declared and observed architecture. | `view-drift` |

- The pack applies automatically when the workspace declares a view. `builtin:views` in `policies.packs` is refused with `CO-POL-001`, and `policy add builtin:views` fails.
- These rules are not advisory by default. The severity of a finding is `comparison.severity` of the view (default `error`), so a violation at or above `check.failOn` gives exit code 1, and an `unknown` result gives exit code 4 when `check.unknown` is `fail`. Set a mode per family with `check.enforcement` (family `view-currency`), for example `shadow` while you roll the views out.
- A workspace without `views` is not affected.
- A fourth interface, assessment currency, lists the assessments that cover a view. It is filled when the workspace has assessments. See the [review currency release notes](review-currency.md).

## View manifests and digests

A manifest sits next to each generated artifact. It is canonical JSON with no timestamp. It holds the view ID, the definition digest, the digest of each artifact, the input digests, a versioned semantic digest, and a separate artifact digest, so there is no self-hash cycle. `check` recomputes the digests and regenerates the artifact. It compares digests and bytes, never revisions. See [Results reference](../reference/results.md#views).

## Commands and output

- `concordia render --views [--view-id <id>]` writes the artifact, the exports, and the manifest of each generated view. It needs a current index and does not combine with `--results`, `--view`, `--focus`, `--architecture`, `--workspace-out`, `--finding`, or `--format`. Plain `render` is unchanged. See [render --views](../reference/cli.md#render---views).
- `check` and `index` never write the files of a view.
- `results.json` gains a `views` array and a `diagramCurrency` area. `schemaVersion` stays `1.1`. A view finding has `related: [{kind: "view", id}]`. SARIF, the run manifest, and the evaluation bundle carry view inputs and results.
- The results gain the relationships `DEPICTS` (view to architecture subject) and `GENERATED_FROM` (artifact to view snapshot).
- New diagnostics `CO-DGM-001` to `CO-DGM-005`. See [Diagnostics](../reference/diagnostics.md).

## Confined rendering

Exports run in a confined runner: scrubbed environment, scratch directory, a pinned Structurizr CLI version, and deny-by-default includes. A rendering input that is refused is `CO-DGM-003` (exit 2). A Structurizr CLI that is not the pinned version is `CO-DGM-004` (exit 5). A missing CLI is `CO-PRJ-001` (exit 5).

## The deployment view

The deployment view now has one deployment environment for each declared environment, with declared and observed placement and status tags. It is no longer skipped (`CO-PRJ-008`) when CALM has no `deployed-in` relationship but observed placement exists. Observed placement comes from the derived `placed-in` facts through the `workload` and `infraResource` bindings. It does not need the `infrastructure` pack. Unbound and ambiguous endpoints show as gaps. A workspace without `views` and without declared environments renders as before, including the "Default" environment.

## What needs your action

- To use the checks, declare `views` and commit the diagram files and manifests. Run `concordia render --views` after an input changes.
- Decide the mode for the new gate before you commit to it: the rules gate by default once a view exists.
- See [Keep diagrams current](../how-to/keep-diagrams-current.md) and [Render diagrams](../how-to/render-diagrams.md).
