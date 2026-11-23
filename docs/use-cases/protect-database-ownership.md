# Protect database ownership

The ledger database holds the bank's record of every movement of money. When a nightly reporting job reads the ledger tables directly, it skips the checks of the ledger service. The next schema change in the ledger breaks the reports, or a bad query locks the tables during settlement.

## The rule

The rule is `PAY-DB-001` in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-DB-001
  name: Only the ledger service accesses the ledger database
  severity: error
  owner: team-ledger
  rationale: The ledger database holds the bank's record of every movement of money. Code outside the ledger service that reads or writes it bypasses the ledger's checks and breaks when the schema changes.
  remediation: Read ledger data through the ledger service API, or a published read model, instead of the database schema.
  subject:
    calmNodes: [ledger-db]
  ownership:
    owners:
      calmNodes: [ledger-service]
    edges: [IMPORTS, CALLS, READS, WRITES]
```

The CALM control `ctl-ledger-ownership` on `ledger-db` names this rule in its `rule-ids`, and the decision `ADR-0001` in `governance/decisions.yaml` lists it under `enforcedBy`.

## What Concordia reads

- **The bindings**: `ledger-db` binds `services/ledger-service/src/db/**`, the schema code of the database. `ledger-service` binds `services/ledger-service/**`. For the schema files, the longer `sourceRoot` of `ledger-db` wins (see [specificity](../reference/bindings.md#specificity)).
- **The code graph** that CBM builds from the TypeScript sources: the imports and calls into the schema files, from every other bound node.
- **The CALM model**: only to find the nodes that the rule names.

Concordia does not use the relationships of the CALM file as evidence. The model declares `ledger-service → ledger-db` and nothing else. That declaration does not stop the reporting job from importing the schema, and it does not prove that the job does not.

## How Concordia decides

1. The bindings map `ledger-db` to the files under `services/ledger-service/src/db/`.
2. Concordia reads every edge of the kinds `IMPORTS`, `CALLS`, `READS`, and `WRITES` that ends in one of those files.
3. It looks up the CALM node of the file where each edge starts.
4. An edge from `ledger-service` is allowed. An edge from any other node is a finding.
5. One finding makes the rule a violation.

## What you still declare

- **The binding of the database to code.** Concordia sees a database only through the code that defines it: here, the schema module. If the database has no code in the repository, there is nothing to protect in the code graph.
- **The owners.** The rule trusts your list of owners. A node that you add to `owners` may access the database.
- **The edge kinds.** In the example, CBM gives `IMPORTS` and `CALLS` edges for TypeScript. It gives no `READS` or `WRITES` edges for this code, so the findings come from the import and the calls. A raw SQL string that names a table, with no import of the schema module, gives no edge.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, and `git` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/protect-database-ownership && mkdir -p /tmp/concordia-usecases/protect-database-ownership
cp -R docs/examples/payments/. /tmp/concordia-usecases/protect-database-ownership
cd /tmp/concordia-usecases/protect-database-ownership
sed -i.bak 's/payments-example/payments-uc-protect-database-ownership/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule PAY-DB-001 --no-color 2>/dev/null | sed -n '/^FAIL/,/Fix:/p'
```

```text
FAIL PAY-DB-001 Only the ledger service accesses the ledger database
  Why it matters: The ledger database holds the bank's record of every movement of money. Code outside the ledger service that reads or writes it bypasses the ledger's checks and breaks when the schema changes.
  Owner: team-ledger
  reporting-job -> ledger-db: 3 finding(s)
    jobs/reporting/src/nightly.ts    imports services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:4  calls LedgerTable in services/ledger-service/src/db/schema.ts
    jobs/reporting/src/nightly.ts:5  calls LedgerTable.totals in services/ledger-service/src/db/schema.ts
  Fix: Read ledger data through the ledger service API, or a published read model, instead of the database schema.
```

The nightly reporting job imports the ledger schema and calls `LedgerTable.totals`. See the evidence of the first finding:

```bash
concordia explain PAY-DB-001 --no-color | awk '/^Finding/{n++} n==1 && NF'
```

```text
Finding 0eb4…0a9
  Fingerprint: sha256:0eb4b86618cf722d910a5492f45b1d990ed17569c3a0035ed16ce4d33c31a0a9
  Kind: ownership-violation
  Message: reporting-job accesses ledger-db, which only ledger-service may access
  Source: calm:node:reporting-job
  Target: calm:node:ledger-db
  Owner: team-finance
  Disposition: active
  Location: jobs/reporting/src/nightly.ts
  Evidence path:
    File jobs.reporting.src.nightly.ts.__file__ (jobs/reporting/src/nightly.ts)
    -[IMPORTS syntactic, cbm:IMPORTS]-> at jobs/reporting/src/nightly.ts
    Module services.ledger-service.src.db.schema (services/ledger-service/src/db/schema.ts:1)
```

The evidence path shows the edge that CBM found, its kind, and its confidence. The finding goes to `team-finance`, the owner of the reporting job.

## Make it block a merge

The rule has severity `error` and no enforcement mode, so it is `mandatory`. It fails the build as shipped:

```bash
concordia check --rule PAY-DB-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 1
```

Run `concordia check` in CI after `concordia index`, and stop the merge on any exit code other than 0. See [run Concordia in CI](../how-to/run-in-ci.md). `ADR-0001` has `enforcement: required` and links this rule, so `GOV-ADR-004` of `builtin:adr-governance` reports the decision as `enforced` only while `PAY-DB-001` stays mandatory (see [enforce architecture decisions](enforce-architecture-decisions.md)).

## Related

- [`ownership` predicate](../reference/policies.md#ownership)
- [Link a rule to a CALM control](../how-to/write-policies.md#link-a-rule-to-a-calm-control)
- [Bind code to architecture](../how-to/bind-code-to-architecture.md)
- [Enforce architecture decisions](enforce-architecture-decisions.md)
- [Enforce trust boundaries in code](enforce-trust-boundaries-in-code.md)
