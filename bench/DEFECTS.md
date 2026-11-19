# Defects found by concordia-bench

This file records the Concordia defects, provider defects and docs gaps that concordia-bench found, and the benchmark defects that still need a fix. An `xfail` in a task spec starts with the ID of its entry here, for example `D5: …`. Remove the `xfail` and the workaround in the same change that fixes the defect.

The agent baseline triage is from job `agent-all-20261114T103145` (terminus-2, `openrouter/deepseek/deepseek-v4.1-flash-1017`, 162 trials). See [README.md](README.md#triage-of-agent-trials-below-10) for the triage causes.

## Concordia and provider defects

### D1. CBM Route nodes are deployed labels that can never bind (design issue)

- `Route` is in `deployedLabels` (`internal/conformance/bindinggap.go:19-21`). A CALLS edge from a bound subject to a CBM `__route__…` node therefore gives `unknown` / `binding-gap` (`evaluate.go:521-530`, CO-BND-005).
- `docs/reference/bindings.md:63` says: "A `Route` node of the provider has no environment and is never matched." The gap can never be resolved.
- Effect: every `acyclic`, `denyPath` or `budget` rule with `CALLS` over Go code that registers HTTP handlers is permanently `unknown`.
- Seen in argo-cd (`ARGO-CYC-001`) and volcano (`VC-DNY-001`, `VC-BUD-001`). The bench asserts the honest unknown with `evidence.onUnknown: warn`.
- Decision needed: exempt Route endpoints from code-rule binding gaps, or make them bindable.

### D2. CBM 0.12.1 resolves Rust `use` imports by bare symbol name (provider defect)

- A `use crate_x::Value;` (or `Any`, `Path`, `Debug`, `get_columns`) binds to a same-named definition in an unrelated crate.
- nushell: 107 false-positive `NU-LYR-001` findings at HEAD, baselined at adopt. A pre-existing false cycle hides any new `CORE-CYC-001` cycle.
- Bench workaround: mutations import symbols with workspace-unique names.

### D3. VIEW-FRESH-001 reports `view-stale` instead of `unknown` when the Structurizr CLI is not the pinned version (Concordia defect)

- With `STRUCTURIZR_CLI` pointing at a CLI that reports another version (`render --views` refuses it with exit 5, `CO-DGM-004`), `concordia check` gives `VIEW-FRESH-001` `violation` findings (`view-stale`) on the Mermaid and JSON exports.
- The docs say that an export the generator cannot regenerate is `unknown` ("generator cannot run"), never a pass or a stale verdict.
- `xfail`: fzf-views check `wrong-pin-is-unknown` keeps the documented assertion.

### D4. CBM 0.12.1 resolves external Java imports to same-named in-scope methods (provider defect)

- An import such as `com.fasterxml.jackson.annotation.*`, `io.strimzi.crdgenerator.annotations.*` or a `…logging` package whose last segment matches a method name resolves to an unrelated in-scope method.
- strimzi: 736 false-positive `STZ-API-001`/`STZ-LYR-001` findings at HEAD, baselined at adopt, plus four false `STZ-IND-001` operator pairs and a false cycle across all seven modules.
- Bench workarounds: `STZ-BUD-001` max set to the HEAD count (11), `STZ-CYC-001` scoped to four modules, `STZ-IND-001` detection on the only clean pair (topic→user).

### D5. Typed `forbidden` rules pass on missing pod-template evidence while coverage claims complete (Concordia and profile defect)

- CBM 0.12.1 emits no USAGE edge for the `serviceAccountName` or the secret volumes of a Deployment pod template, yet the capability profile reports `uses-runtime-identity` and `references-secret` coverage as complete.
- Effect: forbidden obligations (knative `KN-SEC-001` shared controller identity, `KN-SEC-002` webhook-certs mount) report `pass` instead of a violation or `unknown`. A pass is claimed without evidence.
- Also: a forbidden `references-secret` rule whose target `infraResource` binding matches nothing returns `pass`, not `unknown`/`binding-gap`. It could not be separated from the above.
- `xfail`: knative-serving-deploy-security check `head-autoscaler-shares-identity`.
- Pending use task: the probes `controller-mounts-certs` (the controller mounts the webhook certs) and `secret-binding-typo` (a target binding that matches nothing) replace files the agent writes, so they belong in a `knative-serving-deploy-security-gate` use task. While D5 hides the violation, no review of those branches can expect a finding, so the gate would test nothing and is not built. The two mutation directories stay; when D5 is fixed, build the gate (branches for both mutations plus `controller-imports-apis` as the clean branch) and add it to `tasks.json`.
- The two knative-serving-deploy-security agents spent most of their steps chasing this behaviour.

### D6. Indexing a changed gitea tree leaves a state that no cleanup restores (CBM or Concordia defect, cause not isolated)

- Measured in the gitea adopted image, each run `concordia index && concordia check --no-color`:

  | Run | Exit |
  |---|---|
  | untouched HEAD, the image's index | 0 |
  | untouched HEAD, fresh full index in a new container (CBM store deleted), twice | 0 |
  | empty `package mailer` file created and deleted, no index, no git | 0 |
  | empty file indexed, deleted, re-indexed, no git at all | 1 |
  | empty file committed, indexed, `git reset --hard`, re-indexed | 1 |
  | then: CBM store deleted / CBM daemon restarted / working tree deleted and re-checked out / git history pruned / `~/.cache/concordia` deleted / both caches deleted | 1 each |

- The extra finding is one active error: a heuristic `suffix_match` CALLS edge `services/mailer/mail_workflow_run.go → routers.web.repo.actions.ViewJob.Duration`.
- Trigger: indexing a tree that differs from HEAD; git is not involved. Only a container that never indexed a change is clean.
- `xfail`: the gitea-adopt and gitea-structure allowed-mutation checks keep `expectExit: 0`; scored copies assert that no active finding lands in the mutation file.
- Agent-run risk: an agent that indexes a scratch Go change in gitea can make the gitea HEAD checks fail through no fault of its own. Not observed in the baseline.

### D7. The assurance evaluators follow only `workload` and `image` bindings; the docs do not say so (docs gap)

- `internal/conformance/assurance_access_input.go` (`matchAll`) resolves the `from`/`to` elements of `require.effectiveAccess` only through `workload` and `image` binding matches. An element bound with `infraResource` (for example `core/Secret/<ns>/<name>`) matches no rendered resource, so the rule is `unknown` / `binding-gap`.
- `docs/reference/assurance.md` says only "each rendered resource that the `to` element is bound to", and `bindings.md` documents `infraResource` as a deployed-resource binding.
- Hit in argo-cd (argocd-secret), knative-serving (webhook-certs) and strimzi (cluster-operator ConfigMap). Bench workaround: bind the resource as `workload` with `resourceKind: Secret|ConfigMap`.
- Decision needed: support `infraResource` in the evaluator input, or document the restriction in `assurance.md` and `bindings.md`.

### D8. `concordia bind validate` never matches deployed-resource bindings (Concordia defect)

- `bindValidate` (`internal/cli/bind.go:339`) calls `prepareBind(ctx, app, opts, nil)` with no strategies, so `strategyNodes` never runs `withDeployedNodes` (`internal/cli/bind_collect.go:153`), and the deployed resources are not in the node set. `check` attributes them to environments, so the same bindings resolve there.
- Effect: a `workload`, `infraResource` or namespace binding that matches only deployed resources gives `CO-BND-004 required binding matched zero entities` and exit 2 in `bind validate`, while `check` resolves it. Reproduced on the airflow oracle workspace with `required: false` removed from two bindings: `bind validate` exits 2; `check` exits 0 and resolves the binding.
- The docs promise the opposite: `docs/how-to/bind-code-to-architecture.md:127` says to run `concordia bind validate` to check deployed-resource bindings.
- Baseline cost: airflow-deploy-security ×2 (0.889) and knative-serving-deploy-trace ×2 (0.909). The agents' bindings were correct; `bind validate` rejected them.
- The bench hides the defect: all 18 oracle binding files with deployed-only bindings set `required: false`, and the volcano tickets tell the agent to. When D8 is fixed, remove that workaround from the oracles and tickets so the `bound-nodes` checks test `bind validate` for real. Do not add the workaround to more tickets.
- Fix: make `bind validate` merge the deployed nodes (and the route nodes) the way `check` does, and add a test that a `workload` binding of a plain manifest validates.

### D9. Bindings that match only rendered resources need `required: false`, and nothing says so (docs and usability gap)

- airflow-assurance `ATVskLJ` (0.839): workloads that exist only in unrendered Helm templates count toward nothing, so `check` itself stops with `CO-BND-004`. The only hint is "if the code does not exist yet, set `required: false`" (`how-to/bind-code-to-architecture.md:67`), and the CO-BND-004 text has no pointer. The agent diagnosed the cause, then invented a stub manifest instead.
- Fix: state in `bindings.md` and the how-to which entities count toward `required`, and make the CO-BND-004 hint name `required: false` for rendered resources. Decide whether rendered workloads should count.

### D10. `infraResource` addresses: `_` for cluster-scoped kinds is undocumented, and a malformed address is accepted silently (docs gap and usability defect)

- volcano-deploy-security `vuPMvrR`: the agent wrote ClusterRoleBinding addresses as `rbac.authorization.k8s.io/ClusterRoleBinding//<name>`. Concordia matches the address as an exact string; `ParseObjKey` rejects the empty namespace part, so the binding can never match, and with `required: false` nothing reports it.
- `bindings.md:64` never says that `_` stands for a cluster-scoped kind, or `?` for an undeclared namespace; only the example `core/Namespace/_/payments` hints at it. The agent copied `//` from the `CO-OBS-021` message, which prints the scoped identity `k8s:~/~/rbac.authorization.k8s.io/ClusterRoleBinding//<name>`.
- Fix: document `_` and `?` in `bindings.md` and `how-to/trace-a-deployment.md:67`; reject or warn on an `infraResource` address that fails `ParseObjKey` at load time (`internal/binding/load_match.go`); make the CO-OBS-021 message print the binding form of the address.

### D11. Smaller docs and message gaps

- `docs/reference/policies.md:3` says "five predicates"; the page lists 14. The `require.graphProperty: acyclic` section has no pointer to `budget` with `measure: cycles` (one counted finding), and `how-to/write-policies.md` does not cover `budget`. fzf-adopt ×2 chose `acyclic` where the ticket described a counted budget: an agent mistake that the pointer would help prevent.
- `bindings.md:68`: an environment-scoped binding never matches the provider `Image` node, so a `from-image` rule (`built-from`, `base-image`) needs an `image` binding without `environment`. This is not stated where `from-image` rules are documented, and the `unknown` reason then is `build-link-needs-build-command`, which is misleading when a Compose `build:` exists (hoppscotch-containers `vQ7rdqH`, agent mistake).
- `how-to/resolve-unknown-verdicts.md:45` sends `coverage-incomplete` to fixing the loss mode; for a family of partial maturity the lever is `evidence.passCompleteness`.
- `concordia trace` run after a workspace change but before `check` reads the stale `results.json` and reports "no environment is declared".
- `bind validate` runs about 28 s on large repositories with no progress output; two agents interrupted it and never saw its error.

### D12. Binding-gap fingerprints change whenever the file tree changes (Concordia defect)

- `internal/conformance/bindinggap.go:153` sets the `Occurrence` of a binding-gap finding's fingerprint to `endpointID`, the provider-assigned observed node ID (for example `cbm:argo-cd:24176`). The provider renumbers nodes when files are added, so an unchanged binding gap gets a new fingerprint on every branch.
- The docs define the occurrence as a container name or `stage:<index>` (`docs/reference/results.md:133`), and fingerprints exist to identify the same finding across runs: baselines, waivers and "new on this branch" comparisons depend on it.
- Effect: on argo-cd `main`, 16 active D1 binding-gap findings (`ARGO-API-001`, `ARGO-IND-001` on `__route__` nodes) reappear with new fingerprints on every branch, so a reviewer who compares fingerprints with `main` sees about 16 false new findings per branch, including on a change that should pass.
- Fix: build the occurrence from stable identity (the endpoint's label and qualified name), with the fingerprint version-tag change and golden-vector update that `docs/explanation/fingerprints-baselines-waivers.md` requires.
- Bench effect: none scored. Use-task reviews leave out informational findings with an `unknown` result, which is what every binding-gap finding is, so no check carries a D12 `xfail`.

### D13. Finding fingerprints change after a re-index of a changed tree, so baselined findings come back active (suspected Concordia or CBM defect, not isolated)

- Reported while building gitea-governance-gate (governance stage, `indexMode: full`): after checking out a branch and running `concordia index && concordia check`, 629 findings were active; checking `main` again afterwards also gave 629, and all 1,721 active fingerprints differed from the first `main` run. The baseline matches by fingerprint, so its entries stopped matching.
- Related to D6 (state left by indexing a changed tree) and D12 (an unstable `Occurrence`), but wider: it hits ordinary code findings, not only binding gaps.
- Bench effect: plain `check` cannot review a gitea branch on that stage; gitea-governance-gate tells the reviewer to gate with `--changed main`, which scopes the noise away.
- To do: reproduce in the gitea governance image, diff the fingerprint inputs of one finding across the two `main` runs, and name the unstable input.

### D14. `check --changed` turns a rule `unknown` (exit 4) when it filters out the rule's baselined violations and its coverage is below the minimum (Concordia behaviour to confirm against the docs)

- In `internal/conformance/changed.go` (`reduceToChanged`), a rule whose only violations are baselined and outside the change becomes `unknown` when its coverage is below `minimumCoverage`. On `main` the same shortfall (gitea, `indexMode: full`, 0.873 < 0.9) is hidden because the rule's verdict is `violation`.
- Effect: on the gitea governance stage, any branch that changes code outside already-baselined files exits 4 under `--changed main`, although the change adds no finding. gitea-governance-gate's clean branch therefore touches only files with baselined findings.
- Decide: whether a rule with baselined violations and low coverage should be `unknown` on `main` too (consistency), or whether `--changed` should not surface the shortfall for unchanged scope.

## Benchmark defects fixed after the baseline

- B1. `rules_declared` read rules only from `policies.files`, so a pack listed in `policies.packs` with `use: <path>` and `sha256` was reported as not declared (airflow-assurance `7h97Xx9`). Fixed in `35bad7f`.
- B2. knative-serving-deploy-security asserted KN-REL-001 results that depend on evidence settings the ticket never stated. The ticket now states the intended result.
- B3. volcano-deploy-security asserted VC-SEC-002 `unknown` in `dev`, which depends on default evidence settings the ticket never stated (`2tMmWgk`). The ticket now states the intended result.
- B4. The volcano-deploy-security ticket named one file as the environment source while the spec required the folder (`vuPMvrR`). The ticket now names the folder.
- B5. volcano-review and browser-use-review required `mechanism: protected-review`, the documented default (`BwXmUEK`). The specs accept the key absent.
- B6. The verifier did not save every file an agent may author, so trials could not be replayed for triage. Fixed in `f79d302`.
- B7. 52 configure-task mutations replaced a configuration or manifest file that the agent writes in that task with the reference copy, so their checks graded the reference configuration instead of the agent's (airflow-assurance `ATVskLJ`). Fixed by splitting the tasks into configure tasks and use tasks (`bench/README.md`, "Task types"): each change became an engineer change in the configure task (a new or changed file the agent does not write), a condition the configure ticket asks for and the check asserts on HEAD, or a teammate branch in a `<stage>-gate` use task built on the reference configuration. `check_tasks.py` rejects a configure-task mutation that replaces an agent-written file.

## Behaviours no task tests yet

Two configure-task probes changed the architect's configuration only to show an informational `unknown`. They cannot stay in a configure task (they replaced an agent-written file), and a use-task review leaves informational unknowns out, so the split dropped them. Each needs a configure-task HEAD condition that the ticket asks for:

- strimzi: an `image` binding that several nodes share (`CO-BND-009`, `ambiguous-binding` unknowns), formerly `image-binding-ambiguous`.
- volcano: an assurance rendering source that points at the upstream chart directly (`ASR-REN-001` unknown), formerly `assurance-render-upstream-chart`.

## Baseline triage summary

| Trial | Reward | Cause | Entry |
|---|---|---|---|
| airflow-deploy-security `2dvqxgk`, `dMMfUS5` | 0.889 | Concordia defect | D8 |
| knative-serving-deploy-trace `FSjsMMr`, `m6NpVeV` | 0.909 | Concordia defect | D8 |
| airflow-assurance `ATVskLJ` | 0.839 | Docs or usability gap and benchmark defect | D9, B7 |
| volcano-deploy-security `vuPMvrR` | 0.765 | Docs or usability gap and benchmark defect | D10, B4 |
| airflow-assurance `7h97Xx9` | 0.935 | Benchmark defect | B1 |
| knative-serving-deploy-security `Z9ZLcVC`, `gU4H8da` | 0.846 | Benchmark defect | B2 |
| volcano-deploy-security `2tMmWgk` | 0.882 | Benchmark defect | B3 |
| volcano-review `BwXmUEK` | 0.923 | Benchmark defect | B5 |
| fzf-adopt `FkpGZtH`, `zyKJUQd` | 0.783 | Agent mistake | (D11 pointer) |
| hoppscotch-containers `vQ7rdqH` | 0.864 | Agent mistake | (D11 pointer) |

Fixed earlier, in `e223d2c` and `9b42757`: ten trials that encoded a rule with an equivalent predicate, and the nushell-quality and gitea-gate tickets.
