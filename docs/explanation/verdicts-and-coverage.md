# Verdicts and coverage

A check does not just say pass or fail. Each rule gets a verdict, and the difference between "no problem found" and "could not tell" is central to how Concordia is designed. This page explains the reasoning. For the exact fields, see the [results reference](../reference/results.md).

## Verdicts

A rule ends as one of `error`, `violation`, `unknown`, `pass`, or `not-applicable`. The run summary takes the worst of them, in that order. The exit code follows from the verdicts and the finding dispositions: an internal error gives 7, a failing finding at or above `check.failOn` gives 1, and an `unknown` rule gives 4 when `check.unknown` is `fail`.

## The open-world model

Concordia assumes that an architecture model is incomplete. If the code has an edge between two nodes and the model declares no relationship between them, that alone is not a problem: the model may just not mention it yet. An undeclared pair becomes a violation only when you say so, with a `declaredOnly` rule. Other rules, such as `forbid` or `layerOrder`, judge only the edges they name. This keeps adoption cheap: you add rules as you decide which constraints matter.

## Why `unknown` fails by default

A rule that cannot be judged is not a passing rule. If the evidence is missing or too weak, calling it "pass" would let broken architecture through quietly. So `unknown` fails the run (exit 4) by default, using `check.unknown: fail`. You can relax it to `warn` or `ignore` for a rule set you are still tuning, but that is a visible, deliberate choice. The reasons for `unknown` and how to clear them are in [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md).

## Why `unknown` never becomes `pass`

Four rules keep `unknown` from turning into a pass, whatever the pressure to make a run green.

- **The worst result wins.** An obligation, a rule, an area, and the summary each take the worst result below them, and `unknown` is worse than `pass` at every level. One `pass` next to one `unknown` is `unknown`.
- **Policy changes the exit code, not the verdict.** Setting `check.unknown` to `warn` or `ignore` changes whether the run fails. The verdict stays `unknown` in `results.json`, so the gap is still visible.
- **A waiver changes the disposition, not the verdict.** A waived `unknown` keeps the verdict `unknown`, and it keeps exit code 4 when the rule says `onUnknown: fail`. Only evidence resolves an unknown.
- **Evidence that fails admission is never counted.** A statement from the wrong revision, a stale measurement, a failed signature, or a partial render is listed with its reasons and supports nothing. The obligation stays `unknown` until admissible evidence arrives.

## Coverage and excluded files

Coverage is the share of source files that the bindings account for. Files you exclude from analysis still count in the total. If excluded code vanished from the denominator, you could raise coverage just by excluding the hard parts. Keeping it in the total means "files: 6/7 covered" states plainly that one file is not being checked. When coverage falls below `check.minimumCoverage` (default `0.95`), the coverage rule is `unknown` with `CO-CON-021`, and excluded code is never reported as passing.

## Coverage for each predicate

File coverage says how many files the graph holds. It does not say whether the provider extracted every kind of fact from them. Concordia therefore also records coverage for each predicate (a relationship family such as `uses-image` or `policy-selects-workload`) in each scope: a source project, or the sources of a declared environment. A record has a status (`complete`, `partial`, `unsupported`, `failed`, or `unknown`), the number of unresolved items, and the loss modes that apply. The same provider can be complete for one predicate and partial for another in the same scope.

A loss mode is a known way in which the provider loses evidence. Some are stated by the capability profile: the provider keeps one image edge for each workload and image repository, so a second container of the same image repository is lost. Some Concordia detects from the index result of the provider (a directory that the provider skips by default, a raster image, a file that it parsed in part). Three loss modes have no signal in the graph: a lost container, a reference to a target that no manifest declares, and a selector that is empty or uses an expression. For plain Kubernetes manifests, Concordia reads the same committed YAML and compares a short list of structural fields with the facts, never a value. The result is a gap in coverage, not a fact and not a violation.

