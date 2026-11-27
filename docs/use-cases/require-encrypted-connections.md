# Require encrypted connections

The payments service sends card and account data to the card network. The architecture model says the connection uses HTTPS. In the code, a developer wrote `http://` for a quick test and the change went to production. Card data now crosses the network in clear text, and the model still says HTTPS.

## The rule

The rule is `PAY-TLS-001` in `docs/examples/payments/policies/payments.policy.yaml`:

```yaml
- id: PAY-TLS-001
  name: Payments calls other services over HTTPS in production
  severity: error
  owner: team-payments
  rationale: Payment data in transit must be encrypted. A plain HTTP call from the payments service sends card and account data in clear text.
  remediation: Use an https URL for the call, or route it through the service mesh that terminates TLS.
  subject:
    calmNodes: [payments-service]
  obligation:
    type: forbidden
    relation: {kind: calls-service, direction: from-subject}
    applies:
      allOf:
        - environment: production
        - not: {protocol: https}
```

This is a typed rule. It forbids every remote call from `payments-service` that is in `production` and does not use HTTPS.

## What Concordia reads

- **The code graph** that CBM builds: the remote calls (`calls-service`) that leave the files of `payments-service`.
- **The properties of each call**: its environment and its protocol. The rule applies only to calls whose properties match the `applies` selector.
- **The bindings**: `payments-service` binds `services/payments-service/**`.

Concordia does not use the `protocol: HTTPS` of the CALM relationship `payments-to-card-network` for the result. The model states an intent. The evidence must come from what the code or the deployment does.

## How Concordia decides

1. Concordia builds one obligation for `payments-service` in each declared environment. The example declares `production`.
2. The bindings map `payments-service` to its source files.
3. Concordia collects the `calls-service` facts that leave those files. In the example, it finds the call in `PaymentService.authorize`.
4. For each fact, it evaluates the selector with three values: true, false, and unknown. A property that the fact does not have is unknown, never false.
5. The call has no `environment` and no `protocol` property, so `allOf` gives unknown. The obligation is `unknown` with the reason `selector-property-unknown`. It is never `pass` and never `not-applicable`.
6. A call with the environment `production` and a protocol other than `https` would make the obligation a violation.

## What you still declare

- **The subject and its bindings.** Only calls from files that a binding maps to `payments-service` are checked.
- **The evidence for the selector, which is missing in this release.** The call fact from the code graph carries no `environment` and no `protocol`. Concordia does not read the protocol from the URL text in the code, and it does not take it from the CALM model. The result stays `unknown` with the reason `selector-property-unknown` until a source supplies both properties for the call.
- **What `unknown` does.** The rule inherits `onUnknown: fail` from `check.unknown`. That matters only when the rule is `mandatory`.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, `git`, and `jq` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/require-encrypted-connections && mkdir -p /tmp/concordia-usecases/require-encrypted-connections
cp -R docs/examples/payments/. /tmp/concordia-usecases/require-encrypted-connections
cd /tmp/concordia-usecases/require-encrypted-connections
sed -i.bak 's/payments-example/payments-uc-require-encrypted-connections/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule PAY-TLS-001 --no-color 2>/dev/null | sed -n '/^UNKNOWN/,/Cannot judge/p'
```

```text
UNKNOWN PAY-TLS-001 Payments calls other services over HTTPS in production (advisory: does not change the exit code)
  Why it matters: Payment data in transit must be encrypted. A plain HTTP call from the payments service sends card and account data in clear text.
  Cannot judge: selector property unknown: environment, protocol (selector-property-unknown)
```

See the reason and the call that Concordia found:

```bash
concordia explain PAY-TLS-001 --no-color | sed -n '/^Reasons:/,$p'
```

```text
Reasons:
  selector-property-unknown: selector property unknown: environment, protocol

Finding 988f…e28
  Fingerprint: sha256:988f4fd846daf7e138e3263c94af2844234f642e32921889e9ac7f7b803ffe28
  Kind: obligation-unknown
  Message: obligation forbidden payments-service (environment production) is unknown (selector-property-unknown): selector property unknown: environment, protocol
  Source: calm:node:payments-service
  Target: calm:node:
  Owner: team-payments
  Disposition: active
  Location: services/payments-service/src/PaymentService.ts:5
```

Line 5 is the start of `authorize`. Compare the code with the model:

```bash
grep -n 'fetch' services/payments-service/src/PaymentService.ts
```

```text
10:    const response = await fetch("http://card-network.example.com/authorize", {
```

```bash
jq -r '.relationships[] | select(."unique-id" == "payments-to-card-network") | .protocol' architecture/payments.calm.json
```

```text
HTTPS
```

The code calls `http://`, and the model says `HTTPS`. Concordia does not pass the rule on the word of the model. It reports that it cannot judge, and names the two properties that it lacks.

## Make it block a merge

A typed rule without an `enforcement` key is `advisory`, so the rule does not change the exit code as shipped:

```bash
concordia check --rule PAY-TLS-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 0
```

Read the `unknown` results first. Then promote the rule with `check.enforcement`. Add the entry to the `check` key, which is the last key of `concordia.yaml`:

```bash
cat >> concordia.yaml <<'EOF'
  enforcement:
    - { rules: [PAY-TLS-001], mode: mandatory }
EOF
```

```bash
concordia check --rule PAY-TLS-001 --no-color 2>/dev/null | sed -n '/^UNKNOWN/,/Cannot judge/p'
```

```text
UNKNOWN PAY-TLS-001 Payments calls other services over HTTPS in production
  Why it matters: Payment data in transit must be encrypted. A plain HTTP call from the payments service sends card and account data in clear text.
  Cannot judge: selector property unknown: environment, protocol (selector-property-unknown)
```

```bash
concordia check --rule PAY-TLS-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 4
```

The `advisory` note is gone. A `mandatory` rule with an `unknown` verdict and `onUnknown: fail` exits with 4, so the merge stops until the evidence exists. On the example this blocks every build, because the evidence is missing. Use `concordia check --rollout-report` to see which rules would block before you promote them (see [roll out new obligations](../how-to/roll-out-new-obligations.md)).

## Related

- [Typed rules: the `obligation` block](../reference/policies.md#typed-rules-the-obligation-block)
- [Selector dimensions](../reference/policies.md#selector-dimensions)
- [`check.enforcement`](../reference/workspace.md#checkenforcement)
- [Roll out new obligations](../how-to/roll-out-new-obligations.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
