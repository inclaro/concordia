# How to manage baselines and waivers

This guide shows you how to accept existing findings in a baseline, handle new findings, and grant time-limited waivers, including waivers for a scope of findings with a verified approver.

## Prerequisites

- A workspace with a current graph and a working `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- `jq`, to read fingerprints from `results.json` (optional).

For the concepts, see [fingerprints, baselines, and waivers](../explanation/fingerprints-baselines-waivers.md). For file formats, see [exceptions](../reference/exceptions.md).

## Create a baseline

1. Index and check, so that `build/concordia/results.json` is current:

   ```bash
   concordia index
   concordia check
   ```

2. Create the baseline:

   ```bash
   concordia baseline create
   ```

   The file goes to `exceptions.baseline` (default `exceptions/concordia.baseline.json`). Each entry has the owner `unassigned` and the reason `Inherited before enforcement`. Waived findings are not included.

   | Option | Effect |
   |---|---|
   | `--severity <list>` | Severities to include. Default `warning,error`. |
   | `--expires <date>` | Expiry date for every entry. |
   | `--output <path>` | Write to another file. |
   | `--from <results.json>` | Use an existing results file instead of `build/concordia/results.json`. |
   | `--force` | Overwrite an existing baseline. Without it, the command fails with exit code 2. |

3. Commit the baseline file.

## Update the baseline

Remove entries for findings that no longer exist:

```bash
concordia baseline update --prune-resolved
```

Add a current `active` finding on purpose:

```bash
concordia baseline update \
  --accept sha256:<fingerprint> \
  --reason "Legacy module, removal planned" \
  --owner team-billing
```

`--accept` needs the full fingerprint, `--reason`, and `--owner`. The fingerprint must belong to a current `active` finding.

> **Note:** When the `CI` environment variable is set (and is not empty, `0`, or `false`), `--accept` fails with `CO-EXC-008` and exit code 2. New findings never enter the baseline in CI. Run the command on your own machine and commit the result.

## Find a fingerprint

```bash
jq -r '.findings[] | select(.disposition=="active") | "\(.fingerprint) \(.ruleId) \(.location.path)"' build/concordia/results.json
```

To look at one finding, pass the fingerprint, or a unique prefix, to `explain`:

```bash
concordia explain CO-BND-001 --finding sha256:<prefix>
```

Add `--source` to print the source lines around each location.

## Add a waiver

A waiver accepts one finding until a date, and records who owns it.

```bash
concordia waiver add \
  --finding sha256:<fingerprint> \
  --owner team-billing \
  --reason "Refund migration in progress" \
  --expires 2026-12-31 \
  --issue BILL-123
```

| Option | Required | Description |
|---|---|---|
| `--finding` | yes | Full fingerprint (`sha256:` and 64 hex digits). |
| `--owner` | yes | Owner of the waiver. |
| `--reason` | yes | Why the finding is accepted. |
| `--expires` | yes | Expiry date, `YYYY-MM-DD`, in the future. |
| `--issue` | no | Tracker ID. |
| `--rule` | no | Rule ID. When you omit it, the command reads it from `build/concordia/results.json`, so a prior `check` must exist. |

The command appends the waiver to `exceptions.waivers` (default `exceptions/waivers.yaml`) with the next ID, `WV-1`, `WV-2`, and so on. A waiver is active through the whole `expires` day, in UTC. After that day, the finding has the disposition `expired-waiver` and fails the check again.

## Add a scoped waiver

A scoped waiver covers the findings of one rule for a subject, an environment, an obligation, or a mix of them. It names an approver, and it does not depend on fingerprints. Use it for review-currency rules (`REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`), where the finding changes while the exception stays.

```bash
concordia waiver add \
  --rule REV-APR-001 \
  --subject ASM-EDGE \
  --environment production \
  --approver alice@example.com@exception-board \
  --owner team-platform \
  --reason "Review board meets after the release" \
  --expires 2027-01-31 \
  --evidence docs/mitigation.md
```

| Option | Required | Description |
|---|---|---|
| `--rule` | yes | Rule ID. |
| `--subject` | one of the three | CALM ID that the finding is about. |
| `--environment` | one of the three | Environment of the finding. |
| `--obligation` | one of the three | Full obligation ID. |
| `--approver` | yes | `identity@authority`. The authority is the id of an authority of role `exception`. The text after the last `@` is the authority. |
| `--evidence` | no | A file in the workspace, as a path relative to the workspace root. Repeat the option for more files. The command records the SHA-256 digest of each file. |
| `--owner`, `--reason`, `--expires`, `--issue` | as for a finding waiver | Same meaning. |

Any of `--subject`, `--environment`, and `--obligation` writes a scoped waiver. `--finding` together with one of them is rejected with `CO-EXC-002`, and so is a wildcard in a scope value. When `exceptions.maxExpiryDays` is set, `--expires` must be within that many days.

Then get the approver verified:

1. Make sure the workspace file at the trust anchor lists an authority with the role `exception`, and that it lists the approver (`reviewers` for `protected-review`, `signers` for `signed`).
2. For `protected-review`, merge the register with the waiver into the trust anchor branch through the protected review. A waiver that exists only in your change is not applied, and the warning `CO-EXC-009` says that it is not on the trust anchor.
3. For `signed`, put the signature of the canonical entry in `signatures/<id>.sigstore.json` beside the register (by default `exceptions/signatures/<id>.sigstore.json`). See [exceptions](../reference/exceptions.md).

A waiver for the rule `REV-GOV-001` needs an authority of role `governance`. A candidate cannot waive its own relaxation.

Look at the result in `build/concordia/results.json`, under `exceptions.scopedWaivers`: each scoped waiver has its scope, approver, verification result, expiry, evidence state, and the fingerprints of the findings that it covered. A scoped waiver that covers no current finding stays in the register and raises `CO-EXC-010`.

> **Note:** A waiver changes the disposition of a finding to `waived`. It does not change the verdict, it does not change the currency of an assessment, and it never removes exit code 4. If a rule with `onUnknown: fail` has an `unknown` result, the run still exits with code 4 when every unknown obligation is waived. Supply the missing evidence instead.

## Validate waivers

```bash
concordia waiver validate
```

| Option | Effect |
|---|---|
| `--at <date>` | Evaluate at this date or time instead of now. |
| `--fail-expiring-within <days>` | Treat waivers that expire within N days as errors (`CO-EXC-007`). |

The command checks the shape of every entry: for a scoped waiver the scope, the approver, the expiry, and the maximum duration (`exceptions.maxExpiryDays`). An invalid entry gives `CO-EXC-002` and exit code 2. An expired waiver (`CO-EXC-006`) is an error here, and the command exits with code 2. A waiver that matches no current finding gives the warning `CO-EXC-003`, or `CO-EXC-010` for a scoped waiver.

To test expiry in a script, set `CONCORDIA_NOW=2027-01-01T00:00:00Z` for the run.

## Confirm the result

Run `concordia check`. A baselined finding has the disposition `baselined`, a waived finding has `waived`, and only `active` and `expired-waiver` findings cause exit code 1. Exit code 4 for an `unknown` result is not affected by a waiver.

## Next steps

- [Brownfield tutorial](../tutorials/brownfield-adoption.md): a worked example.
- [Run in CI](run-in-ci.md)
- [CLI reference](../reference/cli.md)
