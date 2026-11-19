# Review currency

A security review says that a design was acceptable at one point in time. Review currency answers the next question: is that review still about the system you have now? This page explains how Concordia decides, from the facts of the system alone. It starts with the dependency items that an assessment names, then the digest contract that turns an item into a digest, and the rule that decides whether an old digest contract still counts.

## Dependency items

An assessment depends on facts about the system: a public route, the identity a workload runs as, the secret a container reads, the code that checks authorization. Concordia reduces each fact to a dependency item:

- `kind` is one of a closed list: `runtime-identity`, `role-binding`, `secret-reference`, `configuration-reference`, `storage-reference`, `public-entry-point`, `protocol`, `certificate-reference`, `external-destination`, `trust-context`, `data-classification`, `placement`, `binding`, `control`, `implementation`, `verification`, and `quality-scenario`. Any other kind fails validation.
- `identity` is the scoped identity of the entity. It names the thing, and the digest does not include it. A route item takes the identity of the Ingress or HTTPRoute resource in its environment, never the project-wide route node, so the same host and path in `prod` and `staging` are two items.
- `facets` are the observed facts, such as host and path.
- `implementationRefs` and `verificationRefs` name repository files that implement or verify the claim.

An item is never a copy of the system. It holds the few facts that a reviewer relied on. A change outside those facts does not touch the review.

Two situations are not an empty set. If the provider cannot extract a kind (for example certificate references), the dependency is `unsupported` and its currency is `unknown` with the reason `unsupported-family`. If the kind is supported and complete but nothing matches the selector, the dependency is an empty match: the assessment records the empty set as part of its subject.