The rule that follows is the absence rule. A missing necessary item is a violation only if the provider supports the observation, the item is in scope, the analysis succeeded, and the coverage of the predicate is `complete`. Otherwise the result is `unknown` with the coverage reason. Silence from a provider that is known to be lossy is not evidence of absence. The full list of loss modes is in the [results reference](../reference/results.md#loss-modes).

## Nothing is skipped or cut silently

Each rule leaves out some candidate edges: edges with an endpoint that has no binding, a node that the query did not return, a file outside the scope, or a kind that nobody maps. Concordia counts each of these with a reason, and the edge stays in the evidence. The same holds for limits. The workspace can set [limits](../reference/workspace.md#limits) for the graph, the evidence, and the traversals. A run that goes above a limit reports the limit, the configured value, and the observed value, and the rules that depend on it are `unknown`. A traversal that stops at its limit is incomplete and can never give `pass`. No node, edge, fact, or finding is dropped to stay under a limit.

## One failed project blocks only what depends on it

If the query for one project fails, Concordia marks the coverage of that project `failed` and goes on with the others. A rule that depends on the failed project is `unknown`. A rule that depends on other projects keeps its result, and a mandatory rule that is `unknown` still blocks the run with exit code 4. The failure shows in the coverage section even if no rule uses the project. A failure of the whole connection to the provider is different: no evidence can be trusted, so the run stops.

## Sub-threshold evidence

An observed edge below the minimum confidence is not used to produce a violation. Think of a call that the indexer only guessed at by name: accusing code on that basis would produce false alarms that teach people to ignore the tool. Instead, the rule reports `unknown` with `CO-CON-023`, counting the edges it set aside. You decide whether to accept weaker evidence or leave the rule uncertain.

## Typed obligations: one result for each occurrence

A rule with an `obligation` block (a typed rule) is not judged by pair of nodes. Concordia builds one obligation for each subject and each environment of the rule from the architecture, the rule scope, and the declared environments. It does this before it reads the graph, so an empty or partial graph never removes an obligation. It then evaluates each obligation against each evidence occurrence: each container, each environment, each fact. A pair of nodes can have an allowed HTTPS connection and a forbidden administrative connection. They are two results, and the `reconciliation` pair report never decides between them.

The results of an obligation add up in the order `error`, `violation`, `unknown`, `pass`. A service with one `pass`, one `unknown`, and one `violation` is a `violation`, and all three results stay in the output. If every result is `not-applicable`, the aggregate is `not-applicable`. A rule is `not-applicable` only when Concordia has shown that it does not apply. A scope that cannot be resolved, such as an environment that the workspace does not declare, is `unknown`.

Several workloads of one service do not vouch for each other. With multiplicity `forAll`, one replica that has the required property does not satisfy the other two. With multiplicity `exists`, one is enough. A binding can state `agreement: any`, which lets one bound workload that has the property satisfy the obligation. The default agreement is `all`.

Only rules with the enforcement `mandatory` block the run. Typed rules are `advisory` unless the rule says otherwise: their verdicts and findings are kept and shown, and they do not change the exit code. A valid waiver changes the disposition of a violation to `waived`. It does not change the verdict to `pass`.

## Areas keep verdicts apart

A run has more than one question to answer, and one verdict for all of them would let a good answer hide a bad one. `results.json` therefore reports six areas, each with its own verdict and counts: `conformance` (the edge rules and the typed rules of the infrastructure and assurance families), `deploymentTrace` (how far each service can be followed toward a cluster), `adrGovernance` (decisions), `diagramCurrency` (declared views), `assessmentCurrency` (reviews), and `qualityEvaluation` (quality scenarios).

A rule reports in the area of its pack: the views pack in `diagramCurrency`, the `adr-governance` pack in `adrGovernance`, the `review-currency` pack in `assessmentCurrency`, and the rules that quality scenarios make in `qualityEvaluation`. An area that the workspace does not use is `not-configured`, which is never `pass`. The summary verdict is the worst verdict of the configured areas, so a mandatory area that is `unknown` makes the summary `unknown` while `conformance` passes. The `deploymentTrace` area comes from the trace and not from rules, and it never changes the exit code.

## The reasons for `unknown`

Every `unknown` obligation has exactly one reason from a closed set of nineteen. `results.json` shows it in the `reason` field of the obligation and in the `reasons` of the rule. A closed set matters because a reason that can mean anything cannot guide a fix: each reason has one meaning, and no other reason stands for it. The reasons form four families. To clear one, see [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md).

### Coverage and provider reasons

These say that the provider did not or could not show the whole picture.

| Reason | Meaning | Why it is not a pass |
|---|---|---|
| `coverage-incomplete` | A predicate that the claim needs is not `complete` in the scope: a loss mode applies, the scope failed, or a needed evidence role or resolution is missing. The detail names the predicate, the scope, and the loss mode or the missing role. | Silence from a provider that is known to lose evidence is not evidence of absence. |
| `unsupported-family` | The capability profile of the provider does not support the evidence family. | A provider that cannot see a family cannot show that the family is clean. |
| `capability-regression` | The lock profile supported a family that the profile of this run does not. | A rule that passed before must not pass now because evidence disappeared. |
| `limit-exceeded` | The run went above a configured limit. The detail names the limit, the configured value, and the observed value. | A traversal that stopped at its limit is incomplete. |

### Identity, binding, and selection reasons

These say that Concordia cannot tie a fact to the thing a rule is about.

| Reason | Meaning | Why it is not a pass |
|---|---|---|
| `environment-unresolved` | The environment of the fact or of the obligation is not resolved: the fact has no attribution, or the rule scope names an environment that is not declared. | A fact with no environment cannot vouch for one. |
| `ambiguous-binding` | Bindings that are not exclusive map one entity to different CALM elements. | Choosing by order of declaration would make the verdict depend on the order of a file. |
| `binding-gap` | An endpoint of a fact has no binding. The fact stays in the evidence and a finding shows its location with the next action `resolve binding`. | An unbound fact is judged by nothing. |
| `evidence-conflict` | Facts of the same role disagree about a property of one subject, two manifests of one environment declare the same resource (`CO-OBS-021`), or admissible measurements of one window disagree. The detail names the sources, never a value, and no source wins. | Any winner would be a guess. |
| `selector-property-unknown` | A selector needs a property that the fact or entity does not have. `not` of an unknown stays unknown, and a dimension with no data source (`trustContext`, `dataClassification`, `governanceScope`) is always unknown. | Negating a missing property would turn a gap into a match. |
| `target-not-declared` | A committed reference names a target (for example a Secret) that the provider did not declare, so no edge exists. | A missing target is never an empty match. |

### Review currency reasons

These say that Concordia cannot tell whether a review still applies. The currency of an assessment is `current`, `review-required`, `expired`, `revoked`, or `unknown`, and only an `unknown` currency carries a reason from the closed set. A change in the system gives `review-required`, which is a different thing: it is a decision that a person has to make, not a gap in evidence. See [Review currency](review-currency.md).

| Reason | Meaning | Why it is not a pass |
|---|---|---|
| `trust-anchor-unreadable` | The trust anchor is missing, shallow, or not an ancestor of `HEAD`, so review configuration and approvals cannot be verified (`CO-ASM-007`). | Without the anchor, an approval cannot be told from a self-approval. |
| `verification-unavailable` | Signature verification cannot run, for example a signed approval checked with `--offline` (`CO-ASM-011`). | An approval that cannot be verified is not an approval. |
| `reference-unresolved` | An implementation or verification path that an assessment names does not exist at the revision. | The digest of a missing file would look like no change. |

### Assurance evidence reasons

These say that the evidence that a rule needs was not supplied, or was supplied and not admitted. Admissibility details such as `different-revision` or `stale` are not reasons. They are listed as detail of the one reason, so the reason stays stable while the detail explains. See [Evidence and trust](evidence-and-trust.md).

| Reason | Meaning | Why it is not a pass |
|---|---|---|
| `claim-not-enabled` | A rule needs a claim that no loaded contract enables: the claim is not in the claim registry, or its contract is not loaded. | No adapter has said that it can establish the claim. |
| `no-evidence-supplied` | No statement or result was supplied for the check, or every one that was supplied is inadmissible. | Evidence that fails admission is shown, and never counted. |
| `no-evaluator-available` | An effective-policy claim has no evaluator for the environment: `assurance.evaluators` does not enable one, or a needed construct is unsupported. | Selection edges are not isolation. |
| `rendered-evidence-missing` | The scope has no complete rendered-level evidence: no `assurance.rendering` source, or the render is `partial` or `failed`. | A partial resource set can hide the one resource that breaks the rule. |
| `enforcement-not-declared` | An evaluator result needs an enforcement assumption (for example `network-policy-enforced`) that `assurance.evaluators[].assumes` does not declare. | A policy that nothing enforces isolates nothing. |
| `reviewed-mode-not-available` | A `reviewed` quality scenario is evaluated in a workspace without an `assessments` key, so no review currency exists. | With no review currency, no review can be current. |

Evidence roles are not interchangeable. A rule uses only the roles it permits. If a rule permits only `runtime-observation` and the graph has only a parsed source fact, the obligation is `unknown` and the detail names the missing role. A difference between declared and observed state is drift. It is never a pass.

## Reconciliation classes as drift

Besides rule verdicts, each pair of nodes is reconciled between model and code:

|Class|Meaning|
|---|---|
|`conforming`|declared and observed agree|
|`undeclared`|observed, but not declared|
|`unobserved`|declared, but no edge observed|
|`contradiction`|observed in a way the model forbids|
|`unknown`|not enough evidence to classify|

Read these as drift indicators. They show where the model and code have moved apart, even where no rule is broken, and they feed the status tags in [diagrams](../how-to/render-diagrams.md).

## Related

- [How Concordia works](how-concordia-works.md)
- [Evidence and trust](evidence-and-trust.md)
- [Review currency](review-currency.md)
- [Fingerprints, baselines, and waivers](fingerprints-baselines-waivers.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
- [Results reference](../reference/results.md)
