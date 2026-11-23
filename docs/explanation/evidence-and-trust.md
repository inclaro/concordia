# Evidence and trust

Concordia can judge an architecture from a code graph, from manifests, from renders, from signed records, and from reviews. These sources do not deserve the same weight, and a tool that treats them alike will pass things it should not. This page explains how Concordia ranks evidence, why it refuses to promote some of it, and what its trust model assumes. For how the stages fit together, see [How Concordia works](how-concordia-works.md). For the fields and the reasons, see the [assurance reference](../reference/assurance.md).

## Three questions about every piece of evidence

Concordia asks three separate questions, and a good answer to one never stands in for another.

- **Level.** What is this evidence about? A manifest says what someone intended to apply. A running cluster says what it holds.
- **Integrity.** How sure is Concordia that the evidence is unchanged, and that it comes from whom it claims?
- **Admissibility.** Is the evidence about this revision, this environment, and this time, from a producer that is trusted for this kind of claim?

A claim names what it needs on the first two, and admissibility decides whether a given file counts. Keeping the questions apart is what lets a rule say "rendered evidence, authenticated producer" and get exactly that, instead of a vague notion of "good enough".

## Evidence levels

The deployment trace names four levels. Each one closes a gap that the level below leaves open.

| Level | What it is evidence of | Where it comes from |
|---|---|---|
| `declared` | What the committed manifests and Dockerfiles say, as written. | The provider parses the files. |
| `rendered-or-evaluated` | What a pinned renderer produces from the committed sources, what a plan would apply, and what an evaluator computes from the rendered resources. | Concordia runs the renderer or the evaluator itself. |
| `deployment-execution` | That a deployment ran and which digest it applied. | A signed deployment record. |
| `runtime-observation` | What a cluster was seen to run. | A signed workload observation. |

A Helm chart shows why the levels must stay apart. The declared level can see the chart and its values file, and it cannot see the Deployment that the chart produces. The rendered level can see that Deployment, and it cannot say that anyone applied it. A deployment record says that a pipeline applied it, and it cannot say what the cluster runs an hour later. Only an observation says that. If Concordia merged the levels, a plan would turn into a statement about production, and a green result would mean less than it appears to.

So Concordia keeps them side by side. Rendered evidence never fills in a step of the declared chain, and a rule that needs one level is never satisfied by another. The trace evaluates the declared level, and it evaluates the rendered-or-evaluated level in the environments that have assurance evidence. The other two levels are not steps of the trace. Records and observations are judged through claims (`deployed-digest`, `deployment-succeeded`, and `runtime-drift`), each with its own contract. Some claims are not about a level of the trace at all, such as the outcome of a check, and the contract marks those `not-applicable`.

Evidence from the code graph carries a role as well, such as `parsed-source-fact` or `inferred-association`, and a rule lists the roles that it accepts. The levels and the roles answer the same worry from two sides: a fact must not be used for a purpose that it cannot support.

## Integrity levels

Integrity has three levels, in this order.

1. **Content not verified.** Concordia read a file. Nothing ties it to anything else.
2. **Content verified by digest.** The bytes match a digest that something names. This shows that the content did not change. It says nothing about who wrote it.
3. **Producer authenticated.** A signature verifies against a producer that the workspace declares, and the subject digest of the statement does not differ from the file that it names.

The line between the second and third level is the one that matters. A digest is cheap and answers "is this the file that was recorded?". A signature answers "who recorded it?", and that is the question a skeptical reader asks of a build record, a deployment record, or a test result. Anyone who can write a file can write a statement that says a build succeeded. Only a signature from a declared producer is a reason to believe it.

The same distinction appears in the bundle. `export verify` prints `content verified by digest` for an archive whose files match their digests, and `producer authenticated` only when you also verify the signature of the bundle.

