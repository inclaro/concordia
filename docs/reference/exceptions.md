# Exceptions reference

Exceptions let a run accept known findings without deleting the rule. Concordia has two kinds: the baseline (a snapshot of inherited findings) and waivers (individual, owned, expiring). A waiver covers one finding by fingerprint, or a scope of findings with a verified approver. This page describes both file formats and how Concordia assigns each finding a disposition.

For tasks, see [Manage baselines and waivers](../how-to/manage-baselines-and-waivers.md). For the reasoning, see [Fingerprints, baselines, and waivers](../explanation/fingerprints-baselines-waivers.md).

## Files

The workspace keys `exceptions.baseline` and `exceptions.waivers` set the paths (see [Workspace reference](workspace.md)).

| Key | Default |
|---|---|
| `exceptions.baseline` | `exceptions/concordia.baseline.json` |
| `exceptions.waivers` | `exceptions/waivers.yaml` |

## Baseline JSON

`concordia baseline create` and `baseline update` write the file as canonical JSON (sorted keys), with entries sorted by fingerprint. The same inputs give a byte-identical file.

```json
{
  "schemaVersion": "1.0",
  "createdFrom": {
    "sourceRevision": "<git sha or null>",
    "graphGeneration": "<graph generation>",
    "policyDigest": "sha256:<hex>"
  },
  "findings": [
    {
      "fingerprint": "sha256:<64 hex>",
      "ruleId": "CO-BND-001",
      "owner": "team-billing",
      "reason": "Inherited before enforcement",
      "createdAt": "2026-06-01T00:00:00.000Z"
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `schemaVersion` | const, required | `"1.0"`. |
| `createdFrom.sourceRevision` | string or null, required | Git revision of the run that created the baseline. |
| `createdFrom.graphGeneration` | string, required | Graph generation of that run. |
| `createdFrom.policyDigest` | string, required | `sha256:` of the sorted policy pack digests, joined by newlines. |
| `findings[].fingerprint` | string, required | Matches `^sha256:[0-9a-f]{64}$`. Unique within the file. |
| `findings[].ruleId` | string, required | Rule that produced the finding. |
| `findings[].owner` | string, required | The finding owner, or `unassigned` when `baseline create` finds none. |
| `findings[].reason` | string, required | `Inherited before enforcement` for `baseline create`; the `--reason` value for `baseline update --accept`. |
| `findings[].createdAt` | string, required | ISO 8601 timestamp (honors `CONCORDIA_NOW`). |
| `findings[].expiresAt` | string, optional | Set by `baseline create --expires`. The pipeline does not read it. |

An invalid file gives `CO-EXC-001`. `baseline create` skips findings that are already `waived`.

## Waiver YAML

`concordia waiver add` appends to the file and creates it when missing, with `apiVersion: concordia.io/v1alpha1` and `kind: ExceptionRegister`. The register has two entry shapes, one parser, and one id space. Every entry has `rule` and exactly one of `finding` and `scope`.

### Finding waiver

A finding waiver covers one finding by its fingerprint. It needs no approver.

```yaml
apiVersion: concordia.io/v1alpha1
kind: ExceptionRegister
waivers:
  - id: WV-1
    finding: sha256:0000000000000000000000000000000000000000000000000000000000000001
    rule: CO-BND-001
    owner: team-billing
    reason: Refund migration in progress
    issue: BILL-123
    expires: 2026-12-31
```

### Scoped waiver

A scoped waiver covers the findings of one rule that match a scope. It does not use a fingerprint, so a change of fingerprints does not orphan it. It needs an approver that Concordia can verify.

```yaml
apiVersion: concordia.io/v1alpha1
kind: ExceptionRegister
waivers:
  - id: WV-12
    rule: REV-APR-001
    scope:
      subject: ASM-EDGE
      environment: production
    owner: team-platform
    reason: Review board meets after the release
    issue: PLAT-77
    expires: 2027-01-31
    approver:
      identity: alice@example.com
      authority: exception-board
    compensatingEvidence:
      - path: docs/mitigation.md
        sha256: sha256:0000000000000000000000000000000000000000000000000000000000000002
