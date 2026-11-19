# Use policy packs

This guide shows you how to choose and enable the packs that ship with Concordia, change single rules without a copy, vendor a pack from the community, keep it pinned, and write a pack that others can use.

## Prerequisites

- A [workspace](set-up-a-workspace.md) (`concordia.yaml`).
- For the core pack: CALM nodes that carry the [tags](../reference/core-rules.md#tag-conventions) the rules need, and [bindings](bind-code-to-architecture.md) for those nodes.

## How packs work

A policy pack is a YAML file with rules (see [policies reference](../reference/policies.md)). The workspace gets packs from two places:

- `policies.files`: your own rule files. This is the default way.
- `policies.packs`: packs from the binary (`builtin:<name>`) and vendored files that a `sha256` digest pins.

The `check` command never uses the network. Only `concordia policy add` can fetch a URL.

To see what is available and what the workspace uses, run:

```bash
concordia policy list --rules
```

This command needs no CBM and no index.

## Use the core pack

The core pack has rules for dependency cycles, undeclared dependencies, direct database access, imports between services, layers, trust boundaries, and drift. See [core rules](../reference/core-rules.md) for each rule.

1. Add the pack to the workspace:

   ```bash
   concordia policy add builtin:core
   ```

   The command adds `- use: builtin:core` to `policies.packs`. It keeps your comments and other keys. You can also edit the file by hand. For a new workspace, use `concordia init --preset core`.

2. Tag your CALM nodes. Most core rules choose nodes by tag. A rule does nothing until the model has its tags. This is intended: on a model without tags, these rules have no subject and their verdict is `not-applicable`. See [tag conventions](../reference/core-rules.md#tag-conventions).

3. Run the check:

   ```bash
   concordia check
   ```

The rule IDs start with `CORE-`. Use `concordia explain CORE-LYR-001` to see the rule, the pack it came from, and the evidence.

## Choose a built-in pack

`concordia policy list` prints the packs in the binary. Each pack except `core` is opt-in, needs a workspace key, and has a limit on its enforcement mode. Enable a pack with `concordia policy add builtin:<name>`, or add `- use: builtin:<name>` to `policies.packs`.

| Pack | Rules | Use it to | Needs in the workspace | Enforcement |
|---|---|---|---|---|
| `core` | `CORE-*` | Check dependency cycles, layers, trust boundaries, and drift. | CALM tags and [bindings](bind-code-to-architecture.md). | Default `mandatory`. |
| `infrastructure` | `INFRA-PLC-001`, `INFRA-PLC-002`, `INFRA-TRC-001` to `INFRA-TRC-004` | Check that workloads run where the model says, and that each deployable has an image and a build. | `environments`, bindings for workloads and images, the tag `deployable`. See [trace a deployment](trace-a-deployment.md). | Default `advisory`. `mandatory` stops `check` with `CO-POL-018`, because the families have the maturity `advisory`. |
| `adr-governance` | `GOV-ADR-001` to `GOV-ADR-006` | Check that decisions are committed, active, and enforced. | The `adr` key with a register. See [govern architecture decisions](govern-architecture-decisions.md). | Default `mandatory`. Roll out with [`check.enforcement`](roll-out-new-obligations.md). |
| `review-currency` | `REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, `REV-GOV-001` | Check that required assessments have a current verified approval. | The `assessments` key, with authorities and required assessments. See [review and approve assessments](review-and-approve-assessments.md). | Default `advisory`. `mandatory` needs one authority and a trust anchor (`CO-ASM-004`) and the guarantees of `CO-POL-018`. |
| `assurance` | `ASR-REN-001`, `ASR-PRV-001`, `ASR-PRV-002` | Check rendered manifests, exact lineage, and runtime drift. | The `assurance` key. See [add stronger evidence](add-stronger-evidence.md). | Default `advisory`. `mandatory` stops `check` with `CO-POL-018`. |

Without the input that a pack needs, its rules are `not-applicable` or `unknown`, never a pass. For example, `adr-governance` without a register gives `not-applicable`, and `assurance` without the `assurance` key gives `unknown` with the reason `claim-not-enabled`. A typed rule that runs as `advisory` shows in the report and does not change the exit code. Set a mode per rule, pack, or family with `check.enforcement`.

The pack `views` is not in the table: `check` applies it by itself when the workspace declares `views`, and `policy add builtin:views` and a `policies.packs` entry for it are refused with `CO-POL-001`. See [keep diagrams current](keep-diagrams-current.md).

Each pack has its own rule reference: [core rules](../reference/core-rules.md), [infrastructure rules](../reference/infrastructure-rules.md), [ADR governance](../reference/adr-governance.md#obligations), [policies reference for review currency](../reference/policies.md#review-currency-rules-the-assessmentcurrency-key), and [assurance rules](../reference/assurance-rules.md). You can combine packs, and the `rules` overrides below work for each one.

## Change a rule without a copy

Add `rules` to the pack entry. The key is a rule ID of the pack:

```yaml
policies:
  packs:
    - use: builtin:core
      rules:
        CORE-DECL-001: { severity: warning }
        CORE-SEC-001: { enabled: false }
        CORE-CYC-001: { evidence: { minimumCoverage: 0.8 }, owner: team-platform }
```

| Override | Effect |
|---|---|
| `severity` | `error`, `warning`, or `info`. |
| `enabled` | `false` drops the rule. |
| `evidence` | Partial `evidence` block: `minimumConfidence`, `minimumCoverage`, `onUnknown`. The keys that you set replace the keys of the rule. |
| `owner` | The owner of the rule. |

An override for a rule ID that the pack does not have is an error (`CO-POL-015`). This finds typos. Run `concordia policy verify` after you edit the overrides.

To accept existing violations of a rule, do not turn it off. Use a [baseline or a waiver](manage-baselines-and-waivers.md).

## Vendor a community pack

A pack that comes from outside is a local file in your repository. It is pinned by its digest. This way, a change of the pack text never reaches your build without a review.

1. Add the pack. The source is a local file or an `https://` URL:

   ```bash
   concordia policy add https://example.org/packs/acme-arch-1.2.0.policy.yaml
   ```

   The command does these steps:

   1. It downloads the file (`https` only, 30 seconds, at most 1 MiB). With `--offline` it refuses and exits with code 6.
   2. It validates the file against the pack schema.
   3. It copies the file to `policies/vendor/<metadata.id>@<metadata.version>.policy.yaml`. Use `--id <name>` to change the `<metadata.id>` part of the file name.
   4. It computes the sha256 digest and adds the entry to `policies.packs`.
   5. It prints `added policies/vendor/acme-arch@1.2.0.policy.yaml`.

   The entry looks like this:

   ```yaml
   policies:
     packs:
       - use: policies/vendor/acme-arch@1.2.0.policy.yaml
         sha256: sha256:<64 hex digits>
   ```

2. Read the vendored file. You are responsible for the rules that you add. Look at the selectors, the severities, and the edge kinds.

3. Commit the file, the workspace, and `.concordia.lock`.

A file pack must have a `sha256` (`CO-POL-013`). A `use` that is an URL is an error (`CO-POL-014`): vendor it first. The same rule ID in two packs, or in a pack and a file of `policies.files`, is an error (`CO-POL-003`). If a rule ID collides, ask the pack author to use a prefix that is unique, or do not use that pack.

The default `policies.files` glob matches `policies/vendor/`. Concordia reads a file that `policies.packs` names only once. A vendored file that is not in `policies.packs` is read as a normal policy file, without a pin. Delete the file when you remove its entry.

## Pin and verify

Concordia checks the digest every time it reads a pack file. A digest that differs from the `sha256` gives `CO-POL-010`, and the run stops with exit code 2. To check the packs without a CBM or a check run, use:

```bash
concordia policy verify
```

The command loads all packs, checks the digests, and checks that each override names a rule of its pack. It exits with 0, or with 2 and diagnostics.

`.concordia.lock` also records each pack with its `id`, `version`, `source`, and `sha256`. A pack whose digest differs from the lock gives `CO-INT-012` and exit code 6, as for a resource that `architecture.urlMapping` maps. The lock protects the pin and the built-in packs. It catches an edit of the pin in the workspace together with an edit of the file. See [workspace reference](../reference/workspace.md#concordialock).

## Update a pack

1. Add the new file. There are two cases:

   - **A new pack version has a new file name.** Add it like any pack:

     ```bash
     concordia policy add https://example.org/packs/acme-arch-1.3.0.policy.yaml
     ```

     The command adds a second entry (`acme-arch@1.3.0.policy.yaml`). Both versions declare the same rule IDs, so `check` fails with `CO-POL-003` until you remove the old entry from `policies.packs` and delete the old file. Keep the `rules` overrides: copy them to the new entry.

   - **The same version has a corrected file.** Add it again with `--force`. The command replaces the vendored file and the `sha256` of the entry. The `rules` overrides of the entry stay. It prints the old and the new digest:

     ```text
     updated policies/vendor/acme-arch@1.2.0.policy.yaml (sha256 sha256:… -> sha256:…)
     ```

     Without `--force`, the command refuses to replace an existing entry.

2. Review the change. Look at the difference of the vendored file in your version control, and at the rules that changed.

3. Run `concordia policy verify`, then `concordia check`. New rules can give new findings. A new rule that you do not want can be turned off with an override.

4. Commit the file, the workspace, and the lock.

`policy add` also updates the lock entry of the pack when `.concordia.lock` exists. If you edit a vendored file in place, run `concordia policy add <that file> --force` to pin the new digest.

A new `concordia` binary can change a built-in pack. Then `check` fails with `CO-INT-012`, because the lock has the old digest. Read the release notes. Then remove the entry of the pack (for example `builtin:core`) from `packs` in `.concordia.lock`, or run `concordia policy add builtin:core --force`. The next `check` writes the new digest.

## Write and publish your own pack

A pack is one `PolicyPack` file. Follow these rules, so that other teams can use it.

1. **Choose a unique prefix for the rule IDs.** A rule ID is `<PREFIX>-<AREA>-<NNN>`, for example `ACME-LYR-001`. The pattern is `^[A-Z][A-Z0-9]*(-[A-Z0-9]+)+$`. The prefix must not be `CO`, `CORE`, or the prefix of another pack that your users use.

2. **Choose nodes by node type and tag, not by `calmNodes`.** A `calmNodes` selector names a node of one architecture. It fails with `CO-POL-005` on any other model. Use `nodeTypes` and `tags` and say in your documentation which tags a model needs. You can name the selectors with `selectors` and use them with `ref`.

3. **Give each rule a `rationale` and a `remediation`.** Users read them in `concordia explain`.

4. **Use only the edge kinds that the observation provider supports.** `check` warns with `CO-POL-009` for other kinds.

5. **Set `metadata.id` and `metadata.version`.** The version tells users what changed. The file name is `<id>@<version>.policy.yaml`.

6. **Never reuse or renumber a rule ID.** Baselines and waivers use `ruleId` in their keys. When you change the meaning of a rule, add a new rule with a new number and retire the old one in a new pack version. When you remove a rule, never use its number again.

7. **Version the pack with these rules:**

   | Change | Version |
   |---|---|
   | Fix a typo, change the text of `rationale` or `remediation`. | Patch. |
   | Add a rule, or make a rule less strict. | Minor. |
   | Make a rule stricter (new severity, new edges, new targets), or remove a rule. | Major. |

8. **Publish the file at a stable `https://` URL**, for example a release asset, and publish its digest next to it. Users run `concordia policy add <url>` and compare the digest in their workspace with yours. Test your pack with `concordia policy add <file>` in a sample workspace before you publish.

A small example:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: acme-arch
  version: 1.0.0
selectors:
  ui:
    tags: [presentation]
rules:
  - id: ACME-UI-001
    name: Presentation code does not read a database
    severity: error
    rationale: The user interface must go through the application layer.
    remediation: Call an application service. Do not read the database from the UI.
    subject:
      ref: ui
    forbid:
      target:
        nodeTypes: [database]
      edges: [READS, WRITES]
      traversal: direct
```

## Fix common problems

| Diagnostic | Cause | Fix |
|---|---|---|
| `CO-POL-010` | The file digest is not the pin. | Review the file. Run `concordia policy add <file> --force` to pin it. |
| `CO-POL-011` | The pack file is missing. | Restore the file, or run `concordia policy add <source>` again. |
| `CO-POL-012` | `builtin:<name>` is not in the binary. | Run `concordia policy list` for the names. |
| `CO-POL-013` | A file pack has no `sha256`, or a built-in pack has one. | Add the pin, or remove it from the built-in entry. |
| `CO-POL-014` | `use` is a URL. | Run `concordia policy add <url>` and use the vendored file. |
| `CO-POL-015` | An override names a rule that the pack does not have. | Fix the rule ID. Run `concordia policy list --rules`. |
| `CO-POL-016` | The fetch failed, or `--offline` is set. | Check the URL and the network, or download the file and add it from disk. |
| `CO-INT-012` | The pack differs from `.concordia.lock`. | Review the pack. Pin the new digest as described in [update a pack](#update-a-pack). |

## Related

- [Core rules reference](../reference/core-rules.md)
- [Infrastructure rules reference](../reference/infrastructure-rules.md)
- [Assurance rules reference](../reference/assurance-rules.md)
- [Roll out new obligations](roll-out-new-obligations.md)
- [Policies reference](../reference/policies.md)
- [Workspace reference](../reference/workspace.md)
- [CLI reference](../reference/cli.md)
- [Write policies](write-policies.md)
