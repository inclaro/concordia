# Trace images to trusted builds

The bank reviews the code of the payments service before each release. But the cluster runs an image, not the code. If the image tag `5.1.2` was pushed again from a laptop, or built by a pipeline that the bank does not trust, production runs an image that no trusted build produced. Nothing in the manifests shows the difference.

## The rule

The rule is `ASR-PRV-001` in the built-in pack `builtin:assurance`:

```yaml
- id: ASR-PRV-001
  name: Each container occurrence has exact lineage from source to deployed image
  severity: warning
  family: assurance
  rationale: A tag or a static link from an image to a Dockerfile only suggests where an image came from. Exact lineage needs an admissible chain of statements from the source revision to the digest that runs, or the deployed image cannot be traced to the code that was reviewed.
  remediation: Supply a build provenance statement from a trusted producer that binds the image digest to the source revision, pin the image by digest, and record the deployment. A tag-only occurrence stays unknown until its digest is known.
  require:
    claim:
      id: exact-lineage
```

The rule asks for the claim `exact-lineage` for every container in every environment.

## What Concordia reads

- **The rendered manifests** of each environment (`assurance.rendering`): every container and its image, with the repository, the tag, and the digest kept apart.
- **Build provenance statements** (`assurance.provenance.statements`): SLSA provenance v1 statements, signed by a producer that you trust for `build-provenance` (`assurance.producers`).
- **The trust contract of your builds**: the builder IDs and the source repositories that you accept (`assurance.provenance.builders` and `sourceRepositories`).

Concordia does not treat a tag as a version. A static link from an image to a Dockerfile or a CI file is only a candidate. Neither gives a pass.

## How Concordia decides

1. A rule that needs a claim asks the claim registry for a loaded contract that enables it. `exact-lineage` comes from the `provenance` contract. Without `assurance.provenance` in the workspace, no loaded contract enables the claim, and the rule is `unknown` with the reason `claim-not-enabled`.
2. With the contract loaded, Concordia evaluates each container occurrence of the rendered manifests.
3. An image without a digest is `unknown` with the reason `no-evidence-supplied`: a tag links to no build.
4. For an image with a digest, Concordia looks for an admissible provenance statement with the integrity `producer authenticated` that names the digest. None gives `unknown`, `no-evidence-supplied`.
5. The builder of the statement must be in `builders`, and one resolved dependency must be a Git source in `sourceRepositories`. Then the occurrence is `pass`. A `pass` shows that the image traces to a trusted build of a declared repository. It does not show that the source revision was reviewed.
6. An authenticated statement whose builder is not in your list is a `violation`.

## What you still declare

- **The trust contract.** Concordia trusts the producers, builders, and source repositories that you list, and no others.
- **The producer's signing identity** under `assurance.producers`, with `cosign-key` or `cosign-keyless`. An unsigned statement is `content not verified` and cannot support this claim.
- **Image pins by digest** in your manifests. A tag-only image stays `unknown`.
- **The source of the statements.** Your build pipeline writes and signs them. Concordia only reads them.
- **The review of the source.** Lineage stops at the source revision. If only reviewed code may run, make your trusted builder build only reviewed revisions, for example only the protected `main` branch.

The example has no provenance settings, producers, or digest pins. On the example, the rule is `unknown` with the reason `claim-not-enabled`.

## Try it on the example

Run these commands from the root of a Concordia checkout. You need `concordia`, CBM, and `git` on your `PATH` (see [install Concordia](../how-to/install.md)).

```bash
rm -rf /tmp/concordia-usecases/trace-images-to-trusted-builds && mkdir -p /tmp/concordia-usecases/trace-images-to-trusted-builds
cp -R docs/examples/payments/. /tmp/concordia-usecases/trace-images-to-trusted-builds
cd /tmp/concordia-usecases/trace-images-to-trusted-builds
sed -i.bak 's/payments-example/payments-uc-trace-images-to-trusted-builds/' concordia.yaml && rm concordia.yaml.bak
git init -q -b main && git add -A
git -c user.name="Concordia Docs" -c user.email=docs@concordia.invalid commit -q -m "Payments example"
concordia index
```

Check the rule:

```bash
concordia check --rule ASR-PRV-001 --no-color 2>/dev/null | sed -n '/^UNKNOWN/,/Cannot judge/p'
```

