# Set up a workspace

This guide shows you how to create a Concordia workspace in an existing repository and tune which files it observes.

## Prerequisites

- [Concordia installed](install.md).
- A git repository with source code.
- Optional: a CALM 1.2 or 1.3 architecture file. Without one, `init` writes an empty architecture.

## Create the workspace

1. Change to the repository root.
2. Run `init`:

   ```bash
   concordia init --preset services --ci github
   ```

   To start from an existing CALM file, add `--calm <path>`, for example `--calm architecture/payments.calm.json`.

   | Option | Values | Effect |
   |---|---|---|
   | `--calm <path>` | path | Use an existing CALM file. Without it, `init` writes `architecture/architecture.json` with no nodes. |
   | `--preset` | `modular-monolith`, `services`, `custom`, `core` | Choose the starter policy. `custom` (the default) writes no rules. `core` writes no local rules either, and turns on the built-in core pack. |
   | `--ci` | `github`, `gitlab`, `generic` | Choose the CI template. `github` is the default. |
   | `--force` | | Overwrite existing files. Without it, `init` keeps existing files and prints `CO-WRK-005`. |

   The presets write these rules to `policies/default.policy.yaml`:

   - `services`: `CO-DECL-001`, dependencies between `service`, `database`, and `webclient` nodes must be declared.
   - `modular-monolith`: `CO-CYC-001`, no dependency cycles between `service` and `system` nodes.
   - `core`: no local rule. The workspace has `policies.packs: [{use: builtin:core}]`, and `.concordia.lock` records the pack. See [use policy packs](use-policy-packs.md).

3. `init` writes these files, and prints `wrote <path>` for each:

   | File | Content |
   |---|---|
   | `concordia.yaml` | The workspace file. `metadata.name` and the CBM project name come from the directory name. |
   | `architecture/architecture.json` | An empty CALM 1.3 document (only without `--calm`). |
   | `bindings/concordia.bindings.yaml` | An empty binding set (`apiVersion`, `kind: BindingSet`, and `bindings: []`). |
   | `policies/default.policy.yaml` | The starter policy pack. |
   | `exceptions/waivers.yaml` | An empty waiver register (`waivers: []`). |
   | `.concordia.lock` | The lock file, with the installed CBM version. |
   | `.github/workflows/concordia.yml`, `.gitlab/concordia.gitlab-ci.yml`, or `ci/concordia.sh` | The CI template for `--ci`. |
   | `.gitignore` | `init` appends `build/concordia/` and `.concordia/` if they are missing, and prints `updated .gitignore`. |

4. `init` then validates what it wrote: the workspace, the architecture, and the policies. It prints `initialized: workspace, architecture, and policies are valid` on success, and exits 2 with a diagnostic when something is invalid.

## Check the toolchain

```bash
concordia doctor
```

Right after `init`, expect `cbm` to be `degraded` with `project '<name>' is not indexed; run concordia index`. Run `concordia index` to clear it. See [`doctor`](install.md#confirm-the-install) for the other capabilities.

## Choose the files to observe

Open `concordia.yaml` and edit `observation.projects[0]`. The `include` and `exclude` globs are relative to `root`:

```yaml
observation:
  provider: codebase-memory-mcp
  indexMode: moderate
  projects:
    - name: my-repo
      root: .
      include: [services/**, packages/**]
      exclude: [services/legacy/**]
```

Files that match `exclude` stay in the coverage total but never count as covered, so a rule that depends on them returns `unknown` rather than passing. See [verdicts and coverage](../explanation/verdicts-and-coverage.md).

A workspace has one or more projects. The first one is the primary project; paths of other projects have the prefix `@<name>/`. `indexMode` is `fast`, `moderate` (the default), or `full`.

Run `concordia index` after you change the globs. Then run `concordia model validate` to check the architecture file on its own.

## Declare environments

If the repository holds infrastructure files for more than one deployment, declare the environments and the files of each one:

```yaml
environments:
  - id: prod
    sources: [k8s/base/**, k8s/prod/**]
  - id: dev
    sources: [k8s/base/**, k8s/dev/**]
```

A file in a shared base is attributed to every environment that lists it. `concordia check --environment prod` limits the run to `prod`; the other environments are recorded as excluded and get no verdict. `observation.runtime.environment` must name a declared environment. See the [workspace reference](../reference/workspace.md#environments).

## Add capabilities

Each of these keys is optional and off until you add it. Add one when you need its checks, and follow the guide for the details:

| Key | Adds | Guide |
|---|---|---|
| `environments` | Deployment evidence for each environment (see above). | [Trace a deployment](trace-a-deployment.md) |
| `views` | Checks that architecture views exist, are fresh, and match the model. | [Keep diagrams current](keep-diagrams-current.md) |
| `adr` | A decision register and the decision rules. | [Govern architecture decisions](govern-architecture-decisions.md) |
| `trust` | Extra authority dimensions and the data classification list for trust context. | [Trust context reference](../reference/trust-context.md) |
| `assessments` | Assessments, approvals, authorities, and the trust anchor. | [Review and approve assessments](review-and-approve-assessments.md) |
| `assurance` | Rendered manifests, plans, signed statements, evaluators, and quality scenarios. | [Add stronger evidence](add-stronger-evidence.md) |

The rules for `adr`, `assessments`, and `assurance` come from built-in policy packs that you enable separately. See [use policy packs](use-policy-packs.md#choose-a-built-in-pack). Run `concordia check` after each change: a key that is not valid stops the run with exit code 2 and a diagnostic.

## Use profiles

A profile overrides parts of the workspace for one environment. Objects merge deeply and arrays replace the base value. Add profiles at the top level of `concordia.yaml`:

```yaml
profiles:
  ci:
    check:
      minimumCoverage: 0.98
```

Select a profile with the global `--profile` option:

```bash
concordia --profile ci check
```

An unknown profile name fails with `CO-WRK-004` and exit code 2. A profile can override `metadata`, `architecture`, `observation`, `bindings`, `policies`, `exceptions`, `projection`, `check`, and `report`.

## Next steps

- [Bind code to architecture](bind-code-to-architecture.md)
- [Write policies](write-policies.md)
- [Workspace file reference](../reference/workspace.md)
