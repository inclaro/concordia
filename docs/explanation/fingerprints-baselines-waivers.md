# Fingerprints, baselines, and waivers

Adopting an architecture check on an existing codebase means living with violations you cannot fix today. Baselines and waivers let you do that without turning the check off. Both rest on fingerprints. This page explains why they work the way they do. To do it, see [Manage baselines and waivers](../how-to/manage-baselines-and-waivers.md).

## Fingerprints identify a finding

Each finding gets a fingerprint: a SHA-256 hash of a version tag and these inputs, in this order:

1. The rule ID.
2. The source and target architecture nodes.
3. The semantic evidence kind, for example `imports`, `calls`, `routes-to`, or `deploys-artifact`. This is the kind that Concordia assigns (see [Canonical edge kinds](../reference/architecture.md#canonical-edge-kinds)), never the provider's native edge type. A finding that is not about one edge uses a fixed word instead (`cycle`, `path`, `unobserved`, `budget:<measure>`).
4. The source and target qualified names.
5. The repository path of the source file.

For a finding of a typed obligation (a rule with an `obligation`), the hash also covers the environment of the obligation and the occurrence discriminator, for example a container name. The same rule and subject then give one fingerprint for `staging` and another for `production`, and one for each container of a workload.

The version tag is the first hashed input. When the list of inputs or the hashing contract changes, the tag changes, so every fingerprint changes visibly and the golden vectors in `test/golden/fingerprints.json` change in the same commit. There is one definition and no mode switch; there is no fingerprint migration.

Line and column numbers are deliberately not inputs. Edit the top of a file and every line below it moves, but the violation is still the same violation. If line numbers were included, each unrelated edit would make an old finding look resolved and a "new" one appear, and baselines would rot constantly. Renaming a symbol or moving the file does change the fingerprint, because then the finding really is about different code. See the [exceptions reference](../reference/exceptions.md) for the stored format.

## The ratchet

A baseline records the fingerprints of the findings that exist now. Findings in the baseline get the disposition `baselined` and do not fail the run; anything else is `active` and does. A normal `check` only reads the baseline and never adds entries, so quality ratchets in one direction: existing debt is tolerated, new debt is not. A baseline gets a new entry only when someone runs `concordia baseline update --accept` on purpose, and that change is a reviewed edit to the file.

## Why waivers expire

A baseline entry says "this is old". A waiver says "this is accepted, for a reason, by someone, until a date". The owner, reason, and expiry make the decision accountable. The expiry exists because temporary exceptions otherwise become permanent by neglect. Once the date passes, the finding becomes `expired-waiver` and fails the run again, which forces someone to fix it or consciously renew it. A waiver lasts through the whole `expires` day, in UTC.

When a finding matches several exceptions, the strongest claim wins: waived, then expired-waiver, then baselined, then active.

## Why a scope needs an approver

A waiver by fingerprint covers one finding that someone has looked at. Its reach is as small as it can be, so an owner, a reason, and a date are enough. A scoped waiver is different. It covers every finding of one rule that matches a subject, an environment, or an obligation, including findings that do not exist yet. It also has no fingerprint, so renaming a symbol does not orphan it. That reach and that durability are why it asks for more: an approver whom Concordia can verify, not a name typed into a file.

The approver is checked with the mechanisms and the trust anchor that assessment approvals use (see [Review currency](review-currency.md#a-candidate-cannot-approve-its-own-relaxation)). Three choices follow from the same reasoning.

- **The authority has its own role.** A waiver needs an authority of role `exception`, kept apart from the authorities of role `reviewer`. Someone who may approve a design may not, for that reason alone, excuse a failing rule. A waiver of `REV-GOV-001` needs a role `governance`, because that rule exists to stop a candidate from loosening the checks that decide its own approval, and an excuse for it must not come from the same people.
- **The authority is read from the trust anchor.** The list of approvers comes from the workspace file at the anchor, not from the candidate. A candidate that adds itself as an approver is not believed. With `protected-review`, a waiver also counts only after it is merged into the anchor, so the review of the pull request is the approval. With `signed`, the entry needs a signature from a signer of the authority.
- **Failure applies nothing.** A waiver whose approver cannot be verified is not applied. Its findings keep their disposition, and a warning names the waiver and the reason. A broken waiver therefore makes a run stricter, never more lenient.

A scoped waiver is also narrower in the places where narrowness is cheap. A scope with no key is not valid, wildcards are refused, and a waiver for `staging` does not cover `production`. The workspace can cap how far in the future `expires` may lie with `exceptions.maxExpiryDays`. The cap does not apply to a waiver by fingerprint, because that kind covers less.

A scoped waiver can list compensating evidence: a file and its digest that show what makes the risk acceptable. Concordia checks that the file exists and that the digest matches, and warns when either fails. It never weighs the content, and it never changes a verdict. The evidence is for the people who review the waiver.

Like every waiver, a scoped waiver changes the disposition of a finding and never its verdict. A waived `unknown` is still `unknown`, and it still gives exit code 4 where the rule fails on unknown, because a waiver is a statement about people's tolerance, and `unknown` is a statement about evidence. See the [exceptions reference](../reference/exceptions.md#approver-verification).

## Why CI cannot accept baseline entries

Adding to a baseline is the same as saying "this violation is fine". If a CI job could do that automatically, a failing pipeline could be made green by the pipeline itself, and the ratchet would mean nothing. So when the `CI` environment variable is set, `baseline update --accept` is refused (`CO-EXC-008`). A person accepts a finding locally, with a reason and an owner, and the change goes through code review like any other.

## Related

- [How Concordia works](how-concordia-works.md)
- [Evidence and trust](evidence-and-trust.md)
- [Review currency](review-currency.md)
- [Brownfield adoption tutorial](../tutorials/brownfield-adoption.md)
- [Run in CI](../how-to/run-in-ci.md)
- [Exceptions reference](../reference/exceptions.md)
