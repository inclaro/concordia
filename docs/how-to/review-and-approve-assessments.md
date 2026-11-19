# How to review and approve assessments

This guide shows you how to give an assessment an approval that Concordia counts, how to revoke it, and how to read the result when Concordia does not count a record. An approval is a record file. Concordia verifies who made it with one of two mechanisms, `protected-review` or `signed`, and it never counts repository text that says "reviewed". The example is a security assessment, `ASM-EDGE`, of the public entry points of `api-gateway` for `production`.

## Prerequisites

- A workspace that passes `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- An assessment version, validated offline with `concordia assessment validate`. See the [assessments reference](../reference/assessments.md).
- A Git repository with a protected branch that holds the trust anchor, for example `origin/main`. Concordia reads the authority configuration and, for `protected-review`, the records from that branch.
- An index of the project (`concordia index`). `assessment approve` computes the current dependency items from the indexed evidence.
- For the `signed` mechanism: `cosign` on `PATH` (or `COSIGN` set to its path).

## Choose a mechanism

| | `protected-review` (default) | `signed` |
|---|---|---|
| Who proves the authority | The protection of the anchor branch: only reviewers merge to it. | A cosign signature of a signer that the authority lists. |
| Where the record lives | On the trust anchor. A record that exists only in the candidate is not applied. | Anywhere, also in the candidate change. The signature proves it. |
| Needs | Git only. Works offline. | `cosign` and network access: cosign 3 writes signatures to the public transparency log and fetches the Sigstore trusted root to verify any signature, keyless or with a `publicKey`. |
| Number of merges | Two: the record to the anchor, then the candidate on top of it. | One: the record and its signature travel with the candidate. |

Use `protected-review` unless a change must carry its own approval, or CI cannot read the anchor branch.

## What Concordia verifies, and what it does not

Concordia verifies **where a record is and what it binds**. It reads local Git data and recomputes the digests. It does not call a forge API and it does not check forge settings: branch protection, `CODEOWNERS`, and required reviewers are the forge's job. A `protected-review` approval is as strong as the protection of the anchor branch. If anyone can push to the anchor, anyone can approve.

For every record, Concordia takes the authority configuration from the trust anchor and not from your working tree. A candidate that adds itself to `reviewers` or adds a signer does not approve itself: the change is reported as a governance change and the record is not applied.

**Trust model.** Concordia is run by trusted people and in trusted pipelines. The trust anchor protects against honest mistakes, such as an approval that was never merged to the protected branch or a reviewer list edited in a candidate, not against a deliberately hostile change. In particular, the anchor reference (`assessments.trustAnchor.ref`), the `assessments.trustAnchor` key itself, and `check.rollback` are read from the workspace file of the run. A candidate that points the anchor at its own branch, removes the anchor (the governance rule is then only `unknown`), or adds a rollback (review rules then run as `shadow`) is not stopped by Concordia. Keep the workspace file under the same review as the rest of the change.

## Configure an authority (protected-review)

1. Add the authority, the trust anchor, and the required assessment to `concordia.yaml`, and merge the file to the anchor. Concordia reads this key from the anchor, so a change counts only after it is there.

   ```yaml
   assessments:
     paths: [assessments]
     trustAnchor: { ref: origin/main }
     authorities:
       - id: security-review
         role: reviewer
         mechanism: protected-review
         reviewers: [alice@example.com, bob@example.com]
         paths: [assessments/approvals/**, assessments/revocations/**]
     required:
       - { id: edge-exposure, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-gateway] }, environment: production, authority: security-review }
   ```

   `reviewers` lists the identities that may decide. `paths` lists the globs of the record files that this authority accepts; include the revocation directory if reviewers revoke. See [workspace: assessments](../reference/workspace.md#assessments).

2. Set the review rules to `advisory` while you roll out, then `mandatory` when the flow works. See [roll out new obligations](roll-out-new-obligations.md). A `mandatory` rule needs an authority and a trust anchor (`CO-ASM-004`).

## The two-step protected-review flow

1. **Look at the subject.** On the candidate branch (the branch with the code and the assessment file), the reviewer runs:

   ```bash
   concordia assessment approve ASM-EDGE --reviewer alice@example.com
   ```

   Without `--confirm-subject` the command prints the subject and writes nothing. It exits 2 with `CO-ASM-005`. The output lists the assessment, the dependency items with their digests, the number of assumptions and evidence items, the digest contract, the capability profile, and the **subject digest**. Read it. The digest is what you approve.

   If a dependency item cannot be computed (no evidence for its kind, a partial scan), the command refuses and names the reason. Fix the evidence first; you cannot approve what Concordia cannot see.

2. **Write the record.** Run the command again with the digest that you examined:

   ```bash
   concordia assessment approve ASM-EDGE --reviewer alice@example.com --confirm-subject sha256:<digest>
   ```

   The command writes one file, `assessments/approvals/ASM-EDGE-v1-1.yaml`, and prints its digest and the next step. It takes the authority from the required assessment (`--authority` overrides it) and never overwrites a record: a second decision on the same version is written to the next number, `ASM-EDGE-v1-2.yaml`, and `--path` that names an existing file is refused. `--expires 2027-01-31` makes the approval end at the start of the next day (UTC); `--condition "<text>"` adds an expiry condition in words; `--decision rejected` records a rejection. If the subject changed between the two runs, the digest does not match and nothing is written.

3. **Put the record on the anchor.** Open a small pull request against the anchor branch that holds the assessment version file and the approval record, and nothing else:

   ```bash
   git switch -c approve-asm-edge origin/main
   git checkout candidate -- assessments/ASM-EDGE/v1.yaml assessments/approvals/ASM-EDGE-v1-1.yaml
   git commit -m "docs: approve ASM-EDGE v1"
   ```

   Concordia loads the approval together with the assessment version that it binds, so the version file goes with it. The forge's branch protection decides who may merge this pull request; that is the review.

4. **Rebuild the candidate on the anchor.** Rebase or merge the candidate onto the updated anchor and run `concordia check`. The approval is now on the trust anchor, names a listed reviewer, and binds the digests of the current subject, so the required assessment is `current` and `REV-APR-001` passes. Before step 3 the same check shows the approval as not applied (`CO-ASM-006`, "does not exist at the trust anchor") and the gate stays as it was.

## The signed flow

1. Configure a signed authority and merge it to the anchor:

   ```yaml
   assessments:
     trustAnchor: { ref: origin/main }
     authorities:
       - id: platform-signers
         role: reviewer
         mechanism: signed
         signers:
           - { identity: alice@example.com, issuer: "https://accounts.example.com" }
           # or a key that is committed to the anchor:
           # - { publicKey: keys/review.pub }
     required:
       - { id: edge-exposure, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-gateway] }, environment: production, authority: platform-signers }
   ```

   A `publicKey` is a path that Concordia reads **from the trust anchor**, so a candidate cannot replace the key. A key reference with a scheme (for example `awskms://...`) is passed to cosign as it is.

2. Print the subject and confirm it as above, and add `--sign` (keyless) or `--sign-key <key>`:

   ```bash
   concordia assessment approve ASM-EDGE --reviewer alice@example.com \
     --confirm-subject sha256:<digest> --sign
   ```

   The command runs `cosign sign-blob --yes --bundle <bundle> [--key <key>]` over the **canonical JSON of the record** (not the file bytes, so formatting does not matter) and writes two files: the record and its bundle, `assessments/approvals/ASM-EDGE-v1-1.yaml.sigstore.json`. Signing needs network access, also with `--sign-key`, because cosign 3 records every signature in the public transparency log; with `--offline` the command stops with `CO-WRK-016` (exit 6) before cosign runs. A missing `cosign` is `CO-WRK-014` (exit 5); a `COSIGN` path that cannot be started is `CO-REP-003` (exit 5).

3. Commit the record and the bundle together, in the candidate change. No second merge is needed. `concordia check` runs `cosign verify-blob --bundle <bundle> --certificate-identity <identity> --certificate-oidc-issuer <issuer> <canonical record>` (or `--key <key>`) for each signer of the authority until one verifies. For a keyless signer the record's `reviewer.identity` must equal the signer identity.

A record whose signature does not verify, or whose signer the authority does not list, is not applied (`CO-ASM-006`). A signed record checked with `--offline`, keyless or with a `publicKey` signer, is `unknown` with the reason `verification-unavailable` and `CO-ASM-011` (a warning, never exit 6): Concordia does not start cosign for it, because cosign 3 fetches the Sigstore trusted root to verify any signature. Use a `protected-review` authority for offline CI.

### Cosign flags

These are the flags that Concordia passes, shared with `concordia export verify --verify-signature`:

| Step | Command |
|---|---|
| Sign | `cosign sign-blob --yes --bundle <bundle> [--key <key>] <blob>` |
| Verify, keyless | `cosign verify-blob --bundle <bundle> --certificate-identity <identity> --certificate-oidc-issuer <issuer> <blob>` |
| Verify, key | `cosign verify-blob --bundle <bundle> --key <key> <blob>` |

The flags are confirmed against the cosign release that the repository pins (`cosign_version` in `mise.toml`, installed into `.bin/` by `mise run tools:install`): a key-signed record verifies with these exact commands. cosign 3 also uploads every signature, keyed or keyless, to the public transparency log, and checks a signature against the Sigstore trusted root, which it fetches over the network on each run, even with a warm cache. That is why signing never runs with `--offline` and a signed record is `unknown` (`verification-unavailable`) when checked with `--offline`. `concordia export verify` can verify offline with `--trusted-root` and a Sigstore trusted root file; record verification has no such option, so use `protected-review` for offline CI.

## Approve a quality assessment

A scenario in `reviewed` mode decides from the approval of a `quality` assessment. The assessment names the scenario in `subject.scenarios`, and each scenario is a dependency of kind `quality-scenario`. A security assessment never reviews a scenario. The flow is the same as above:

```yaml
apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-QUAL-CHECKOUT
version: 1
type: quality
scope:
  calmNodes: [api-gateway]
environment: production
subject:
  summary: Checkout latency design review
  scenarios: [checkout-latency]
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
```

1. Declare the scenario in a `QualityScenarioSet` with `mode: reviewed` and a `criterion` that has a `subject` and an `authority`, under `assurance.quality.scenarios`. See [declare quality scenarios](add-stronger-evidence.md#declare-quality-scenarios).
2. List the assessment under `assessments.required`, with `type: quality`.
3. Run `concordia assessment approve ASM-QUAL-CHECKOUT --reviewer alice@example.com`, check the subject, and confirm it as in [the two-step flow](#the-two-step-protected-review-flow).

The approval binds the digest of the scenario definition and of its response measure. A changed definition or criterion makes the assessment `review-required`. Moving a scenario inside its file changes nothing. The verdict of the scenario follows the outcome of the approved assessment; the mapping is in the [assurance reference](../reference/assurance.md#quality).

A `verification` dependency works the same way: it depends on the outcome and the statement digest of the admissible check results of a check in an environment. A new outcome makes the approval `review-required`. Both kinds need the `assurance` key. Without it, the dependency is `unknown` with the reason `unsupported-family`. See the [dependency kinds](../reference/assessments.md).

## Revoke an approval

A revocation names an approval by id and digest. It counts through the same mechanism as the approval.

```bash
concordia assessment revoke ASM-EDGE-v1-1 --reviewer alice@example.com --reason "gateway replaced"
concordia assessment revoke ASM-EDGE-v1-1 --reviewer alice@example.com --reason "gateway replaced" \
  --confirm-subject sha256:<approval digest>
```

The first run prints the approval and its digest and writes nothing. The second writes `assessments/revocations/ASM-EDGE-v1-1.yaml`. With `protected-review`, put that file on the anchor like an approval; once it is there, the assessment is `revoked` even if no other file changed. `--sign` and `--sign-key` work as for `approve`.

## What the commands refuse

| Situation | Result |
|---|---|
| `CI` is set to a true value | `CO-ASM-005`, exit 2, nothing written. A reviewer decides, a pipeline does not. |
| No `--reviewer` | `CO-ASM-005`, exit 2. |
| No `--confirm-subject`, or a digest that differs from the current subject | `CO-ASM-005`, exit 2; the subject and its digest are printed. |
| A subject that cannot be computed | `CO-ASM-005`, exit 2; the reasons are printed. |
| `--path` names a file that exists | `CO-ASM-005`, exit 2. A record is never overwritten. |
| `cosign` missing with `--sign` | `CO-WRK-014`, exit 5 (`CO-REP-003`, exit 5, when `COSIGN` names a file that cannot be started). |

The commands write the record file, and with `--sign` its bundle, and nothing else. They never edit an assessment, an assumption, or the workspace.

## When a record is not counted

`concordia check` and `concordia assessment status` show every record that failed verification with its reason. The record does not count and the obligation is not satisfied.

| Reason | Meaning | Fix |
|---|---|---|
| `not-on-anchor` | `protected-review`: the file is not at the trust anchor revision. | Merge the record to the anchor (step 3). |
| `path-not-allowed` | The path matches no glob of the authority's `paths`. | Move the record or add the glob on the anchor. |
| `reviewer-not-listed` | The reviewer is not in `reviewers` as the anchor lists them. | Add the reviewer on the anchor. |
| `digest-mismatch` | The candidate's file differs from its copy on the anchor. | Restore the file, or merge the new version. |
| `authority-not-configured`, `authority-mismatch` | The authority does not exist on the anchor, or has another role or mechanism. | Fix the record or the authority on the anchor. |
| `not-signed` | `signed`: no `<record>.sigstore.json` beside the record. | Run `approve --sign`. |
| `signer-not-listed` | No signer of the authority signed the record. | Sign with a listed signer. |

Two situations make the obligation `unknown`, with a closed reason, and a `mandatory` gate then blocks (exit 4):

- `trust-anchor-unreadable` (`CO-ASM-007`): the anchor ref is missing (a shallow clone), is not an ancestor of `HEAD`, or has no `concordia.yaml`. The message names the ref and the fix. In a shallow clone, `git fetch --depth=<n> origin main:refs/remotes/origin/main` creates `origin/main` (a plain `git fetch origin main` does not, because a shallow clone tracks only its own branch), and `git fetch --deepen=<n>` makes the anchor an ancestor of `HEAD`.
- `verification-unavailable` (`CO-ASM-011`): a signature cannot be verified here, for example any signed record with `--offline`.

An approval stops being current when its subject changes, when it expires, or when a verified revocation exists. See [assessments](../reference/assessments.md) and [resolve unknown verdicts](resolve-unknown-verdicts.md).
