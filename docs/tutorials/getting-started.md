# Getting started: your first architecture check

*2026-11-23T17:43:10Z by Showboat 0.6.1*
<!-- showboat-id: a9207539-f2ae-425b-bdc6-ce6835722281 -->

On this page you run Concordia on the code of a retail bank's payments platform, and you see the code that breaks the bank's architecture before it reaches production.

You need Concordia and the Codebase Memory MCP server (CBM) on your PATH, and `git` and `jq`. If you have not installed them yet, follow [Install Concordia](../how-to/install.md) first.

Every command and output below is real. You can run the whole page again with `showboat verify docs/tutorials/getting-started.md`.

First, check the versions. The output names four versions: Concordia, the CALM schema versions that it reads, the protocol of its CBM adapter, and the CBM release that it found.

```bash
concordia --version
```

```output
concordia dev
calm-schema 1.2,1.3
cbm-adapter concordia-cbm/v2
cbm 0.12.1
```

The output shows that Concordia found CBM. Without CBM, Concordia cannot read code.

Now make a throwaway copy of the example. Run the commands from the root of a Concordia checkout. The command copies `docs/examples/payments` to `/tmp/concordia-docs/getting-started` and names its CBM project `payments-getting-started`. When you run it again, it starts from a clean state.

```bash
rm -rf /tmp/concordia-docs/getting-started && mkdir -p /tmp/concordia-docs/getting-started && cp -R docs/examples/payments/. /tmp/concordia-docs/getting-started && cd /tmp/concordia-docs/getting-started && sed -i.bak 's/name: payments-example/name: payments-getting-started/' concordia.yaml && rm concordia.yaml.bak && ls
```

```output
architecture
assessments
bindings
concordia.yaml
deploy
docs
exceptions
governance
infra
jobs
policies
services
```

The copy holds the bank's code (`services` and `jobs`), its CALM architecture (`architecture`), the bindings that connect the two (`bindings`), and the bank's rules (`policies`). The other directories hold deployment files, decisions, and reviews. Other pages use them.

Concordia reads the committed state of a repository. Make the copy a git repository with one commit. The fixed author makes the commit the same for everyone.

```bash
cd /tmp/concordia-docs/getting-started && git init -q -b main && git config user.name "Concordia Docs" && git config user.email docs@concordia.invalid && git config commit.gpgsign false && git add -A && git commit -q -m "Initial payments example" && git log --format=%s
```

```output
Initial payments example
```

The repository has one commit.

The workspace file `concordia.yaml` ties everything together. Look at three of its keys. `architecture` names the CALM file. `observation` says which code CBM reads: `include` and `exclude`. `policies` names the bank's own rules and the built-in packs.

```bash
cd /tmp/concordia-docs/getting-started && sed -n '/^architecture:/,/^  indexMode:/p; /^policies:/,/^    - use: builtin:assurance/p' concordia.yaml
```

```output
architecture:
  source: architecture/payments.calm.json
  urlMapping: architecture/url-mapping.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: payments-getting-started
      root: .
      include: [services/**, jobs/**]
      exclude: [services/legacy-core-banking/**]
  indexMode: full
policies:
  files: [policies/**/*.policy.yaml]
  packs:
    - use: builtin:core
    - use: builtin:adr-governance
      rules:
        GOV-ADR-006: { severity: error }
    - use: builtin:review-currency
    - use: builtin:assurance
```

The workspace reads the code under `services` and `jobs`, but not the legacy core banking code. The rules come from `policies/payments.policy.yaml` and from four built-in packs. The formats are in [architecture extensions](../reference/architecture.md), [bindings](../reference/bindings.md), [policies](../reference/policies.md), and [workspace](../reference/workspace.md).

Next, validate the architecture model. Every other step needs a valid model.

```bash
cd /tmp/concordia-docs/getting-started && concordia model validate
```

```output
info CO-DGM-005: suggested view not declared: a container view for the system 'payments-platform', which has services [concordia.yaml]
info CO-DGM-005: suggested view not declared: a deployment view for the production environment 'production' [concordia.yaml]
info CO-DGM-005: suggested view not declared: a system-context view for the system 'card-network' [concordia.yaml]
info CO-DGM-005: suggested view not declared: a system-context view for the system 'payments-platform' [concordia.yaml]
model valid: architecture/payments.calm.json (0 warning(s))
```

The model is valid. The `info` lines suggest diagram views that the model does not declare. They are advice only, and you can ignore them here.