Claims ask for the level that fits the risk. Claims about what Concordia computes itself from committed files, such as a render or a NetworkPolicy evaluation, need at least a digest. Claims that someone else would otherwise assert (a plan, exact lineage, a deployed digest, runtime drift, the outcome of a check, a measurement) need an authenticated producer. A statement in a bare in-toto file, or in a DSSE envelope without a Sigstore bundle, cannot be verified, so it never goes above the lower levels. A cloud firewall result reads an unsigned plan at `content not verified`, and the result shows this integrity next to the digest of the plan, so you can see how much the result rests on.

## Admissibility

One engine decides whether evidence can support a claim, and it collects every reason that it cannot, not only the first. Evidence is admissible when all of these hold:

- Its producer is trusted for this kind of claim.
- Its integrity reaches the level that the claim needs.
- Its revision is the evaluated revision, and its environment is the environment of the obligation.
- It is not older than the limit that the workspace sets for the source.
- The working tree is the committed state that the statement describes.
- It has every field that its predicate requires.

Each condition answers a way that evidence goes wrong in practice. A statement about another revision describes other code. A statement about another environment describes another place. An old statement describes a world that has changed. A statement bound to a commit says nothing about uncommitted edits. A producer trusted to attest builds is not, for that reason, trusted to attest measurements: trust is scoped to the kind of claim, so that one stolen key does not vouch for everything.

