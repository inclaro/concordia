package e2e_test

import "testing"

// A33: an image with a tag and no digest has no immutable identity: the exact-lineage claim is unknown with the missing
// items, no statement is linked to it, and a tag that moved after the build is not connected to the attestation.
func TestAssuranceA33MutableTagWithoutDigestOrProvenance(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	t.Run("tag and no digest", func(t *testing.T) {
		t.Parallel()

		w := &asrWorld{
			t: t, attestations: []string{asrShop},
			records: []asrRecord{{path: "deploy-1.json", digest: asrDigestA}, {path: "observed-1.json", digest: asrDigestA}},
		}
		r := w.build()
		doc, _ := r.check()

		// The result: the tagged container is unknown with the reason, the pinned container passes, and the rule is unknown.
		sidecar := requireAsrLinTagNoDigestResult(t, doc)

		// The explanation names the missing items and the occurrence and says nothing about a version.
		out := r.explain("ASR-PRV-001").output()
		containsAll(t, out, "Verdict: unknown", "no-evidence-supplied", "missing digest", "missing provenance",
			"Deployment/payments/api (sidecar)", "assurance-render/production/payments.yaml")

		// The provenance: no statement is linked to the tagged occurrence, and the bundle records the same statements
		// as the run.
		requireAsrLinTagNoDigestProvenance(t, r, doc, sidecar)
	})

	t.Run("tag moved after the build", func(t *testing.T) {
		t.Parallel()

		// The attestation names repository@sha256:aaa; the manifest of both containers has the tag only.
		w := &asrWorld{t: t, attestations: []string{asrShop}, image: asrRepository + ":1.4", sidecar: asrRepository + ":1.4"}
		r := w.build()
		doc, _ := r.check()

		requireAsrLinTagMoved(t, r, doc)
	})
}

// A34: verified provenance from a trusted producer whose builder and source are in the trust contract, and a deployment
// record and an observation that name the same digest. The lineage passes and names the contract; the build step of
// the trace is verified; and no output states a SLSA level.
func TestAssuranceA34VerifiedProvenanceAndDeploymentDigestAgree(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	t.Run("agree", func(t *testing.T) {
		t.Parallel()

		r := asrLineageWorld(t).build()
		doc, res := r.check()

		requireAsrLinAgreeOutputs(t, r, doc, res)
		digest := requireAsrLinAgreeContainers(t, doc)
		requireAsrLinAgreeTrace(t, r)

		// The provenance: the persisted statement of the lineage is in the bundle with the digest as its subject.
		dir, paths := r.bundleFiles()
		requireStatementInBundle(t, dir, paths, digest, "sha256:"+asrHexA)
	})

	t.Run("deployed digest differs from the observed digest", func(t *testing.T) {
		t.Parallel()

		w := asrLineageWorld(t)
		w.records[2] = asrRecord{path: "observed-1.json", digest: asrDigestB}
		r := w.build()
		doc, _ := r.check()

		requireAsrLinDrift(t, r, doc)
	})
}