```

| Field | Type | Description |
|---|---|---|
| `waivers` | array | The waivers. A missing or null `waivers` key means none. |
| `id` | string, required | Unique ID. `waiver add` assigns `WV-<n>`, where `n` is one more than the highest trailing number in the file. A duplicate gives `CO-EXC-004`. |
| `rule` | string, required | Rule ID. A waiver covers findings of this rule only. |
| `finding` | string | Fingerprint, `sha256:<64 hex>`. A finding waiver matches a finding only when both `finding` and `rule` are equal. Exactly one of `finding` and `scope` is required. |
| `scope` | object | The scope of a scoped waiver, with at least one of `subject`, `environment`, and `obligation`. |
| `scope.subject` | string | The CALM ID that the finding is about. |
| `scope.environment` | string | The environment of the finding. |
| `scope.obligation` | string | A full obligation ID. |
| `owner` | string, required | Person or team that owns the waiver. |
| `reason` | string, required | Why the finding is accepted. |
| `issue` | string, optional | Tracker reference. |
| `expires` | string, required | Date in `YYYY-MM-DD`. |
| `approver` | object, required for a scoped waiver | `identity` and `authority` (both strings): who accepted the waiver, and the id of the authority of role `exception` that lists them. |
| `compensatingEvidence` | array, optional | Scoped waiver only. Each item has `path` (relative to the workspace root) and `sha256` (`sha256:<64 hex>`). |

An entry with both `finding` and `scope`, or with neither, is not valid. A scoped waiver is checked more strictly than a finding waiver:

- It accepts only the keys in the table. An unknown key in a scoped waiver is not valid. A finding waiver ignores unknown keys, so registers of earlier versions stay valid.
- A `scope` value must not be a wildcard (`*` or `?`), and a scope with no key is not valid.
- It needs an `approver` with `identity` and `authority`.
- With `exceptions.maxExpiryDays` set, `expires` must not be further away than that many days (see [Workspace reference](workspace.md)). The limit does not apply to a finding waiver.

Any other shape gives `CO-EXC-002`, exit code 2. There is no JSON schema for the register.

A waiver is active while the clock is before 00:00 UTC on the day after `expires`. It is therefore active for the whole `expires` day.

## Approver verification

A scoped waiver counts only if Concordia verifies its approver with the same mechanisms and the same trust anchor as assessment approvals. The authority of the approver must have the role `exception` and must be configured under `assessments.authorities`, separate from the authorities of role `reviewer`. Concordia reads the authority from the workspace file at the trust anchor, so a candidate cannot add itself.

| Mechanism of the authority | What Concordia checks |
|---|---|
| `protected-review` | The register at the trust anchor contains the waiver with an equal canonical entry, and the approver is listed in `reviewers` of the authority. A waiver that a candidate adds is therefore not applied until it is merged into the anchor. |
| `signed` | The canonical JSON of the entry has a signature from a signer of the authority, in `signatures/<id>.sigstore.json` beside the register (by default `exceptions/signatures/<id>.sigstore.json`). |

A waiver that is not verified is not applied: its findings keep their disposition, and the warning `CO-EXC-009` names the waiver, the approver, and the reason (`not-on-anchor`, `reviewer-not-listed`, `authority-not-configured`, `authority-mismatch`, `not-signed`, `signer-not-listed`, `digest-mismatch`, `trust-anchor-unreadable`, or `verification-unavailable`). A waiver for the rule `REV-GOV-001` needs an approver in an authority of role `governance`.

## Compensating evidence

Each `compensatingEvidence` reference is checked against the file in the workspace. The results list each reference as `resolved`, `unresolved` (the file cannot be read), or `digest-mismatch`. An unresolved reference or a different digest gives the warning `CO-EXC-011`. Compensating evidence never changes a verdict and does not stop the waiver from applying.

## Dispositions

Each finding gets exactly one disposition. The first matching rule applies:

| Order | Condition | Disposition | `dispositionRef` |
|---|---|---|---|
| 1 | A waiver matches and is active | `waived` | the waiver ID |
| 2 | A waiver matches and has expired | `expired-waiver` (also raises `CO-EXC-006`) | the waiver ID |
| 3 | The fingerprint is in the baseline | `baselined` | `baseline` |
| 4 | none of the above | `active` | `null` |

A finding waiver matches by fingerprint and rule. A scoped waiver matches by rule and each scope key that it lists (`subject`, `environment`, `obligation`), and a finding of another subject, environment, or obligation is not covered: a waiver for `staging` does not cover `production`. When several waivers match, an active waiver wins, then the later `expires`, then the smaller ID. Only a scoped waiver with a verified approver takes part.

Dispositions never remove a finding from the results. A waiver changes the disposition and never the verdict: a waived `violation` keeps the verdict `violation`, a waived `unknown` keeps the verdict `unknown`, and a waiver does not change the currency of an assessment. A waiver also never removes exit code 4: an `unknown` result of a rule with `onUnknown: fail` keeps the exit code 4 when every unknown obligation is waived. Supply the missing evidence to resolve an unknown. After `expires`, the findings have the disposition `expired-waiver`, count as live, and keep their verdict. How dispositions affect the exit code is described in [Results reference](results.md).

Two bookkeeping lists come out of the same step:

- **Resolved baseline entries**: baseline entries for rules that ran, whose fingerprint no longer appears in the findings. `baseline update --prune-resolved` removes them.
- **Orphaned waivers**: waivers that matched no finding. A finding waiver raises the warning `CO-EXC-003`. A scoped waiver raises the warning `CO-EXC-010` and stays in the register.

## Related diagnostics

`CO-EXC-001` to `CO-EXC-004`, `CO-EXC-006` to `CO-EXC-011`: see [Diagnostics reference](diagnostics.md). `CO-EXC-005` is reserved.

## Next steps

- [Manage baselines and waivers](../how-to/manage-baselines-and-waivers.md)
- [CLI reference: `baseline` and `waiver`](cli.md)
- [Results reference](results.md)