```text
UNKNOWN ASR-PRV-001 Each container occurrence has exact lineage from source to deployed image (advisory: does not change the exit code)
  Why it matters: A tag or a static link from an image to a Dockerfile only suggests where an image came from. Exact lineage needs an admissible chain of statements from the source revision to the digest that runs, or the deployed image cannot be traced to the code that was reviewed.
  Cannot judge: no loaded contract enables claim "exact-lineage" (claim-not-enabled)
```

The workspace has no `assurance.provenance` key, so Concordia has no way to judge lineage at all. Declare the trust contract of your builds. Open `concordia.yaml` and add this block under `assurance`, before `evaluators`:

```yaml
  provenance:
    statements: ["build/attestations/*.json"]
    builders: ["https://github.com/example-bank/payments/.github/workflows/build.yml"]
    sourceRepositories: [example-bank/payments]
```

Check again:

```bash
concordia check --rule ASR-PRV-001 --no-color 2>&1 | sed -n '/CO-PRV-011/p;/Cannot judge/p'
```

```text
warning CO-PRV-011: evidence glob matches no file: build/attestations/*.json [build/attestations/*.json]
  Cannot judge: missing digest: registry.example.com/bank/channels-api:2.4.0 has no immutable digest, so a tag is not a version and no build is linked; missing provenance: none can be linked without a digest (no-evidence-supplied)
  Cannot judge: missing digest: registry.example.com/bank/redis:7.2 has no immutable digest, so a tag is not a version and no build is linked; missing provenance: none can be linked without a digest (no-evidence-supplied)
  Cannot judge: missing digest: registry.example.com/bank/payments-service:5.1.2 has no immutable digest, so a tag is not a version and no build is linked; missing provenance: none can be linked without a digest (no-evidence-supplied)
```

The claim is now enabled, and Concordia judges each container. All three images use a tag, so none can be linked to a build. Pin the payments service image by digest. The digest below is an example value:

```bash
sed -i.bak 's|payments-service:5.1.2|payments-service@sha256:4f2a8c1e9b7d6a5f3e2c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e|' deploy/production/payments.yaml && rm deploy/production/payments.yaml.bak
```

```bash
concordia check --rule ASR-PRV-001 --no-color 2>/dev/null | grep 'Cannot judge'
```

```text
  Cannot judge: missing digest: registry.example.com/bank/channels-api:2.4.0 has no immutable digest, so a tag is not a version and no build is linked; missing provenance: none can be linked without a digest (no-evidence-supplied)
  Cannot judge: missing digest: registry.example.com/bank/redis:7.2 has no immutable digest, so a tag is not a version and no build is linked; missing provenance: none can be linked without a digest (no-evidence-supplied)
  Cannot judge: no provenance for this digest (no-evidence-supplied)
```

The payments service now has a digest, and the only missing piece for it is a provenance statement. The channels API and the fraud cache still use tags, so the rule stays `unknown` until every container has both. To get a `pass`, do this for each of the three images, which needs your build pipeline and a signing key or identity:

1. Pin the image by digest in its manifest.
2. Declare a producer under `assurance.producers` with the claim `build-provenance` and its `cosign-key` or `cosign-keyless` authentication. One producer can sign for all images.
3. Add a SLSA provenance v1 statement for the image digest, signed by that producer, in `build/attestations/`. Its builder ID must be in `builders`, and its resolved dependencies must name the Git source in `sourceRepositories`.

[Add signed statements](../how-to/add-stronger-evidence.md#add-signed-statements) shows how to declare the producer and sign a statement in GitHub Actions. [Trace from source to the running image](../how-to/add-stronger-evidence.md#trace-from-source-to-the-running-image) shows the provenance keys.

## Make it block a merge

This rule is advisory only. The `UNKNOWN` line says `advisory: does not change the exit code`:

```bash
concordia check --rule ASR-PRV-001 > /dev/null 2>&1; echo "exit code: $?"
```

```text
exit code: 0
```

The `assurance-claim` family has the maturity `advisory` in this release, so `ASR-PRV-001` runs as `advisory` or `shadow`. A `check.enforcement` entry with `mode: mandatory` stops `check` with `CO-POL-018` and exit code 2. The findings still show every container that has no lineage. Review them in the pull request, or read them from `results.json`.

## Related

- [Add signed statements](../how-to/add-stronger-evidence.md#add-signed-statements)
- [Provenance and exact lineage](../reference/assurance.md#exact-lineage)
- [`assurance.provenance`](../reference/workspace.md#assuranceprovenance) and [`assurance.producers`](../reference/workspace.md#assuranceproducers)
- [Trace a deployment](../how-to/trace-a-deployment.md)
- [Evidence and trust](../explanation/evidence-and-trust.md)
