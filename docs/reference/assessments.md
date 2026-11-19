# Assessments reference

An assessment is a written review of a security or quality property of a scope, for one environment. Concordia keeps assessments as committed YAML documents, binds each approval to the exact assessment version and to the dependencies it covers, and derives the review state from decision records instead of from a field that an author can edit.

This page describes the four document kinds, their layout, the version digest, the review states, and the outcome. `concordia assessment validate` checks the documents offline; see [assessment validate](cli.md#assessment-validate). Which approvals count (the authority) and when an approval is current are separate topics: see [`assessments`](workspace.md#assessments) in the workspace reference.

## Document kinds and layout

All four kinds use `apiVersion: concordia.io/v1alpha1` and a closed schema: an unknown key, a missing required key, or a value outside its list is `CO-ASM-001` and exit code 2. The schemas are embedded in the program (`internal/assessment/schemas/`).

| Kind | Schema ID | Default file |
|---|---|---|
| `Assessment` | `https://concordia.io/schemas/v1alpha1/assessment.schema.json` | `assessments/<id>/v<N>.yaml` |
| `Assumption` | `https://concordia.io/schemas/v1alpha1/assumption.schema.json` | `assessments/assumptions/<id>.yaml` |
| `AssessmentApproval` | `https://concordia.io/schemas/v1alpha1/approval.schema.json` | `assessments/approvals/<assessment>-v<N>-<seq>.yaml` |
| `AssessmentRevocation` | `https://concordia.io/schemas/v1alpha1/revocation.schema.json` | `assessments/revocations/<approval id>.yaml` |

`assessments.paths` lists the directories, files, and globs to read. A directory is read recursively for `*.yaml` and `*.yml`. The loader tells the kind of a file from its `kind` key, not from its path, so the layout is a convention: the commands that write records use it, and an authority's `paths` patterns select records by it. A path that does not exist is `CO-ASM-001`. A workspace without an `assessments` key reads the default directory `assessments/` when it exists.

Each file holds one document. Documents are checked against each other when they are loaded:

- An assessment id and version pair is unique. Two files that declare the same pair are `CO-ASM-001`; the message names both locations.
- An assumption id, an approval id, and a revocation id is unique.
- An assessment lists only assumptions that a document declares.
- An approval binds an assessment version that a document declares, and a revocation names an approval that a document declares.
- An approved version is not edited, see [Immutable versions](#immutable-versions).

## Assessment

```yaml file=assessments/ASM-EDGE/v1.yaml
# The review of the public entry points of the gateway.
apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-EDGE
version: 1
type: security
title: Edge exposure
scope:
  calmNodes: [api-gateway]
environment: production
subject:
  summary: Public entry points of the gateway
  dependencies:
    - kind: public-entry-point
      selector:
        calmNodes: [api-gateway]
assumptions: [ASM-EDGE-1]
evidence:
  - id: EV-1
    ref: docs/security/edge-review.md
outcome: satisfactory
state: ready-for-review
```

| Key | Type | Default | Description |
|---|---|---|---|
| `apiVersion` | `concordia.io/v1alpha1` | required | Schema version. |
| `kind` | `Assessment` | required | Document kind. |
| `id` | string | required | Stable id: letters, digits, `.`, `_`, `-`; it starts with a letter or digit. |
| `version` | integer, at least 1 | required | Version number. A version is immutable, see [Immutable versions](#immutable-versions). |
| `type` | `security`, `quality` | required | Assessment type. |
| `title` | string | none | Short title. |
| `notes` | string | none | Free text. Notes never count as a review: a note that says "reviewed" has no effect. |
| `reviewed` | boolean | `false` | An informal flag. It is a known key, so a file that carries it loads, and it is ignored for the review state: repository text that says "reviewed" is not an approval. Only a verified approval record gives the state `approved`, so a required assessment with `reviewed: true` and no verified approval is still a violation. |
| `scope` | object | required | The architecture elements covered: at least one of `calmNodes`, `calmRelationships`, `nodeTypes`, `tags` (lists of strings). |
| `environment` | string | required | The environment that the assessment is for. An approval for another environment does not satisfy it. |
| `subject` | object | required | What is assessed: `dependencies`, `scenarios`, or both, and an optional `summary`. See [subject](#subject). |
| `assumptions` | string[] | required | Ids of the assumptions that the assessment relies on. May be empty. |
| `evidence` | list | required | Evidence items. May be empty. See [evidence](#evidence). |
| `outcome` | enum | required | The result of the assessment, see [Outcome](#outcome). |
| `state` | `draft`, `ready-for-review` | `draft` | The authored review state, see [Review states](#review-states). |
| `dispositions` | list | `[]` | Accounts for dependency items that were removed: `identity`, `disposition` (`removed-intentionally`, `retired`, or `moved` with `movedTo`), and an optional `reason`. A disposition takes effect only when a verified approval binds the version that lists it. |

### subject

| Key | Type | Description |
|---|---|---|
| `summary` | string | Optional text. |
| `dependencies` | list | Dependency selectors: each item has a `kind` and a `selector`. At least one item when the key is present. |
| `scenarios` | string[] | Quality scenarios by id. Each is a dependency of kind `quality-scenario` with the selector `identity: <id>`. |

At least one of `dependencies` and `scenarios` is required. A `quality` assessment can name a scenario as its subject, and any assessment can list a `quality-scenario` among its `dependencies`.

A dependency `kind` is one of the closed list below. Any other kind is `CO-ASM-001`.

| Kind | Meaning |
|---|---|
| `runtime-identity` | The identity a workload runs as (service account, identity provider reference). |
| `role-binding` | A binding of a role to a principal. |
| `secret-reference` | A reference to a secret, never its value. |
| `configuration-reference` | A reference to a configuration object. |
| `storage-reference` | A reference to a volume or storage resource. |
| `public-entry-point` | An ingress, route, or backend that is reachable from outside. |
| `protocol` | The protocol of a connection. |
| `certificate-reference` | A reference to a certificate. |
| `external-destination` | A destination outside the system. |
| `trust-context` | A trust dimension value of an element. |
| `data-classification` | The data classification of an element or flow. |
| `placement` | The region, zone, or hosting of a workload. |
| `binding` | A binding of a CALM element to a code entity. |
| `control` | A control that the assessment relies on. |
| `implementation` | The content of a file that implements a claim. |
| `verification` | A check result: the outcome of a named check in an environment. See [Verification and quality scenarios](#verification-and-quality-scenarios). |
| `quality-scenario` | A quality scenario of `assurance.quality`. See [Verification and quality scenarios](#verification-and-quality-scenarios). |

A `selector` is a mapping of facet names to a string or a list of strings. Which facets a kind understands, and which fields enter its digest, is set by the digest contract of the change-impact analysis, not by the document schema.

#### Verification and quality scenarios

The kinds `verification` and `quality-scenario` come from the evidence of the [assurance adapters](assurance.md). They are supported when the workspace has an `assurance` key. Without the key, the capability profile of the run does not list them, and a dependency of either kind is `unknown` with the reason `unsupported-family`, as before.

| Kind | Identity | Items | Selector facets | Fields in the digest |
|---|---|---|---|---|
| `verification` | `check:<check id>@<environment>` | One for each check and environment that has an admissible check result of [`assurance.verification`](workspace.md#assuranceverification). An inadmissible result is not an item, so it cannot stand behind an approval. | `check`, `subject`, `result` | `subject` (the elements that the results cover), `result` (the outcome and the statement digest of each admissible result) |
| `quality-scenario` | `scenario:<scenario id>` | One for each scenario of [`assurance.quality`](workspace.md#assurancequality). | `identity` (the scenario id, with or without the `scenario:` prefix), `scenario`, `responseMeasure` | `scenario` (the digest of the definition, without its place in the file), `responseMeasure` (the criterion in words) |

An item holds references and digests, never the content of a result or a scenario. A new outcome or a new result statement changes the digest of a `verification` item. A changed definition or criterion changes the digest of a `quality-scenario` item, and moving a scenario inside its file changes nothing. Both kinds have the trigger class `implementation-verification`: see [Review triggers](../explanation/review-currency.md#review-triggers).

When the source of an assurance kind is not completely read (no `assurance.verification` result source, a glob that matches no file, a result file that cannot be read, or no `assurance.quality` scenario source), the fact source reports the kind as `no-evidence-supplied`, and an empty match is not recorded as an empty set. The currency is then `unknown` with the reason `coverage-incomplete` (the closed set of [Reasons of unknown](#reasons-of-unknown) maps every other fact-source reason to it), and the cause detail names the missing source. An `unknown` currency is never a pass.

### evidence

| Key | Type | Description |
|---|---|---|
| `id` | string | Required. Unique inside the document. |
| `ref` | string | Required. A repository path or a URL of the evidence. |
| `digest` | `sha256:<hex>` | Digest of the evidence content, if the author recorded it. |
| `description` | string | Optional text. |

## Assumption

An assumption states something that an assessment relies on. It declares dependency selectors, explicit evidence, or both; with neither it is `CO-ASM-001`. An assessment that lists the assumption depends on every entity that the selectors match.

```yaml file=assessments/assumptions/ASM-EDGE-1.yaml
apiVersion: concordia.io/v1alpha1
kind: Assumption
id: ASM-EDGE-1
statement: Only the gateway is reachable from the internet.
scope:
  calmNodes: [api-gateway]
owner: security-team
verificationMethod: review
selectors:
  - kind: public-entry-point
    selector:
      calmNodes: [api-gateway]
```

| Key | Type | Description |
|---|---|---|
| `apiVersion` | `concordia.io/v1alpha1` | Required. |
| `kind` | `Assumption` | Required. |
| `id` | string | Required. Stable id, unique across assumptions. |
| `statement` | string | Required. The assumption in one sentence. |
| `scope` | object | Required. Same shape as the assessment `scope`. |
| `owner` | string | Required. Who answers for the assumption. |
| `verificationMethod` | string | Required. How the assumption is verified, for example `review` or `test`. |
| `selectors` | list | Dependency selectors (`kind` and `selector`), as in the assessment `subject`. At least one item when present. |
| `evidence` | list | Evidence items, as in the assessment. At least one item when present. |

## Immutable versions

Each pair of assessment id and version is one immutable record. A change to the scope, the subject, the assumption list, the evidence, or the outcome is a new version, saved as the next file. Both versions load, the earlier approval stays bound to the earlier version, and the new version has no approval.

The **version digest** is `sha256:` and the hex SHA-256 of the canonical JSON of the parsed document without the authored `state`. Canonical JSON sorts object keys, so comments, formatting, quoting, and key order do not change the digest. Any other change does, including the list order of `subject`, `assumptions`, and `evidence`. The digest is computed from the file alone, so it works in a shallow clone.

An approval or rejection binds a version by this digest. If the file of that version has another digest, `assessment validate` and `check` report `CO-ASM-002` with exit code 2. The message names the version, the record, and both digests, and the approval is not applied to the changed file.

## Review states

| State | Set by |
|---|---|
| `draft` | The author. |
| `ready-for-review` | The author. |
| `approved` | A verified approval record with the decision `approved` as the latest decision of the version. |
| `rejected` | A verified approval record with the decision `rejected` as the latest decision. |
| `review-required` | Currency evaluation: the approval no longer covers the current system. |
| `expired` | Currency evaluation: the approval's expiry has passed. |
| `revoked` | A verified revocation that names the latest approval by id and digest. |

A document authors only `draft` or `ready-for-review`; an assessment without a `state` key is `draft`. Any other authored value is `CO-ASM-003` and exit code 2, so a file never shows a version as approved. Without a verified decision record the derived state is the authored one.

Only decision records whose authority is verified count. `assessment validate` checks the form of the records and the digests they bind, and it does not verify who made them; it never prints a review state.

## Outcome

The outcome is stored in the document and shown beside the state. It does not imply a state: a version with the outcome `satisfactory` and no approval stays `draft` or `ready-for-review`, and a reviewer can approve a version with the outcome `unsatisfactory`, which is then `approved` with the outcome `unsatisfactory`; the report shows both.

| Outcome | Meaning |
|---|---|
| `satisfactory` | The assessed property holds. |
| `satisfactory-with-conditions` | It holds if the stated conditions hold. |
| `unsatisfactory` | It does not hold. |
| `inconclusive` | The assessment could not decide. |

## Approval record

An approval record is a document of kind `AssessmentApproval`. It is a decision about one assessment version, and it binds everything that the decision rests on. A record that omits a bound field is `CO-ASM-001`.

The digests in this sample and in the revocation sample below are the real digests of the sample files on this page.

```yaml file=assessments/approvals/ASM-EDGE-v1-1.yaml
apiVersion: concordia.io/v1alpha1
kind: AssessmentApproval
id: ASM-EDGE-v1-1
decision: approved
assessment:
  id: ASM-EDGE
  version: 1
  digest: sha256:8499a1adaceb62a7d3265f32993c9d847235a97efe4a261c5bced5cbc3f6fd04
subject:
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000002
  dependencies:
    - kind: public-entry-point
      identity: k8s:ingress/api
      digest: sha256:0000000000000000000000000000000000000000000000000000000000000003
scope:
  calmNodes: [api-gateway]
environment: production
assumptions:
  - id: ASM-EDGE-1
    digest: sha256:355debb4d5d90332fce47c2618ab38dab622a8a9c272c6499da999c8c3a0f9b9
evidence:
  - id: EV-1
    digest: sha256:0000000000000000000000000000000000000000000000000000000000000005
contractVersion: review-digest/1
capabilityProfile: cbm-0.12.1
reviewer:
  identity: alice@example.com
  authority: security-review
decidedAt: 2026-05-01T10:00:00Z
producedUnder:
  normalizationVersion: normalize/1
  evaluatorVersions:
    review-currency: 1.0.0
  policyDigest: sha256:0000000000000000000000000000000000000000000000000000000000000006
  authorityDigest: sha256:0000000000000000000000000000000000000000000000000000000000000007
expiry:
  expiresAt: 2027-05-01T10:00:00Z
  conditions:
    - a new public route is added
```

| Key | Type | Description |
|---|---|---|
| `id` | string | Required. Unique across approvals. |
| `decision` | `approved`, `rejected` | Required. |
| `assessment` | object | Required. `id`, `version`, and `digest`: the version digest of the file that the reviewer examined. |
| `subject` | object | Required. `digest`: the digest of the subject. `dependencies`: the dependency items behind it, each with `kind`, `identity`, and `digest` (may be empty when every selector matched nothing). |
| `scope` | object | Required. The scope of the assessment, as in the document. |
| `environment` | string | Required, not empty. An approval satisfies only a requirement for this environment. |
| `assumptions` | list | Required. Each assumption the version lists, as `id` and `digest` of the assumption document. May be empty. |
| `evidence` | list | Required. Each evidence item, as `id` and `digest`. May be empty. |
| `contractVersion` | string | Required. The digest-contract version that the digests were computed under, for example `review-digest/1`. |
| `capabilityProfile` | string | Required. The identity of the capability profile that supplied the evidence. |
| `reviewer` | object | Required. `identity` and the `authority` under which the reviewer decided. |
| `decidedAt` | RFC 3339 time | Required. |
| `producedUnder` | object | Required. The values the approval was produced under, so that a change of any of them can invalidate it: `normalizationVersion` (string), `evaluatorVersions` (mapping of evaluator name to version, may be empty), `policyDigest` (`sha256:<hex>` of the policy set that selected the review rule), and `authorityDigest` (`sha256:<hex>` of the authority configuration at the trust anchor). All four keys are required. |
| `expiry` | object | Optional. `expiresAt` (RFC 3339, after `decidedAt`) and `conditions` (free-text conditions that end the approval). At least one of the two. |
| `previous` | object | Optional. For an incremental approval: the `approval` id and the `subjectDigest` that it bound. Needs `delta`. |
| `delta` | object | Optional. For an incremental approval: `added` and `removed` items (`kind`, `identity`, `digest`) and `modified` items (`kind`, `identity`, `previousDigest`, `digest`). All three lists are required; they may be empty. Needs `previous`. |

An item is listed once per `kind` and `identity`. Concordia recomputes the delta from the two subjects, so a delta that differs from the recomputed one makes the approval invalid. Dispositions of removed items are listed in the assessment version that the approval binds.

The **record digest** is `sha256:` and the hex SHA-256 of the canonical JSON of the parsed record. It does not depend on key order or formatting, and a timestamp has the same meaning whether or not it is quoted. A signed record is signed over the canonical JSON, not over the file bytes.

## Revocation record

A revocation is a document of kind `AssessmentRevocation`. It names an approval by id and by record digest, so it cannot apply to an approval that was changed after the revocation was made.

```yaml file=assessments/revocations/ASM-EDGE-v1-1.yaml
apiVersion: concordia.io/v1alpha1
kind: AssessmentRevocation
id: ASM-EDGE-v1-1
approval:
  id: ASM-EDGE-v1-1
  digest: sha256:684f1e1c05a2c2a97b6f460ec431200dfa9d325424e3eaf03a00872ae8b41478
reviewer:
  identity: alice@example.com
  authority: security-review
decidedAt: 2026-06-01T10:00:00Z
reason: The gateway moved to another zone.
```

| Key | Type | Description |
|---|---|---|
| `id` | string | Required. Unique across revocations. |
| `approval` | object | Required. `id` and `digest` of the approval record. |
| `reviewer` | object | Required. `identity` and `authority`. |
| `decidedAt` | RFC 3339 time | Required. |
| `reason` | string | Required. |

## Currency

Currency says whether the approval of an assessment version still covers the current system. `check` and `assessment status` evaluate it on every run, from the same function (`assessment.Evaluate`), with the evaluation clock (`CONCORDIA_NOW`, or `--at` for `assessment status`). Each assessment version has exactly one currency state.

| Currency | Meaning |
|---|---|
| `current` | A verified approval binds the current subject, the comparison was complete, and nothing in it changed. |
| `review-required` | The approval does not cover the current system, or there is no verified approval. The reasons say why. |
| `expired` | The `expiresAt` of the approval is not after the evaluation time. |
| `revoked` | A verified revocation names the approval by id and digest. |
| `unknown` | A needed comparison cannot run. One closed reason says why. |

The evaluation runs these steps in order:

1. Load the documents. An invalid document or an edited approved version stops the run with exit code 2.
2. Verify the approvals and revocations (see the [workspace](workspace.md#assessments) authorities). The latest verified approval of the version by decision time decides. A version without one is compared with the latest approval of an earlier version of the same id; its dependencies are the comparison basis, and the version stays `review-required`.
3. A verified revocation gives `revoked`. An expiry time that has passed gives `expired`. Both run on every call, also when no file changed.
4. The environment, the version digest, the scope, the assumption and evidence digests, the digest contract, the capability profile, the policy, the trust authority, the evaluator and normalization versions, and the dependency items are compared with what the approval bound. A mismatch gives `review-required` with its reasons.
5. A comparison that cannot run gives `unknown`.
6. Otherwise the result is `current`.

Text in the document never counts: a `reviewed: true` flag or a note such as "reviewed by security" does not change the state. Without a verified approval the result is `review-required` with the reason `no approval`.

### Reasons of review-required

| Reason | Meaning |
|---|---|
| `no approval` | No verified approval binds the version. This includes an approval that is only in the candidate change when the authority uses `protected-review`; the record is listed under `notApplied`. |
| `approval rejected` | The latest verified decision is a rejection. |
| `environment mismatch` | The approval binds another environment than the assessment, or the required entry names another environment than the assessment. |
| `scope mismatch` | The scope of the version differs from the scope the approval binds, or a required entry names scope that the assessment does not cover. |
| `type mismatch`, `authority mismatch` | A required entry names another type, or another authority than the approval's. |
| `assessment version digest mismatch` | The version digest differs from the one the approval binds. |
| `subject digest mismatch` | A dependency item was added, removed without a verified disposition, or modified. The triggers name the items. The approval also gives this reason when the subject digest it records does not match the dependencies it lists. |
| `assumption digest mismatch`, `evidence digest mismatch` | An assumption or an evidence entry changed, was added, or was removed. The digest of an evidence entry is the digest the document authors, else the digest of the file `ref` at the revision. |
| `digest contract changed` | The approval was bound under another digest contract version than the current one, and no tested equivalence covers it. |
| `policy changed`, `trust authority changed`, `normalization version changed`, `evaluator version changed` | The value that the approval records under `producedUnder` differs from the current value. A value the run cannot supply is not compared. |
| `capability profile changed` | The capability profile that supplied the evidence is another one. |
| `incremental approval invalid` | The recorded delta of an incremental approval differs from the recomputed delta. See below. |

### Reasons of unknown

An `unknown` result carries one reason from the closed set: `trust-anchor-unreadable`, `verification-unavailable`, `unsupported-family`, `target-not-declared`, `reference-unresolved`, `coverage-incomplete`, or `limit-exceeded`. When several causes apply, the first in that order is the reason, and `causes` lists all of them. `no approval`, `environment mismatch`, and `digest contract changed` are review-required reasons and never unknown reasons. A change that makes the result `review-required` takes precedence over an unknown cause, except when a traversal limit stopped the analysis and the dependency comparison is the only reason: the result is then `unknown` with `limit-exceeded`.

A reference whose target declaration was deleted gives the cause `target-not-declared`. The approved item stays in the comparison as a removed item, so the result is `review-required` until a disposition is recorded.

### Initial and incremental approvals

An initial approval binds the whole subject. A scope that has no approval has no comparison basis, so every dependency item counts as new; agreement of a diagram with the model is never an approval.

An incremental approval names the previous approval (`previous.approval`) and its subject digest, and lists the exact `delta`. The evaluation recomputes the delta from the dependencies of the two approvals. A recorded delta that leaves out a recomputed item, or lists an item that did not change, makes the approval invalid: the result is `review-required` with `incremental approval invalid`, and the warning `CO-ASM-006` names the item. The previous approval must itself be verified. An approval never covers a change that it does not bind: a dependency added after the approval is a trigger.

### Triggers

Each changed dependency is a reason for review, not a finding about the system. A trigger names the changed dependency, its class (exposure, boundary-crossing, identity, sensitive reference, protection, environment or hosting, implementation or verification, removal or capability loss), the kind of change (`added`, `removed`, `modified`), the assumptions on the path, the assessment, the shortest dependency path, all contributing evidence, and the next action `get review`. All changes that reach the same assessment are merged into one trigger.

A removed item stays in `undisposed` until the version that a verified approval binds lists a disposition (`removed-intentionally`, `retired`, or `moved`) for its identity. Items with a disposition are listed under `disposed`.

### Consumers

`Evaluation.CurrencyFor(subject)` returns the assessments that cover a CALM node or relationship id, a quality scenario id, or an assessment id: the latest version of each, with its type, currency state, reasons, approval id, and the required entries that name it. It reads the evaluation that `check` made. Views use it for their `assessmentCurrency` entry, and a `quality` assessment is returned as well as a `security` one.

The rule `REV-APR-001` maps the currency of a required assessment to a verdict: `current` is a `pass`; `review-required`, `expired`, `revoked`, and no approval are a `violation`; `unknown` is `unknown` with its one reason.

### Status output

`concordia assessment status --json` prints one canonical JSON document. This sample is the output for one approved assessment that is `current`; a test runs the command and compares the keys of the output with the keys of this sample. Keys that appear only when they apply (`basis`, `unknownReason`, `incomplete`, `title`, `reason`) are not in the sample. `required[].currency` has the same keys as `assessments[].currency`.

```json
{
  "assessments": [
    {
      "currency": {
        "analysis": { "closureSize": 1, "complete": true, "visited": 0, "widened": false },
        "approval": {
          "authority": "security-review",
          "conditions": [],
          "contractVersion": "review-digest/1",
          "decidedAt": "2026-05-01T10:00:00Z",
          "decision": "approved",
          "expiresAt": "2026-06-02T00:00:00Z",
          "id": "ASM-EDGE-v1-1",
          "mechanism": "protected-review",
          "reviewer": "alice@example.com",
          "verified": true
        },
        "causes": [],
        "details": [],
        "disposed": [],
        "notApplied": [],
        "reasons": [],
        "regressions": [],
        "state": "current",
        "triggers": [],
        "undisposed": []
      },
      "digest": "sha256:eae0fa20b93a7af090a71d209695babb365aa0b08cd7c91241439ebf84112509",
      "environment": "production",
      "file": "assessments/ASM-EDGE/v1.yaml",
      "id": "ASM-EDGE",
      "latest": true,
      "line": 1,
      "outcome": "satisfactory",
      "required": ["edge"],
      "scope": { "calmNodes": ["api-gateway"] },
      "state": "approved",
      "subjects": ["ASM-EDGE", "api-gateway"],
      "type": "security",
      "version": 1
    }
  ],
  "capabilityProfile": "cbm-0.12.1",
  "contractVersion": "review-digest/1",
  "cycles": [],
  "diagnostics": [],
  "digestImpact": { "affected": [], "current": "review-digest/1", "equivalent": [], "invalid": [] },
  "evaluatedAt": "2026-05-01T10:00:00.000Z",
  "required": [
    {
      "assessment": "ASM-EDGE",
      "authority": "security-review",
      "currency": "same keys as assessments[].currency",
      "environment": "production",
      "id": "edge",
      "scope": { "calmNodes": [], "nodeTypes": [], "tags": [] },
      "type": "security",
      "version": 1
    }
  ],
  "verification": [
    {
      "applied": true,
      "authority": "security-review",
      "detail": "",
      "kind": "approval",
      "mechanism": "protected-review",
      "reason": "",
      "recordId": "ASM-EDGE-v1-1",
      "signer": "alice@example.com"
    }
  ],
  "visited": 0
}
```

`digestImpact` lists the verified approvals that the current digest contract reaches (see [review currency](../explanation/review-currency.md#digest-impact-example)). `evaluatedAt` is the time that expiry was judged at.

## Review rules

The built-in pack `builtin:review-currency` (version `1.0.0`) has four rules. Each has the family `assessment-currency`, the severity `error`, no `subject`, and a `check` that `assessmentCurrency` names. They report in the area `assessmentCurrency`, and `--changed` never reduces them. The rule format is in [Policies reference](policies.md#review-currency-rules-the-assessmentcurrency-key).

| Rule ID | `assessmentCurrency` | Unit of result | Result |
|---|---|---|---|
| `REV-APR-001` | `approval` | Each required assessment | `pass` for `current`. `violation` for `review-required`, `expired`, `revoked`, and no approval. `unknown` with one reason of [Reasons of unknown](#reasons-of-unknown) for `unknown`. |
| `REV-DSP-001` | `disposition` | Each removed or no longer selected item of a required assessment | `violation` for an item in `undisposed`. `pass` when each item has a verified disposition (`removed-intentionally`, `retired`, or `moved`). |
| `REV-CAP-001` | `capability` | Each dependency of an approved assessment whose kind or selector facet the capability profile no longer supports | `unknown` with the closed reason of the loss (for example `unsupported-family`); the loss is shown. `pass` for an assessment with no open loss. |
| `REV-GOV-001` | `governance` | Each relaxation of the review rules, of the `check.enforcement` entries for review rules, or of an authority | `violation` without a governance disposition at the trust anchor. `unknown` when the trust anchor cannot be read. |

- The pack declares no `enforcement`. The rules are `advisory` by default, and `check.enforcement` entries with `family: assessment-currency` or `pack: review-currency` set the mode. A verdict never depends on the mode.
- A `mandatory` mode needs one authority and a trust anchor (`CO-ASM-004`, exit code 2) and the guarantees of the enforcement check (`CO-POL-018`).
- An active `check.rollback` runs the rules as `shadow` until its expiry.
- A workspace with no required assessments gives four `not-applicable` rules, and `areas.assessmentCurrency` is `not-configured`.
- The findings and fields of the area are in [Results reference](results.md#assessment-currency).

## Diagnostics

| Code | Severity | Exit code | Meaning |
|---|---|---|---|
| `CO-ASM-001` | error | 2 | A document is not valid: unreadable, syntax, schema, unknown key, unknown reference, duplicate version, an assumption without selectors and evidence. |
| `CO-ASM-002` | error | 2 | The file of an approved version has another digest than the record binds. |
| `CO-ASM-003` | error | 2 | A document authors a state other than `draft` or `ready-for-review`. |
| `CO-ASM-004` | error | 2 | A mandatory review rule has no authority or no trust anchor. |
| `CO-ASM-005` | error | 2 | `assessment approve` or `assessment revoke` is refused: it runs in CI, or the reviewer or the confirmation digest is missing. |

Every diagnostic of a document names the file and the line.

The warnings and notes `CO-ASM-006` to `CO-ASM-011` (approval not applied, trust anchor unreadable, incomplete change-impact analysis, traversal cycle, incremental cache not usable, and signature verification unavailable) never stop a run. They are in the [diagnostics reference](diagnostics.md#assessment-currency-asm). See [diagnostics](diagnostics.md) for the full catalog.

## Related

- [CLI](cli.md#assessment-validate): `assessment validate`.
- [Workspace](workspace.md#assessments): `assessments.paths`, authorities, and required assessments.
- [Trust context](trust-context.md): the dependency kinds and the trust dimensions.
