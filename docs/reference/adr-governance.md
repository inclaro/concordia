# ADR governance reference

ADR governance checks that the architecture decisions of a repository are committed, that they say what they govern, and that the rules which enforce them exist. Decision facts (title, status, supersession) come from the code graph provider: the `ADR` nodes of the indexed decision files. What a decision governs and which rules enforce it come only from a committed **decision register**. ADR text is data: Concordia does not run, fetch, or follow anything in it.

ADR governance is off until the workspace has an [`adr`](workspace.md#adr) key that lists a register file. The decision rules are in the built-in pack `builtin:adr-governance`; add it under `policies.packs` to evaluate them.

## Decision register

A register is a YAML file with `kind: DecisionRegister`. The schema is `internal/adr/schemas/decision-register.schema.json` (`https://concordia.io/schemas/v1alpha1/decision-register.schema.json`). Every object is closed: an unknown key is `CO-ADR-001`.

```yaml
apiVersion: concordia.io/v1alpha1
kind: DecisionRegister
decisions:
  - id: ADR-0003
    path: docs/adr/0003-orders-own-db.md
    expect: active
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
```

| Key | Type | Default | Description |
|---|---|---|---|
| `apiVersion` | `concordia.io/v1alpha1` | required | Schema version. |
| `kind` | `DecisionRegister` | required | Document kind. |
| `decisions` | list | required | The registered decisions. |

### `decisions[]`

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Decision ID of the form `ADR-<number>`: the prefix in any case, the number with or without leading zeros. `ADR-0003`, `adr-003`, and `ADR-3` are the same decision, and results always show it in canonical form (`ADR-3`), the form of the provider `adr_id`. Any other form (`ADR 3`, `ADR_3`, `ADR3`, `3`) is `CO-ADR-001`, so a misspelled ID never leaves a decision silently absent. |
| `path` | string | none | Repository-relative path of the decision file. Pins the entry to one file. |
| `project` | string | none | Project name, for a workspace with several projects. |
| `expect` | `active`, `historical` | `active` | The lifecycle that the decision is expected to have. |
| `categories` | string[] | `[]` | Categories that the decision covers. A [trigger](#triggers) with the same category is satisfied by a change to this decision. |
| `governs` | list | required, at least one | What the decision governs. See [governs](#governs). |
| `enforcement` | `required`, `none` | `none` | Whether the decision must have an enforcing rule. |
| `enforcedBy` | list | `[]` | Rules that enforce the decision. See [enforcedBy](#enforcedby). |
| `released` | list | `[]` | Subjects and rules that the decision gives up. Only with `expect: historical`. See [released](#released). |

The pair of canonical ID and project is unique. A duplicate in one file, or across files, is `CO-ADR-002`; the message names both locations.

### governs

Each item has exactly one of `ref` and `select`.

| Form | Meaning |
|---|---|
| `ref: calm:node:<id>` | A CALM node. |
| `ref: calm:relationship:<id>` | A CALM relationship. |
| `ref: calm:interface:<node-id>/<interface-id>` | An interface that a CALM node declares. |
| `select: {calmNodes, nodeTypes, tags}` | The nodes that a selector chooses. Each key is a list; the result is the union of the three, as in the [policy selectors](policies.md). A `select` expands to `calm:node:<id>` subjects, sorted. |

A `ref` to an ID that the architecture does not declare, and a `select` that matches no node, are `CO-ADR-003`. The message names the unknown ID or the empty selector.

### enforcedBy

| Key | Type | Description |
|---|---|---|
| `rule` | string | Rule ID. |
| `pack.id` | string | Pack ID. |
| `pack.version` | string | Pack version. Rules have no version of their own: the version of a rule is the version of its pack. |

A link is valid when the pack with that ID and version is loaded and declares the rule. An unknown rule, pack, or version is `CO-ADR-004`; for a version mismatch the message names the loaded version. Links are checked against every rule that the packs declare, so a `check --rule <id>` filter does not make a link invalid. In findings and in `released`, a rule link is written `<rule-id>@<pack-id>/<pack-version>`, for example `CORE-DB-001@core/1.0.0`.

### released

A historical decision may give up subjects and rules explicitly. Each item has `reason` and exactly one of `ref` and `rule`:

| Key | Type | Description |
|---|---|---|
| `ref` | string | A subject: `calm:node:<id>`, `calm:relationship:<id>`, or `calm:interface:<node-id>/<interface-id>`. It must exist in the architecture (`CO-ADR-003`). |
| `rule` | string | A rule link `<rule-id>@<pack-id>/<pack-version>`, validated like `enforcedBy` (`CO-ADR-004`). |
| `reason` | string | Why the decision releases it. Required. |

`released` on an entry with `expect: active` is `CO-ADR-001`, as is an item with both `ref` and `rule`, or with neither.

## Lifecycle profile

The lifecycle profile maps the status of a decision to a class. It is the `adr.lifecycle` key of the workspace.

| Key | Type | Default | Description |
|---|---|---|---|
| `active` | string[] | `[accepted]` | Statuses that make a decision active. Only `accepted` and `implemented` are allowed. |
| `statusMap` | map | `{}` | Maps the exact status text of a decision (case-insensitive) to a provider status. |

Classes:

| Class | Statuses |
|---|---|
| `active` | The statuses in `active`. |
| `pending` | `proposed`, `draft`. A pending successor does not supersede its predecessor. |
| `inactive` | All others: `rejected`, `deprecated`, `superseded`, `abandoned`, `unknown`, and `accepted` or `implemented` when not listed in `active`. |

`statusMap` applies first. With `statusMap: {Ratified: accepted}`, a decision whose status text is `Ratified` is treated as `accepted`. The original status and status text are kept unchanged in every result; the mapped status is reported next to them. A status text that no entry maps, and the status `unknown`, are not active.

`proposed`, `rejected`, `deprecated`, `superseded`, `draft`, `abandoned`, and `unknown` cannot be listed under `active`: such a status could not be the only justification of a rule. A forbidden status, a mapping to a status that the provider does not use, or two mappings of one text to different statuses is `CO-ADR-005`.

## Triggers

A trigger names a change that should be justified by a decision. It is an item of `adr.triggers`.

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Unique trigger ID. |
| `category` | string | none | Category that a satisfying decision lists. One of `system-boundaries`, `deployment-strategy`, `security-controls`, `external-dependencies`, `quality-tradeoffs`, or a custom name. |
| `paths` | glob[] | `[]` | Changed files that raise the trigger. |
| `architecture` | string[] | `[]` | Architecture changes that raise it: `node-added`, `node-removed`, `relationship-added`, `relationship-removed`. |
| `nodeTypes` | string[] | `[]` | Narrows `architecture` to these node types. |
| `tags` | string[] | `[]` | Narrows `architecture` to nodes with these tags. |

A trigger needs at least one of `paths` and `architecture`. A duplicate ID, a trigger with neither, or a glob with bad syntax is `CO-ADR-005`.

## Workspace key

The `adr` key of the workspace is described in [workspace](workspace.md#adr). A workspace with `adr` and no `files` is valid and has nothing to validate. A profile replaces `adr.files`, as it replaces `bindings.files`.

```yaml
adr:
  files: [governance/decisions.yaml]
  lifecycle:
    active: [accepted]
    statusMap: {Ratified: accepted}
  triggers:
    - id: TRG-DEPLOY
      category: deployment-strategy
      paths: ["deploy/**"]
      architecture: [relationship-added, relationship-removed]
```

## adr validate

`concordia adr validate` checks the declarations before a run. It loads the workspace (with `--profile`), the architecture model, and the policy packs, then checks, in this order: the lifecycle profile and triggers (`CO-ADR-005`), the register files (`CO-ADR-001`, `CO-ADR-002`), the subjects of every entry against the architecture (`CO-ADR-003`), and the rule links against the loaded packs (`CO-ADR-004`). The first problem is reported.

The command is offline. It starts no CBM process, uses no network (a remote architecture resolves from the pinned cache), and writes no file.

| Result | Exit code |
|---|---|
| Valid; or the workspace has no `adr.files` (a note is printed) | 0 |
| A declaration is not valid; the diagnostic is printed on stderr | 2 |

The codes are in the [diagnostics reference](diagnostics.md). The option list is in the [CLI reference](cli.md#adr-validate).

## Presence

Presence answers one question per registered decision: is the ADR file tracked in the evaluated revision, and does the indexed text equal the committed text? Text that exists only in the index (an untracked file, a deleted file, an editor workspace of the provider) never satisfies it.

Concordia finds the decision like this:

1. The decision must belong to the evaluated project. A register entry with `project` sees only the decisions of that project. Presence is `unverifiable` with reason `no-revision` when that project has no git revision.
2. An entry with `path` selects the decision at that path. The path is relative to the project; in a multi-project run Concordia adds the `@<project>/` prefix.
3. An entry without `path` is matched by label: the canonical `adr_id` of the decision. `ADR-0003` in the register and `ADR-3` from the provider are the same label (the prefix without regard to case, the number without leading zeros). A file that moves keeps its register ID: with no `path` pin presence still passes at the new path.
4. More than one decision with the label gives no pick. If exactly one of them is tracked, that one is used. Otherwise presence is `unverifiable` with reason `duplicate-id`, and the other paths are named. The same duplicate is a [history anomaly](#supersession-history).

| State | Result | Meaning |
|---|---|---|
| `committed` | pass | The path is tracked in the evaluated revision, has no working-tree change, and the provider did not index it after a change. |
| `index-only` | violation | The provider reports the decision, but its path is not tracked in the evaluated revision (untracked or deleted). |
| `uncommitted-edit` | unknown | The path is tracked, but its working tree text differs (a dirty file) or the provider indexed it before a change (a stale file). Facts of the edited text are not used. |
| `absent` | violation | No decision matches the ID or the pinned path, and the ADR extraction coverage is complete. |
| `not-recognized` | unknown | The pinned path is tracked, but the provider reports no decision for it. |
| `path-mismatch` | violation | The register pins a path, and the decision whose label matches the ID is at another path. Both paths are named. |
| `unverifiable` | unknown | Concordia cannot verify presence. The reason says why. |

The reason of a presence result is one of these strings. The first three are finding reasons; the rest are evidence reasons of an `unknown` result.

| Reason | State | Next action |
|---|---|---|
| `index-only` | `index-only` | Commit the ADR file. |
| `absent` | `absent` | Add or commit the ADR, or correct the ID or path in the register. |
| `path-mismatch` | `path-mismatch` | Correct the `path` pin or the ID in the register. |
| `uncommitted-edit` | `uncommitted-edit` | Commit or discard the edit, and index again. |
| `not-recognized` | `not-recognized` | Make the file a decision the provider recognizes (title, status), or correct the path. |
| `no-revision` | `unverifiable` | Run in a git repository with a commit. |
| `no-capability` | `unverifiable` | Fix the analysis configuration: the provider has no ADR evidence. |
| `extraction-incomplete` | `unverifiable` | Fix the analysis configuration: a Markdown file in the indexed scope (not excluded and not dirty) is not indexed, so absence cannot be shown. Excluded and edited Markdown files do not count. CBM 0.12.1 extracts Markdown (`ADR` nodes, `SUPERSEDES` edges) only in index mode `full`; with `fast` or `moderate` no Markdown file is indexed. Set `observation.indexMode: full` and include the decision directory. |
| `duplicate-id` | `unverifiable` | Give the files different IDs, or pin the entry with `path`. |

When the provider has no ADR capability, presence, applicability, and history are all `unknown` and a warning diagnostic says so. Nothing is guessed.

## Applicability

Applicability says whether a committed decision counts as active. It is separate from presence and from enforcement. A decision whose presence is not `committed` has applicability `unknown`, because the facts of text that is not committed are not evidence: the reason is `not-committed`, or the reason of an `unverifiable` presence. Otherwise the state comes from the lifecycle profile and the [supersession history](#supersession-history).

| State | Reason | Meaning |
|---|---|---|
| `active` | none | The lifecycle class is active, and no active successor supersedes the decision. |
| `inactive` | `inactive` | The status is not active (`proposed`, `draft`, `rejected`, `deprecated`, `abandoned`, `unknown`), or it is `superseded` with only a pending successor. |
| `superseded` | `superseded` | A successor with an active lifecycle class supersedes the decision. |
| `ambiguous` | the first history anomaly of the component | The history of the connected decisions has an anomaly, so no member is active. |
| `unknown` | `not-committed`, `no-revision`, `no-capability`, `extraction-incomplete`, `duplicate-id` | The facts are not committed or not verifiable. |

## Supersession history

The history combines the `supersedes` edges of the provider and the `superseded_by` text of the ADRs into one directed graph, successor to predecessor.

**Nodes** are committed decisions, keyed by repository-relative path. The label of a node is its canonical `adr_id`; it is never the identity, so two files with the same number are two nodes. Decisions that are not committed are not nodes, and an edge from one is ignored.

**Links** come from two sources.

- An edge `A supersedes B` is a link `A → B`.
- A `superseded_by` value on `B` is link target text, not an ID (CBM 0.12.1). Concordia splits it on commas, semicolons, and whitespace. For each target it removes the surrounding backticks or angle brackets, a `#fragment`, and a leading `./`, and resolves the rest as a path relative to the directory of the ADR that holds the value. `..` is allowed inside the project. The path must equal the path of a committed decision; that decision is the successor. A target that escapes the project, is absolute, or matches no committed decision is a `missing-target` that names the target text. A value that has no readable target is also a `missing-target`.
- Only a target without a path separator and without a file extension is read as a decision ID (`ADR-7`, `ADR-0007`); it is compared by canonical label within the project of the holder.

Each link records its provenance: `edge`, `metadata`, or `both` when an edge and a `superseded_by` value name the same pair.

**Effect.** A link takes effect only when its successor has an active lifecycle class (or is itself `superseded`, so a chain stays quiet). A link whose successor is `proposed` or `draft` is `pending`: it is shown, and it changes nothing about the predecessor, also when the predecessor says `superseded`. When a link is effective but the predecessor's own status still says active, the predecessor is `superseded` and the report flags `statusLags`; the original status text is shown unchanged.

**Components.** Concordia connects the nodes through their links (ignoring direction) and analyzes only the components that contain a registered decision. Other components are not reported.

**Anomalies.** A component with an anomaly is `ambiguous` as a whole: every registered member gets applicability `ambiguous` with an `unknown` result. No date, number, or file order breaks the tie, and an unrelated component is not affected.

| Reason | Raised when |
|---|---|
| `cycle` | Successor links form a loop, including a decision that supersedes itself. All members are named. |
| `missing-target` | An edge or a `superseded_by` target names no committed decision. The target text is named. |
| `duplicate-id` | Two committed decisions have the same label. All their paths are named; the value is the label. |
| `contradictory-status` | A decision has status `superseded` and names no successor, or a successor has the lifecycle class inactive (`rejected`, `deprecated`, `abandoned`, `unknown`). A `proposed` or `draft` successor is pending, not contradictory. |
| `competing-successors` | A decision has two or more successors, edge or metadata, and for some pair neither supersedes the other. All of them are named. |

Without an anomaly the component has fan-in only. A decision with an effective successor is `superseded`. The other decisions get the applicability that their own lifecycle class gives: a head with an active class is `active`, and a head with another class is `inactive`.

The history is sorted and does not depend on the order of the input. The cycle search is iterative, so the number of decisions cannot overflow the stack.

**Limit of CBM 0.12.1.** The provider does not show a "supersedes X" statement whose X is not a committed decision, so such a statement is invisible and cannot be a `missing-target`. Only the other direction can show a missing target: a `superseded_by` value that resolves to no committed decision. The capability profile and the report state this limit. See the [capability profiles](capability-profiles.md).

## Obligations

The obligations are the rules `GOV-ADR-001` to `GOV-ADR-006` of the built-in pack `adr-governance` (`builtin:adr-governance`, version `1.0.0`). They run only when the pack is enabled in `policies.packs`. Their verdicts are `pass`, `violation`, `unknown`, `not-applicable`, and `error`; the severity, the `onUnknown` setting, the owner, waivers, baselines, `--rule`, and the exit codes work as for every other rule. The rules are not reducible: under `check --changed` they run on the full scope.

| Rule | Obligation | Default severity | Unit of result |
|---|---|---|---|
| `GOV-ADR-001` | `presence` | error | A registered decision. See [Presence](#presence). |
| `GOV-ADR-002` | `applicability` | error | A registered decision: replacement review. |
| `GOV-ADR-003` | `justification` | error | A mandatory rule that at least one decision links. |
| `GOV-ADR-004` | `enforcement` | error | A decision with `enforcement: required`. |
| `GOV-ADR-005` | `history` | error | An [anomaly](#supersession-history) of a component with a registered decision. |
| `GOV-ADR-006` | `scope` | warning | A configured trigger. See [Trigger evaluation](#trigger-evaluation). |

The result of a rule is the worst result of its units. A rule with no unit is `not-applicable` and says why.

All six rules have the family `decision-governance`. The pack declares no `enforcement`, so a violation gates like the violation of a code rule. `check.enforcement` entries with `family: decision-governance` set the mode to `mandatory`, `advisory`, or `shadow`. An active `check.rollback` runs these rules as `shadow` until its expiry, like the typed rules; see [check.rollback](workspace.md#checkrollback).

### Mandatory rules

A rule is **mandatory** when its severity is at or above `check.failOn` and its effective `onUnknown` is `fail`. The effective `onUnknown` is the one of the compiled rule, so the rule override, the control, the workspace `check.unknown`, and `--unknown` are already applied. Only the rules of the run are classified: a rule that `--rule` removed has no plan. It stays a valid link, but it is not judged by `GOV-ADR-003`, and `GOV-ADR-004` counts it as mandatory because it cannot show it to be advisory.

This is the PRD definition of a mandatory rule (design D6), and it does not read the enforcement mode of the rule (`enforcement` in the pack, or `check.enforcement` in the workspace). An `advisory` or `shadow` rule that meets both conditions is still mandatory here: it counts as enforcing the decision, but it does not block a run. See [gate by environment](../how-to/run-in-ci.md#gate-by-environment).

### Replacement review (`GOV-ADR-002`)

The register decides what a decision is expected to be (`expect`, default `active`). Concordia never moves the governance of a replaced decision to its successor.

| `expect` | Applicability | Result | Reason |
|---|---|---|---|
| `active` | `active` | `pass` | none |
| `active` | `superseded` | `violation` | `superseded`: the finding lists the governed subjects, the enforcement rules, and the successors that the history names, each with `applied: false`. |
| `active` | `inactive` | `violation` | `inactive` |
| `historical` | any definite state | `pass` when every link is transferred | none |
| `historical` | any definite state | `violation` when a link is not | `historical-link`: the finding names the untransferred subjects and rules. |
| any | `ambiguous` or `unknown` | `unknown` | The reason of the applicability. |

A link of a `historical` decision is **transferred** when it is listed under `released` (a subject by its ref, a rule by `<rule-id>@<pack-id>/<pack-version>`), or when another decision declares it and that decision is active: its applicability is `active` and its `expect` is `active`. A successor that is committed and active, but not in the register, declares nothing: its subjects and rules stay ungoverned, and it is only a candidate.

### Rule justification (`GOV-ADR-003`)

For each selected rule that at least one decision links with `enforcedBy`:

| Case | Result |
|---|---|
| The rule is advisory (below `check.failOn`, or `onUnknown` other than `fail`) | `not-applicable` |
| A linking decision is active (applicability `active` and `expect: active`) | `pass` |
| No linking decision is active, and at least one is definitely not (inactive, superseded, or expected `historical`) | `violation` |
| No linking decision is active, and every one is `ambiguous` or `unknown` | `unknown` |

A rule that no decision links has no result. The links of a `historical` decision never justify a rule. The finding names the rule, the linking decisions with their states, and the candidate successors of the decisions that are superseded.

### Enforcement coverage (`GOV-ADR-004`)

The enforcement state of a decision is `enforced` (a linked rule is mandatory), `advisory-only` (rules are linked, none is mandatory), `unlinked` (no rule is linked), or `not-applicable` (the decision is not active: superseded, inactive, or expected `historical`). It does not depend on presence. A decision that is committed and active, and links no rule, shows `unlinked` and raises no finding.

Only a decision with `enforcement: required` is a unit of the rule.

| Decision | Enforcement state | Result | Reason |
|---|---|---|---|
| Active | `enforced` | `pass` | none |
| Active | `advisory-only` | `violation` | `advisory-only` |
| Active | `unlinked` | `violation` | `enforcement-gap` |
| Superseded, inactive, or historical | `not-applicable` | `not-applicable` | none |
| Ambiguous or not verifiable | the state of the links | `unknown` | The reason of the applicability. |

### Finding reasons

Each finding has a reason. A `violation` has one of the reasons below; an `unknown` has an evidence reason (see [Presence](#presence) and [Applicability](#applicability)).

| Reason | Rule | Next action |
|---|---|---|
| `inactive` | `GOV-ADR-002` | Get review: the decision is not in force. Register its replacement, or set the entry to `historical`. |
| `superseded` | `GOV-ADR-002` | Get review: register the successor with the scope it takes over, then set the entry to `historical` or release what ends. |
| `historical-link` | `GOV-ADR-002` | Get review: declare the named subjects and rules in an active decision, or list them under `released` with a reason. |
| `rule-unjustified` | `GOV-ADR-003` | Get review: link the rule from an active decision, or change the rule. |
| `enforcement-gap` | `GOV-ADR-004` | Correct intent: link a mandatory rule, or set `enforcement: none`. |
| `advisory-only` | `GOV-ADR-004` | Correct intent: link a mandatory rule. |
| `trigger-unsatisfied` | `GOV-ADR-006` | Supply evidence: add or change a registered decision with the category of the trigger. |

The reasons of presence (`index-only`, `absent`, `path-mismatch`) and of the history anomalies (`cycle`, `missing-target`, `duplicate-id`, `contradictory-status`, `competing-successors`) are in the tables above.

The next action is one of the categories of the evaluation reporting contract:

| Next action | Reasons |
|---|---|
| `supply evidence` | `index-only`, `absent`, `path-mismatch`, `trigger-unsatisfied`, and the unknown reasons `uncommitted-edit`, `not-recognized`, `not-committed` |
| `get review` | `superseded`, `inactive`, `historical-link`, `rule-unjustified` |
| `correct intent` | `enforcement-gap`, `advisory-only`, and the history reasons |
| `fix analysis configuration` | `no-revision`, `no-capability`, `extraction-incomplete`, and a trigger that cannot be evaluated |

### Finding fields

Every governance finding has `area: adrGovernance`, the obligation, the subject (the decision ID or the rule ID), the result, the expected condition, the observed evidence or gap, the related decisions, and a `decision` block: `id`, `title`, `status`, `statusText`, `path`, `line`, `successors`, `predecessors`, `governs` (resolved subjects), `enforcedBy` (rule refs), and `candidateSuccessors`. The kind of a finding is `adr-<reason>`.

The location is the ADR file and the line of the decision when the provider reports it. Otherwise it is the register file and the line of the entry. A rule-justification finding points to the line of the link in the register. A trigger finding points to the changed file that raised it.

The **fingerprint** is the common fingerprint with these inputs and no path, line, or digest.

| Input | Value |
|---|---|
| Rule | The governance rule ID. |
| Source | The register decision ID in canonical form (`ADR-3`), with `@<project>` when the entry has a `project`. For a history anomaly: the smallest canonical ID of its members, which is the label when a member is not registered. For `GOV-ADR-003`: the linked rule ID. For `GOV-ADR-006`: the trigger ID. |
| Target | The sorted, comma-joined subject, rule, and successor IDs of the reason: for `superseded` the governed subjects, the rule IDs, and the successor IDs; for `historical-link` the untransferred subjects and rule IDs; for `advisory-only` the rule IDs; for an anomaly the member IDs, or the target text of a `missing-target`. |
| Edge kind | `adr:<reason>` |

Renaming an ADR file or editing its body does not change a fingerprint. A change of status that changes the reason (for example from `inactive` to `superseded`) does. The register ID is owned by the team, so renumbering it changes the identity by design.

### Untrusted text

ADR titles and text come from the repository. Concordia uses them only as data: it never runs or fetches a command or a link in them, and it reads Git only through the Git port. A secret value in a title is replaced by a redaction marker in the findings, the decision summary, and the diagnostics, and each redaction is recorded with its output, file, line, and class, never the value. Control sequences are escaped.

### Without a register

When the pack is enabled and no register is configured, each rule is `not-applicable` with the reason `no register`, and the result has no `decisionSummary`. When a register is configured and the pack is not enabled, no rule runs and `CO-ADR-007` says that the register is not evaluated.

## Trigger evaluation

Triggers are evaluated by `GOV-ADR-006` only when the run has a change set (`check --changed [ref]`). The change set is the base reference and the changed files, with the project prefix where the workspace has projects.

A trigger is **raised** when one of these is true.

- A changed file matches a `paths` glob. Each matching file is listed in `raisedBy`.
- The architecture change from the base model to the current model has a kind in `architecture`. The base model is read from the `architecture.source` file at the base reference. `raisedBy` lists `<kind>:<id>`, for example `relationship-added:orders-to-db`. `nodeTypes` and `tags` keep only nodes that match; a relationship matches when its source, container, or one target matches. For a removal the base model decides, for an addition the current model.

A raised trigger is **satisfied** when a registered decision whose `categories` contain the trigger category has its committed file in the change set (added or modified). `satisfiedBy` names those decisions. Custom category names are accepted as written.

| Case | Result | Reason |
|---|---|---|
| Raised and satisfied | `pass` | none |
| Raised and not satisfied | `violation` | `trigger-unsatisfied` |
| Not raised | `pass`, with `raised: false` | none; no ADR is required |
| No change set | `not-applicable` | `no change set`; `CO-ADR-009` says that triggers were not evaluated |
| Not raised by a path, and the base model cannot be read | `unknown` | `base model unreadable: <cause>` |

A trigger that cannot read the base model is `unknown` only through its `architecture` part. A path that raises the trigger still decides, and a trigger with only `paths` is never `unknown`. Results are sorted by trigger ID. No trigger exists by default.

## Result

`check` writes the governance result in two places of `results.json`: the area `areas.adrGovernance` (a verdict and counts, like every [area](results.md#areas)) and the top-level `decisionSummary` (the detail for each decision). `decisionSummary` is present only when the workspace has a register and the pack runs; the pack without a register fills only the area (every rule `not-applicable`), and a register without the pack fills neither. The shape of each field is in the [results reference](results.md#decision-summary).

```json
"areas": {"adrGovernance": {"verdict": "violation", "counts": {"pass": 3, "violation": 1, "unknown": 0, "notApplicable": 1, "error": 0, "findings": 1}}},
"decisionSummary": {
  "providerSupport": "supported",
  "decisions": [{
    "id": "ADR-3", "title": "Orders owns its database", "path": "docs/adr/0003-orders-own-database.md",
    "status": "accepted", "statusText": "Accepted", "expect": "active", "categories": ["system-boundaries"],
    "presence": {"state": "committed", "reason": ""},
    "applicability": {"state": "superseded", "reason": "superseded-by ADR-7", "statusLags": true},
    "enforcement": {"state": "not-applicable", "required": true, "rules": ["CORE-DB-001@core/1.0.0"]},
    "governs": ["calm:node:orders-db"], "released": [],
    "successors": ["ADR-7"], "predecessors": []
  }],
  "history": {"links": [{"from": "ADR-7", "to": "ADR-3", "fromPath": "docs/adr/0007-orders-managed-database.md", "toPath": "docs/adr/0003-orders-own-database.md", "provenance": "both", "effect": "effective"}]},
  "ruleJustification": [{"rule": "CORE-DB-001@core/1.0.0", "mandatory": true, "linkedBy": ["ADR-3"], "result": "violation"}],
  "triggers": [{"id": "TRG-DEPLOY", "category": "deployment-strategy", "raised": true, "satisfiedBy": [], "result": "violation"}],
  "candidates": [{"kind": "unregistered-decision", "id": "ADR-7", "path": "docs/adr/0007-orders-managed-database.md"}],
  "findings": ["sha256:..."]
}
```

The three states of a decision (`presence`, `applicability`, `enforcement`) are separate fields. Every decision is named in canonical form everywhere in the result (`ADR-3`, whether the register says `ADR-0003` or `adr-3`); an unregistered decision has the canonical form of its provider label (`ADR-7`). History links and candidates also give the path, which is the identity of the file. `findings` lists fingerprints only; the finding objects stay in the common list. To roll the rules out without blocking, see [govern architecture decisions](../how-to/govern-architecture-decisions.md). The diagnostics `CO-ADR-001` to `CO-ADR-009` are in the [diagnostics reference](diagnostics.md), the policy keys in [policies](policies.md#decision-governance-rules-the-decisions-key), the workspace keys in [workspace](workspace.md#adr), and the commands in the [CLI reference](cli.md#adr-validate).