The kinds `verification` and `quality-scenario` are the exception that depends on the workspace. When the workspace has an `assurance` key, they are supplied from the assurance evidence: a `verification` item is a check result in an environment (identity `check:<id>@<environment>`), and a `quality-scenario` item is a scenario (identity `scenario:<id>`). Only an admissible check result is an item, because an inadmissible one cannot stand behind an approval. Without the key, the two kinds stay `unsupported` and give `unsupported-family`. With the key but without a complete result or scenario source, the fact source reports the kind as `no-evidence-supplied`, and the assessment currency is `unknown` with the reason `coverage-incomplete` (the closed set of currency reasons), with the missing source in the cause detail. Both kinds belong to the trigger class `implementation-verification`, so a new outcome of a check or a changed scenario criterion is a reason for review, never a finding about the system. The fields and selector facets are in [Assessments](../reference/assessments.md#verification-and-quality-scenarios).

### References, never values

The reference kinds (`secret-reference`, `configuration-reference`, `storage-reference`, `certificate-reference`) hold identifiers and roles only: the name of the secret, its role, and the container that consumes it. A manifest can carry a literal value next to a reference. The item drops every facet that the contract does not list for a reference kind and replaces any value of a secret shape with a redaction marker. Each removal or replacement is recorded in the item, by facet name and class, never by value. No digest input, result, log, or bundle holds a secret value.

### Code and tests as evidence

Topology alone does not show that an authorization check still works. An item can therefore name implementation files and verification files. The item digest covers the digest of each file at the revision. If one of those files changes while no workload, route, or identity changes, the item digest changes and the assessment is no longer current. A named path that does not exist at the revision gives `unknown` with the reason `reference-unresolved`, not a digest of nothing.

## Digest contract

The item digest is the SHA-256 of the canonical JSON of the item's contract fields. The digest contract is a versioned list that says, for each kind, which facets enter the digest. The current version is `review-digest/1`:

| Kind | Fields in the digest |
| --- | --- |
| `runtime-identity` | `serviceAccount`, `roleReferences`, `identityProvider` |
| `role-binding` | `roleReference`, `subjects`, `scope` |
| `secret-reference` | `reference`, `role`, `consumer` |
| `configuration-reference` | `reference`, `role`, `consumer` |
| `storage-reference` | `reference`, `role`, `consumer` |
| `public-entry-point` | `host`, `path`, `backend`, `exposureClass` |
| `protocol` | `protocol`, `port`, `flow` |
| `certificate-reference` | `reference`, `role`, `consumer` |
| `external-destination` | `destination`, `protocol`, `port` |
| `trust-context` | `element`, `dimensions` |
| `data-classification` | `element`, `classification` |
| `placement` | `workload`, `environment`, `platform`, `region` |
| `binding` | `source`, `target`, `mode` |
| `control` | `control`, `target`, `configuration` |
| `implementation` | `subject` |
| `verification` | `subject`, `result` |
| `quality-scenario` | `scenario`, `responseMeasure` |

The digest follows these rules:

- A fact outside the list does not change the digest. An annotation, a label, or the order of the values of a facet is not a change to review.
- A field in the list changes the digest when its value changes. Each facet is a set: Concordia sorts and de-duplicates the values, so the order that a provider returns them in does not matter.
- Referenced files add their file digests, grouped by role and sorted by path.
- Empty fields and empty file lists are left out of the canonical form. Adding a field to the contract therefore does not change the digest of an item that has no value for it.
- The version string is not part of the digest, and neither is the identity. The identity is the key of the item; the digest is its content.
- The result does not depend on the run: two runs over the same facts give the same digest.

An approval records the contract version that it was bound under, next to the dependency digests. The capability profile, the normalization version, and the evaluator versions are recorded with it, because a change to any of them can change which facts exist.

## Equivalence

Changing the contract changes what a digest means. A version that drops a security field would let an old approval stay current for a change that the new version cannot see. So the default is that a new contract version is not equivalent to an old one, and an approval that was bound under the old version is `review-required` with the detail `digest contract changed`.

A contract can be marked `equivalentFrom` an earlier version. Concordia accepts the mark only if shipped, tested evidence holds:

1. The mark carries a fixture with at least one item of every dependency kind.
2. The two contracts give equal digests for every fixture item.
3. The new contract drops no field that the earlier contract lists.

An add-only version, which lists every old field and adds new ones, can pass. A version that drops a field cannot, even if the fixture happens to leave that field empty: rule 3 does not look at the fixture. A mark that fails any check is reported as invalid and does not count, so a wrong mark never keeps an old approval current. A chain of marks counts only when every step passes.

Under an accepted mark, an approval bound under the earlier version is compared with digests that the current contract computes. A new field that has a value on a current item shows as a modified item, which is a trigger for review in the normal way.

### Digest-impact example

The digest-impact report lists, for a contract change, which approvals are affected. Take a contract `review-digest/2` that drops `exposureClass` from `public-entry-point` and has no equivalence mark. Two approvals were bound under `review-digest/1`: `ASM-EDGE-v2-1` for `ASM-EDGE@2` and `ASM-DATA-v1-1` for `ASM-DATA@1`. The report is:

```json
{
  "current": "review-digest/2",
  "affected": [
    {
      "assessment": "ASM-DATA@1",
      "approval": "ASM-DATA-v1-1",
      "boundContract": "review-digest/1",
      "detail": "digest contract changed"
    },
    {
      "assessment": "ASM-EDGE@2",
      "approval": "ASM-EDGE-v2-1",
      "boundContract": "review-digest/1",
      "detail": "digest contract changed"
    }
  ],
  "equivalent": [],
  "invalid": []
}
```

Each affected approval needs a new review before its assessment is `current` again. The tests in `internal/impact` run this example and compare the report with the block above.

## Comparison basis and the diff

Concordia compares a candidate with the subject that the applicable approval bound. It does not compare with the previous commit or the last run. Three commits that touch unrelated files do not matter; a fourth commit that changes a bound route is compared with the digests in the approval, and the trigger names the route.

The compared set is the union of two sides: the dependency items that the approval recorded, and the items that the candidate selects now. Each item has a kind and an identity, and falls into one case:

- **Added.** Only the candidate has the item. With no verified approval the basis is empty, so every current item is added and the assessment is `review-required`.
- **Removed.** Only the approval has the item. A selector that now matches two workloads instead of three leaves the third workload in the set as removed. A deleted binding, a removed mandatory control, and a reference whose target declaration was deleted are removed items too. A missing item is never read as no change.
- **Modified.** Both sides have the item and the digests differ.
- **Unchanged.** Both sides have the item and the digests are equal.
- **Unknown.** The digest of the current item cannot be told, for example because a referenced file is missing. A comparison that needs it cannot run, so the currency is `unknown`.

A removed item stays as a historical subject until a verified approval records a disposition for it: `removed-intentionally`, `retired`, or `moved` with the new identity. A disposition that no verified approval binds does not count, and the removed item still blocks. Rule `REV-DSP-001` gives one obligation for each removed item without a verified disposition. The earlier approval and the removal both stay in the bundle; Concordia never edits a historical record.

## Traversal and its limits

Impact analysis follows dependencies from the changed items to the assumptions, assessments, views, controls, and quality scenarios that rely on them. A file that did not change is still evaluated when one of its dependencies changed, so the result of an incremental run equals the result of a full run. `check --changed` reduces edge findings to the changed files, and it never reduces assessment currency.

The traversal is a breadth-first search from the changed items:

- It keeps a visited set keyed by identity and version and visits each pair once. A cycle ends the walk on that path, is not an error, and is reported as the note `CO-ASM-009`.
- Each trigger gets the shortest path from the changed item to the assessment. Equal-length paths break the tie by sorted identity, so two runs choose the same path.
- Triggers that reach the same assessment through different paths merge into one trigger. Every contributing assumption and every piece of evidence stays listed.

Three limits bound the work: `maxDepth`, `maxVisited`, and `maxSeconds` under `assessments.analysisLimits`. A limit that is not set takes its default (64, 100000, and 120 seconds). If a limit stops the walk, Concordia never truncates silently. It reports `CO-ASM-008` with the assessment and the limit, the affected currency is `unknown` with the reason `limit-exceeded`, and a mandatory gate exits 4. Raising the limit above the closure size gives the real comparison.

If a selector cannot be resolved, so the closure of an assessment is not known, Concordia widens the closure to the full declared scope of the assessment. If that scope cannot be computed either, the currency is `unknown`.

## Incremental evaluation

`check --incremental` reuses the comparison of an earlier run. It reads and writes one file below `.concordia/cache/impact/`, a derived directory that no other command writes and that `init` already ignores. A plain `check` reads and writes nothing there.

The name of the file is the digest of the cache key: the digest contract, the normalization version, the capability profile, the policy digest, the authority digest, the evaluator versions, and the revision of the trust anchor. A change of any member is another file, so the run starts from nothing for everything that depends on it and reports `CO-ASM-010`. A file that is damaged, from another format, or for another key is never used: Concordia evaluates in full, reports `CO-ASM-010`, and replaces the file.

The file holds the digest of each item, the digests that were computed for them, and the comparison of each assessment with its approval. An assessment reuses its stored comparison only when its fingerprint is equal: the digests of the items on both sides, the dispositions, the limits, and the dependency graph. A changed item changes the fingerprint of each assessment that depends on it, so those assessments and their paths are evaluated again and the others are not. A traversal that a limit stopped is never stored.

Expiry, revocation, and the verification of the records always run, with the clock of the run, even when every comparison is a hit. The result of an incremental run equals the result of a full run on the same inputs; only the `analysis` object (`mode`, `cacheHits`, `cacheMisses`) and the note `CO-ASM-010` differ.

## Review triggers

A change to a dependency of an assessment triggers a review in one of eight classes. Each change is also classified as an addition, a removal, or a modification.

| Class | Dependency kinds | Example |
| --- | --- | --- |
| `exposure` | `public-entry-point` | a new public route |
| `boundary-crossing` | `external-destination`, `trust-context`, `data-classification`, `binding` | a new flow to a destination in another zone |
| `identity` | `runtime-identity`, `role-binding` | a changed role binding or service account |
| `sensitive-reference` | `secret-reference`, `configuration-reference`, `storage-reference` | a workload that refers to another secret |
| `protection` | `protocol`, `certificate-reference`, `control` | a changed protocol or certificate reference |
| `environment-hosting` | `placement` | a workload that moves to another region |
| `implementation-verification` | `implementation`, `verification`, `quality-scenario` | changed code or tests named as evidence |
| `removal-capability-loss` | every removal, and a capability loss | a removed control, or a lost provider selector |

A removal always has the class `removal-capability-loss`, whatever the kind of the item. A removed mandatory control is therefore a removal with a visible regression, not a protection change. The `binding` kind is the flow between a source and a target, so it belongs with boundary-crossing flows. A `quality-scenario` is the verification of a quality claim.

Each trigger has the changed dependency (`kind:identity`), the class, the change, the assumptions on the path, the assessment, the path, the evidence, and the action `get review`. A trigger is a reason to ask a person to look again. It never says that the system is vulnerable, and it never says that the system is compliant; a test checks that no trigger text uses either word.

## Possible invalidators

Some changes do not touch a dependency but still change what an approval means. Concordia treats a change to any of these values as a possible invalidator of every approval that was produced under the earlier value:

| Invalidator | Value that changed |
| --- | --- |
| `policy` | the digest of the review policy rules |
| `trust-authority` | the digest of the authority configuration at the trust anchor |
| `evaluator` | the version of an evaluator that supplied evidence |
| `normalization` | the normalization version of the observation provider |
| `digest-contract` | the digest-contract version |

The result is `review-required` with the invalidator named, unless the new value is marked equivalent by shipped, tested evidence. For the digest contract the evidence is the equivalence rule described above, and the detail is `digest contract changed`. For the other values the default is the same: no change is equivalent unless a mark says so. An approval that did not record a value is not compared for that value. An evaluator that an approval did not use does not invalidate it, and an evaluator that an approval used and that is no longer present counts as changed.

A normalization upgrade without an equivalence mark makes every approval bound to the older version `review-required`, and the report lists the affected assessments. An evaluator version change lists the assessments that used that evaluator as possibly invalidated.

## Capability loss

An approval's evidence used some kinds and selector facets of a provider. If the capability profile of the current run no longer supports one of them, the result is a coverage regression:

- A lost kind gives the reason `unsupported-family`. The diagnostic `CO-SEC-002` names the kind.
- A lost selector facet of a supported kind gives the reason `coverage-incomplete`.

In both cases the dependency is `unknown`, never unchanged, and the assessment shows that a review applies. The trigger has the class `removal-capability-loss`. Rule `REV-CAP-001` asks for a recorded disposition of the capability loss.

## A candidate cannot approve its own relaxation

A candidate must not weaken the checks that decide its own approval. Concordia reads the verification inputs from the trust anchor: the authority configuration, the `check.enforcement` entries that select review rules, the required assessments, and the policy digest for each rule. If the anchor is not an ancestor of HEAD or cannot be read, the governance verdict is `unknown` with the reason `trust-anchor-unreadable` and the diagnostic `CO-ASM-007` explains why.

For each rule, enforcement entry, and authority entry, Concordia compares the candidate with the anchor in a conservative partial order. A candidate is stricter or looser only if no axis points the other way:

| Axis | Stricter means |
| --- | --- |
| Severity | `error` over `warning` over `info` |
| `onUnknown` | `fail` over `warn` over `ignore` |
| Minimum coverage | a higher number |
| Minimum confidence | a higher rank: `resolved`, `runtime`, `syntactic`, `heuristic`, `unknown` |
| Scope | a superset of the selectors |
| Enforcement mode | `mandatory` over `advisory` over `shadow` |

Anything else is incomparable. Any change to an authority or a reviewer is incomparable too, because Concordia cannot show that a different list still lets the same people approve. A removed rule is looser. A removed `mandatory` enforcement entry is looser, and any other removed or added entry that is not `mandatory` is treated as a loosening, because an entry can shadow a later one. A different order of the `check.enforcement` entries is incomparable, because the first match wins.

A looser or incomparable change is a relaxation. A stricter change, such as raising a rule from `warning` to `error`, is a possible invalidator but not a relaxation, and needs no disposition.

Each relaxation gives an obligation `REV-GOV-001` with the verdict `violation`, until a record binds the digest of the changed configuration. The record has to come from an authority with the role `governance` that the trust anchor configures, and the caller must have verified it. The candidate's own records, and any authority that the candidate adds, are not accepted. The report asks for a protected governance disposition.

The anchor protects against honest mistakes: an approval that was never merged to the protected branch, or a reviewer list that was edited in the candidate. It does not protect against a hostile candidate. The anchor reference (`assessments.trustAnchor.ref`), the `assessments.trustAnchor` key, and `check.rollback` are read from the workspace file of the run, so a candidate that points the anchor at its own branch, removes the anchor, or adds a rollback is not stopped by Concordia. The review of the workspace file is the control for that. [Evidence and trust](evidence-and-trust.md#the-trust-model) explains why the model stops there.

## Rule IDs

The review-currency pack has four rules:

| Rule | Obligation |
| --- | --- |
| `REV-APR-001` | A required assessment has a current verified approval. |
| `REV-DSP-001` | Each removed or narrowed subject has a recorded disposition. |
| `REV-CAP-001` | A capability loss has a recorded disposition and shows the coverage regression. |
| `REV-GOV-001` | Each relaxation has a governance disposition. |

## Benchmark

The benchmark compares a full and an incremental evaluation of the same assessments on two graphs, and it fails when the two modes give different results. It runs `assessment.Evaluate` in both modes: the full mode reads no cache, the incremental mode reads and writes the [cache](#incremental-evaluation) of its own directory.

The benchmark is this command, run from the repository root:

```sh
go test -run xxx -bench Impact -benchmem -benchtime 10x -count 5 ./internal/impact/
```

The code is in `internal/impact/bench_test.go` and `bench_fixture_test.go`. A test of the same package (`TestIncrementalEqualsFullOnBothGraphs`) asserts the equality of the modes without the benchmark, and `TestBenchmarkDocumentMatchesTheGraphs` compares the sizes below with the graphs.

### Graphs

Every observed edge of a graph is one dependency item of the kind `public-entry-point`. Ten items form one assessment, and ten assessments share one assumption that selects the first item of each of them. Every assessment has a verified approval of the items as the graph shows them. Each iteration of the benchmark moves the digest of one shared item and restores it in the next iteration, so every incremental iteration has one changed item.

| Graph | Entities | Edges | Items | Assessments | Dependency nodes | Closure total | Closure max | Visited max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `commerce` | 115 | 136 | 106 | 11 | 119 | 207 | 20 | 12 |
| `generated` | 3000 | 12000 | 12000 | 1200 | 13320 | 24000 | 20 | 12 |

- `commerce` is the pilot graph: the recording `test/fixtures/cbm-recordings/commerce.json` of the commerce fixture, read through the observation adapter for the native kinds `CALLS`, `DEFINES`, `DEFINES_METHOD`, and `IMPORTS`. Entities and edges are the counts of the frozen CBM graph; the items are the observed edges of those four kinds.
- `generated` is a graph from `internal/testutil/fakegraph.go`: 3000 files that import 4 other files each, read through the same adapter.
- Dependency nodes are the items, the assessments, and the assumptions. The closure of an assessment is the set of nodes that it depends on; visited is the number of nodes that the traversal from the changed item reached in one assessment.

### Results

Median of five runs; the range is in brackets. Durations and memory are for one evaluation of all assessments.

| Graph | Mode | Duration (ns/op) | Memory (B/op) | Allocations | Cache hits | Cache misses |
| --- | --- | --- | --- | --- | --- | --- |
| `commerce` | full | 2.82 ms [2.63 to 2.95] | 1.99 MB [1.98 to 2.00] | 16,089 | none | none |
| `commerce` | incremental | 2.47 ms [2.36 to 2.55] | 1.90 MB [1.87 to 1.93] | 6,264 | 1 | 10 |
| `generated` | full | 285 ms [281 to 330] | 195.7 MB [195.6 to 195.7] | 1,740,174 | none | none |
| `generated` | incremental | 220 ms [209 to 223] | 191.9 MB [181.3 to 194.5] | 529,642 | 1,190 | 10 |

The incremental mode is 12 percent faster on `commerce` and 23 percent faster on `generated`, and it makes 61 and 70 percent fewer allocations. The gain is bounded because most of an evaluation is work that no mode skips: loading the documents, verifying the records, asking the fact source, and comparing the recorded values of every approval. The cache replaces the digests of items that did not change, the digest of the subject that an approval lists, and the comparison and traversal of the assessments that do not depend on the changed item. On `generated`, one changed item makes 10 of 1200 assessments miss, which are the assessments that share it.

### Machine

| Item | Value |
| --- | --- |
| Date | 2026-10-30 |
| `uname -a` | `Linux w04 6.18.54.1-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Tue Oct  6 19:13:07 UTC 2026 x86_64 GNU/Linux` |
| CPU (`/proc/cpuinfo`) | AMD Ryzen 9 9950X3D 16-Core Processor, 32 logical CPUs |
| Go | `go version go1.27.3 linux/amd64` |

### Tolerance

A fresh run on the same machine matches the table within these limits: durations within 50 percent of the median, memory within 5 percent, allocations within 2 percent, and every size in the graph table exactly. The durations move more than the other numbers, because the workstation runs other work, and the `commerce` evaluations last only a few milliseconds. Other machines differ in the durations; the sizes and the cache counters stay the same.

### Default limits

The defaults of `assessments.analysisLimits` stay 64 for `maxDepth`, 100000 for `maxVisited`, and 120 for `maxSeconds`. In the largest graph the longest dependency path has three nodes (item, assumption, assessment), the largest traversal visits 12 of 13,320 nodes, and a full evaluation of all 1200 assessments takes 0.29 seconds. The defaults leave a wide margin for these graphs and still stop a walk that runs away. Graphs with views, controls, and quality scenarios as consumers have longer paths: raise `maxDepth` only when `CO-ASM-008` names it.

## Related

- [Evidence and trust](evidence-and-trust.md)
- [How Concordia works](how-concordia-works.md)
- [Verdicts and coverage](verdicts-and-coverage.md)
- [Fingerprints, baselines, and waivers](fingerprints-baselines-waivers.md)
- [Assessments reference](../reference/assessments.md)
- [Review and approve assessments](../how-to/review-and-approve-assessments.md)