Now index the code. Concordia asks CBM to build a graph of the repository, and it records the graph that it uses as a generation. The coverage line `files: 14/15 covered` counts the excluded Kotlin file in the total. The output also names the generation, an ID that changes with every commit. The command drops that one line with `grep -v`, so that this page stays the same on every run.

```bash
cd /tmp/concordia-docs/getting-started && concordia index | grep -v '^generation'
```

```output
project: payments-getting-started
files: 14/15 covered (93.3%)
excluded: 1, stale: 0, unindexed: 0
coverage file: build/concordia/coverage.json
```

CBM read 14 of the 15 files. The one file that it did not read is the excluded legacy file.

Bindings connect each architecture node to its code. Before you check anything, confirm that the binding file is valid.

```bash
cd /tmp/concordia-docs/getting-started && concordia bind validate
```

```output
bindings valid: 11 binding(s), 60/238 observed node(s) bound, 0 unbound file(s)
```

All 11 bindings are valid. `0 unbound file(s)` means that every file CBM read belongs to an architecture node, so no code escapes the rules.

Now run three rules. `--rule` selects one rule, and you can give it more than once:

- `PAY-API-001`: the payments service uses only the published API of the fraud service.
- `CORE-SEC-001`: public code does not access restricted nodes directly. It is a built-in rule.
- `PAY-LEG-001`: the legacy core banking system does not call the ledger service.

Read each block from the top: the rule, why it matters, the owner, and the findings for each pair of architecture elements. Each finding is one line: the file and line, and what the code does there (`imports` a file, `calls` a class or a method). `Fix:` tells you what to change. The last line prints the exit code from the results file.

```bash
cd /tmp/concordia-docs/getting-started && concordia check --no-color --rule PAY-API-001 --rule CORE-SEC-001 --rule PAY-LEG-001; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: violation (2 violation, 1 unknown, 6 findings)

FAIL CORE-SEC-001 Public and external code does not access restricted nodes directly
  Why it matters: A node tagged public or external is exposed to untrusted callers. A direct access from it to a restricted node removes the control point between them.
  Owner: team-channels
  channels-api -> customer-profile: 3 finding(s)
    services/channels-api/src/accounts.ts:9   calls ProfileStore in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts:10  calls ProfileStore.load in services/customer-profile/src/internal/ProfileStore.ts
    services/channels-api/src/accounts.ts     imports services/customer-profile/src/internal/ProfileStore.ts
  Fix: Route the access through a node that checks authorization, for example a gateway or an application service, and tag that node as neither public nor external.

FAIL PAY-API-001 Payments uses only the published fraud API
  Why it matters: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
  Owner: team-payments
  payments-service -> fraud-service: 3 finding(s)
    services/payments-service/src/PaymentService.ts    imports services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:7  calls RiskModel.score in services/fraud-service/src/internal/RiskModel.ts
    services/payments-service/src/PaymentService.ts:6  calls RiskModel in services/fraud-service/src/internal/RiskModel.ts
  Fix: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.

UNKNOWN PAY-LEG-001 Legacy core banking must not call the ledger service
  Why it matters: The legacy core banking system is being retired. If it still calls the ledger, switching it off breaks the bank's books.
  Cannot judge: minimum coverage not met: 0.750 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 75.0% (required 90.0%)

Deployment trace: unknown (3 partial, 3 subjects)
  Evidence: declared configuration, and rendered or evaluated in production. Not traced: deployment execution and runtime.
  Subjects: 3 in 1 environment(s): 0 complete, 3 partial, 0 unresolved, 0 unknown
  partial: channels-api in production
  partial: fraud-cache in production
  partial: payments-service in production

Assessment currency: violation (1 assessments, 1 required, 1 reviewRequired)
  ASM-LEDGER v1 (security): review-required (no approval)
    outcome satisfactory; no approval; assessments/ledger-threat-model.yaml:2
    changed: addition of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification; next action: get review

Not configured: ADR governance, Diagram currency, Quality evaluation

Unknown area(s): Deployment trace
Violation area(s): Assessment currency
6 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 1
```

Here is what the output tells you:

- The first line, `warning CO-OBS-016`, says that CBM gave an edge kind that Concordia does not use (`HAS_BRANCH`, from the git history). It changes no verdict.
- `PAY-API-001` fails. `PaymentService.ts` imports `RiskModel.ts` from the internals of the fraud service and calls it. A refactor of the risk model can then stop payment authorization.
- `CORE-SEC-001` fails. The public channels API calls `ProfileStore` inside the restricted customer profile service, with no control point between them.
- `PAY-LEG-001` is `unknown`. You learn why below.
- The sections after the rules, `Deployment trace` and `Assessment currency`, are workspace checks that run on every `check`. Other pages explain them.
- The exit code is 1, because six findings are active errors.

