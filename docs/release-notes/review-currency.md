# Review currency release notes

This release adds assessments, approvals, and a currency check, so a team can tell whether an earlier review still covers the system after a change. It also signs the release artifacts with cosign. This page lists what a user can see change.

## Trust context

- The `trust` workspace key declares extra authority dimensions and the allowed data classifications. The built-in dimensions are `organization`, `account`, `principal`, `zone`, and `region`. A missing classification stays `unknown`. A value that is not declared is `CO-SEC-001` (exit 2).
- Concordia derives boundary crossings for flows with direction, and lists a closed set of dependency kinds that an assessment can depend on. A kind that the capability profile does not support is the warning `CO-SEC-002`.
- `results.json` gains a `trust` object. See [Trust context](../reference/trust-context.md).

## Assessments, approvals, and revocations

- An assessment and an assumption are immutable, versioned documents. An approval and a revocation are separate records. A record binds the subject digest, the dependency digests, the scope, the environment, the digest-contract version, the reviewer authority, the decision time, and the expiry. See [Assessments](../reference/assessments.md).
- The workspace key `assessments` has `paths`, `trustAnchor`, `authorities[]`, `required[]`, and `analysisLimits`. An authority has a `role` (`reviewer`, `exception`, or `governance`) and a `mechanism`.
- `protected-review` is the default mechanism. The record must exist at the trust anchor revision, which is read from git. `signed` needs a cosign signature of a listed signer. Text in a document, such as `reviewed: true`, never counts.
- `concordia assessment validate`, `status`, `approve`, and `revoke` are new. `approve` and `revoke` refuse to run in CI (`CO-ASM-005`) and write one record file, plus a signature bundle with `--sign`. `validate` and `status` never write. See [assessment commands](../reference/cli.md#assessment-validate) and [Review and approve assessments](../how-to/review-and-approve-assessments.md).
- New diagnostics `CO-ASM-001` to `CO-ASM-011`.

## Currency states

`check` evaluates every assessment version on every run: `current`, `review-required`, `expired`, `revoked`, or `unknown`. Expiry and revocation are judged at each evaluation. Unknown currency blocks a mandatory review gate.

- Four reasons for an `unknown` result join the closed set: `target-not-declared`, `trust-anchor-unreadable`, `verification-unavailable`, and `reference-unresolved`.
- Change impact compares the candidate with the snapshot that the approval covers, uses the union of the baseline and candidate scopes, keeps removed subjects until a recorded disposition, and reports the shortest explanation path. A change of policy, trust authority, evaluator, normalization, or digest contract can invalidate a review. A change that relaxes a review rule needs a governance disposition. See [Currency](../reference/assessments.md#currency).
- `check --changed` never reduces assessment currency.
- `check --incremental` reuses the review evaluation of an earlier run from `.concordia/cache/impact/`. The result equals a full evaluation. A cache that cannot be used gives `CO-ASM-010` and a full evaluation. It is the only option that writes below `.concordia/cache/`. See [Review currency](../explanation/review-currency.md).

## The `review-currency` pack

Built-in pack `review-currency`, version `1.0.0`. Add `- use: builtin:review-currency` to `policies.packs`.

| Rule | What it checks |
|---|---|
| `REV-APR-001` | A required assessment has a current verified approval. |
| `REV-DSP-001` | A removed or narrowed subject has a recorded disposition. |
| `REV-CAP-001` | A lost capability has a recorded disposition. |
| `REV-GOV-001` | A relaxation of review rules has a governance disposition. |

- The pack is opt-in and advisory by default. Set a mode per rule, pack, or family (`assessment-currency`) with `check.enforcement`. There is no second enforcement key.
- Mandatory review rules need an authority and a trust anchor, or `check` stops with `CO-ASM-004` (exit 2).
- An active `check.rollback` runs these rules as `shadow`.

## Scoped waivers

A waiver has a required `rule` and exactly one of `finding` or `scope`. A `scope` has at least one of `subject`, `environment`, and `obligation`, and needs an `approver` that an authority of role `exception` lists and that Concordia can verify. A waiver changes the disposition to `waived`. It never turns the verdict into `pass` and never removes exit code 4 for an unknown obligation. A scoped waiver accepts only its documented keys. The workspace key `exceptions.maxExpiryDays` limits the expiry. See [Exceptions reference](../reference/exceptions.md#scoped-waiver).

## Results and output

- New top-level objects in `results.json`: `assessmentCurrency` (the area), `governance`, `analysis`, and `triggers[]`. The input lists `inputs.assessments` and `inputs.approvals` are new. `schemaVersion` stays `1.1`. A run without an `assessments` key has none of them. See [Assessment currency](../reference/results.md#assessment-currency).
- A review finding has the next action `get review`, or `supply evidence` and `fix analysis configuration` for `unknown`.
- SARIF has results for review obligations. The evaluation bundle has assessment versions, approvals, and exception authorizations.
- A view `assessmentCurrency` entry lists the matching assessments. See the [view currency release notes](view-currency.md).

## Signed releases

Release artifacts are now signed.

- GoReleaser signs the checksum file keyless with cosign, with the GitHub OIDC identity of the release workflow (Sigstore). Releases need no repository secrets and no key file.
- GoReleaser publishes the release as a draft. The workflow verifies the keyless signature and only then makes the release public.
- To verify a download, run `cosign verify-blob --bundle concordia_checksums.txt.sigstore.json --certificate-identity https://github.com/inclaro/concordia/.github/workflows/release.yml@refs/tags/<tag> --certificate-oidc-issuer https://token.actions.githubusercontent.com concordia_checksums.txt`.

## Offline signing and verification

cosign 3 records every signature in the public transparency log and fetches the Sigstore trusted root over the network. This changes what works with `--offline`:

- Signing under `--offline` is `CO-WRK-016` (exit 6), with or without a key. This applies to `assessment approve --sign`, `assessment revoke --sign`, and `export bundle --sign-key`.
- A signed approval under `--offline` cannot be verified. Its currency is `unknown` with the reason `verification-unavailable`, and `CO-ASM-011` is a warning.
- `concordia export verify --verify-signature --trusted-root <file>` verifies offline with a Sigstore trusted root file. Without `--trusted-root`, `--offline` gives `CO-WRK-016`.
- A missing `cosign` is `CO-WRK-014` (exit 5).

## What needs your action

- To gate on reviews, declare `assessments` with an authority and a trust anchor, write the assessments, and get approvals recorded. Then enable the pack and choose a mode.
- Replace a waiver `rule` that contains a wildcard (`*` or `?`) with the exact rule ID: a waiver now names one rule.
- For signed approvals in an offline pipeline, provide a trusted root as described above.