Inadmissible evidence is never dropped silently and never counted. It stays in the report with every reason, and the obligation that needed it is `unknown` with the reason `no-evidence-supplied`. The reasons are details of that one reason and are not reasons themselves. The closed set of `unknown` reasons stays small, and the detail still tells the author which condition to fix. See [Verdicts and coverage](verdicts-and-coverage.md#assurance-evidence-reasons).

## Candidates never pass

Some facts look like evidence and are not. Concordia calls them candidates, shows them, and never lets one produce a `pass`.

- **A `TESTS` or `TESTS_FILE` edge.** The graph links a test to a tested node. The edge has no framework, no test name, and no confidence, and nothing in it says that a test ran or passed. Treating it as a passing check would make a test file that exists, and has been red for a month, count as verification.
- **A link from an image to a Dockerfile.** A build command names a Dockerfile for an image repository, so Concordia lists the Dockerfile as a lineage candidate. A tag can be moved, several Dockerfiles can build one repository, and nobody signed anything. The link does not show that the image in a cluster came from that Dockerfile.
- **Mentions, file references, and similarity edges.** These carry heuristic confidence by design and only suggest that two things are related.

A candidate still has a use: it narrows where to look. A `TESTS` edge suggests which CALM element a check result should cover, and Concordia shows that mapping next to a check that has no result. A Dockerfile link tells you where provenance would point. The rule is that a candidate can help a person find the real evidence, and it can never be the conclusion. Exact lineage needs an immutable image digest, a verified build statement that names it, and a builder and source repository that your trust contract lists. Anything less is a candidate.

## Why assurance rules start advisory

A rule that blocks a release must produce results that you can bound. A new evidence adapter has not yet earned that. Each claim belongs to a contract, and every contract is marked experimental until its fixture suite is listed as passing. While it is experimental, a finding is capped at `warning`, and the rule family has the maturity `advisory`. Setting such a rule to `mandatory` stops `check` with `CO-POL-018` and exit code 2. Only `advisory` and `shadow` are available.

The refusal is deliberate. Concordia could downgrade a `mandatory` entry to `advisory` and run on, but a gate that silently stops gating is worse than a gate that will not start. An early false failure from a new adapter would also teach a team to waive or disable the check, and that habit outlasts the bug. Running the rules as advisory shows the verdicts and the findings, so the team learns what the evidence says before any of it can break a build. The placement and trace rules of the infrastructure pack are refused as `mandatory` for the same reason: their profile families are `advisory` too.

Contracts also list what they do not claim, and that list is part of the contract. The provenance contract reads SLSA data and claims no SLSA level. The check-result contract claims an outcome, and it does not claim test adequacy or the absence of defects. The network-policy contract does not claim encryption in transit. A claim that no loaded contract enables is `unknown` with `claim-not-enabled`, never a pass.

## Why offline verification needs a trusted root

cosign 3 verifies a Sigstore bundle against the Sigstore trusted root: the certificate authorities and the transparency log keys that make a signature meaningful. By default cosign fetches that root from the network. Offline, it has none, so it cannot verify anything.

Concordia has two bad options at this point. It can skip verification, or it can accept the bundle as it is. Both turn "not verified" into "verified", which is exactly the error that integrity levels exist to prevent. Concordia takes a third path. When the workspace names a trusted root file in `assurance.verification.trustedRoot`, cosign verifies without network access. Without the file, Concordia does not start cosign. The statement stays below `producer authenticated`, with the detail `offline-verification-unavailable`, and the claim that needs it is `unknown`.

That outcome is not an error. A statement that cannot be verified is missing evidence, and the run degrades honestly. Signing is different. Under `--offline`, signing fails with `CO-WRK-016` and exit code 6, because signing is an action that you asked for and that cannot happen. A signed approval under `--offline` is `unknown` with the reason `verification-unavailable`. An approval by `protected-review` reads local git data only, and it works offline.

The trusted root is itself a trust input. It sits in the workspace, and it is reviewed like any other file there. Concordia's own releases are signed with cosign too, so the binary that runs in a trusted pipeline can be verified. That check also needs network access to the Sigstore trusted root, as [Install Concordia](../how-to/install.md) describes.

## The trust model

Concordia runs in trusted pipelines, started by trusted people, and the workspace file is reviewed like code. The design depends on this, and the tool does not try to remove that dependency.

The reason is that the workspace file declares what to trust: the producers, the builders, the source repositories, the trusted root, the trust anchor, and the rollback. If an attacker controls that file, the attacker can declare their own producer and sign whatever they like. Defending against that would need a root of trust outside the workspace, and the only candidate is the process that already protects your code, which is review and branch protection. Concordia does not try to guard its own configuration. It tries to make sure that, given a reviewed configuration, the evidence matches it.

What the producer contract protects against:

- Evidence files that changed after the producer made them, found by the digest, the signature, and the subject check.
- Evidence from a producer that you did not declare, even when its signature is valid.
- A declared producer that attests a kind of claim it is not trusted for.
- Evidence about another revision, another environment, or an earlier time.
- A build from a builder or a source repository that your trust contract does not list.

What it does not protect against:

- A change to the workspace file itself, such as a new producer, a new builder, a trust anchor that points at the candidate's own branch, or an added rollback. The review of that file is the control.
- A declared producer that signs something false. A signature says who made a statement and not whether it is true. A check result states that a check had an outcome, and Concordia reads no more into it than that.
- A stolen signing key or a hijacked CI identity of a declared producer.
- A runner that an attacker controls, because that runner can change the inputs and the binary before Concordia reads them.

The trust anchor of review currency follows the same stance. It protects against honest mistakes, such as an approval that was never merged or a reviewer list that a candidate edited, and it does not protect against a hostile candidate. See [Review currency](review-currency.md#a-candidate-cannot-approve-its-own-relaxation).

The position is useful because it is plain. A team that knows where the guarantees stop can put its review effort in the right place. The workspace file, the producer list, and the trusted root deserve the same scrutiny as the code that they vouch for.

## Related

- [How Concordia works](how-concordia-works.md)
- [Verdicts and coverage](verdicts-and-coverage.md)
- [Review currency](review-currency.md)
- [Fingerprints, baselines, and waivers](fingerprints-baselines-waivers.md)
- [Assurance reference](../reference/assurance.md)
- [Add stronger evidence](../how-to/add-stronger-evidence.md)
- [Review and approve assessments](../how-to/review-and-approve-assessments.md)
- [Assurance adapters release notes](../release-notes/assurance-adapters.md)