To see all the evidence for one rule, use `explain`. It prints the rationale, the remediation, the compiled rule, the coverage, and the evidence path of every finding. The command shows only the first eight lines.

```bash
cd /tmp/concordia-docs/getting-started && concordia explain PAY-API-001 --no-color | sed -n '1,8p'
```

```output
PAY-API-001 Payments uses only the published fraud API
Verdict: violation   Severity: error   Family: public-api-only   Policy: payments-architecture@1.0.0
Rationale: When the fraud team refactors its risk model, every caller of its internals breaks. A refactor of the risk model once stopped payment authorization in production. The fraud API is the part that the fraud team keeps stable.
Remediation: Call the functions in services/fraud-service/src/api instead of the classes under src/internal.
Rule owner: team-payments
Subjects: calm:node:payments-service
Targets: calm:node:fraud-service
Plan:
```

The header gives the verdict, the severity, the rule family, and the policy pack and version. The rationale and the remediation come from the rule file. The subject and the target are the two CALM nodes of the rule. The `Plan:` line starts the compiled rule, which the command cuts off. Run the command without `sed` to see the compiled rule and the evidence path of each finding.

Now look at `PAY-LEG-001` alone. The rule watches the legacy Kotlin code, but the workspace excludes that code. So the coverage is below the required 90%, and Concordia cannot judge the rule.

```bash
cd /tmp/concordia-docs/getting-started && concordia check --rule PAY-LEG-001 --no-color; jq -r '"exit code: \(.summary.exitCode)"' build/concordia/results.json
```

```output
warning CO-OBS-016: native edge kind kept as unsupported evidence: HAS_BRANCH (1 edges)
Conformance: unknown (1 unknown)

UNKNOWN PAY-LEG-001 Legacy core banking must not call the ledger service
  Why it matters: The legacy core banking system is being retired. If it still calls the ledger, switching it off breaks the bank's books.
  Cannot judge: minimum coverage not met: 0.750 < 0.9 (CO-CON-021)
  Cannot judge: language coverage not met: .kt 0.000 < 0.9 (CO-CON-026)
  Coverage: 75.0% (required 90.0%)

Deployment trace: unknown (3 partial, 3 subjects)
  Evidence: declared configuration, and rendered or evaluated in production. Not traced: deployment execution and runtime.
  Subjects: 3 in 1 environment(s): 0 complete, 3 partial, 0 unresolved, 0 unknown
  partial: channels-api in production
  partial: fraud-cache in production
  partial: payments-service in production

Assessment currency: violation (1 assessments, 1 required, 1 reviewRequired)
  ASM-LEDGER v1 (security): review-required (no approval)
    outcome satisfactory; no approval; assessments/ledger-threat-model.yaml:2
    changed: addition of implementation:file:services/ledger-service/src/api/index.ts, class implementation or verification; next action: get review

Not configured: ADR governance, Diagram currency, Quality evaluation

Unknown area(s): Deployment trace
Violation area(s): Assessment currency
0 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 1 unknown
exit code: 4
```

The verdict is `unknown`, with two reasons. `CO-CON-021`: only 75% of the files that the rule needs are covered. `CO-CON-026`: no `.kt` file is covered. The exit code is 4, not 0. Concordia never reports code that it could not read as a pass. The [how-to guide on unknown verdicts](../how-to/resolve-unknown-verdicts.md) shows the fixes.

Finally, clean up. `clean --all` removes the local state of Concordia and the CBM project that `index` made.

```bash
cd /tmp/concordia-docs/getting-started && concordia clean --all --yes
```

```output
removed: .concordia/, build/concordia/, CBM project 'payments-getting-started'
```

You indexed a bank's code, bound it to its architecture, found two rules that the code breaks with their evidence, and met an `unknown` verdict. Next steps:

- [Adopting Concordia on an existing codebase](brownfield-adoption.md) shows how to accept today's violations and fail only on new ones.
- [Integrate through published APIs](../use-cases/integrate-through-published-apis.md) explains how `PAY-API-001` decides.
- [Enforce trust boundaries in code](../use-cases/enforce-trust-boundaries-in-code.md) explains how `CORE-SEC-001` decides.
- [Run Concordia in CI](../how-to/run-in-ci.md) puts the check in your pipeline.
- [Write policies](../how-to/write-policies.md) shows how to add your own rules.
