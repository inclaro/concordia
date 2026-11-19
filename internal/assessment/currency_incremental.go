package assessment

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// Incremental turns on incremental evaluation (design D15). The zero value of EvalInput.Incremental, nil, is a full
// evaluation that reads and writes no file.
type Incremental struct {
	// Dir is the OS path of the cache directory, `<workspace>/.concordia/cache/impact` (impact.CacheDir).
	Dir string
	// AnchorRevision is the revision of the trust anchor: a member of the cache key. Empty when no anchor is
	// configured.
	AnchorRevision string
}

// IncrementalReport is what an incremental evaluation says about its cache. It is never set in a full evaluation.
type IncrementalReport struct {
	// Key is the digest of the cache key (the name of the cache file).
	Key string `json:"key"`
	// Status is `loaded`, `missing`, or `corrupt` (impact.CacheStatus).
	Status impact.CacheStatus `json:"status"`
	// Fallback says why the run was a full evaluation, empty when the cache was loaded (CO-ASM-010 carries it).
	Fallback string `json:"fallback,omitempty"`
	// Hits are the assessment versions whose stored comparison was reused, and Misses those that were evaluated.
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
	// ChangedItems are the items whose digest differs from the stored run, or that are new or gone.
	ChangedItems int `json:"changedItems"`
}

// itemComparison is the part of the currency of one assessment version that the incremental cache stores: the result
// of the comparison of the baseline with the current items and of the traversal from the changes. It depends on the
// entries, the dispositions, the graph, and the limits, never on the clock, the revocations, or the verification
// (design D8: these always run).
type itemComparison struct {
	Causes     []UnknownCause         `json:"causes"`
	Undisposed []impact.Change        `json:"undisposed"`
	Disposed   []impact.Disposed      `json:"disposed"`
	Triggers   []impact.ReviewTrigger `json:"triggers"`
	Cycles     []impact.Cycle         `json:"cycles"`
	// Changes are the added, modified, and undisposed removed items that make the version review-required.
	Changes  []impact.Change `json:"changes"`
	Analysis Analysis        `json:"analysis"`
}

// openCache opens the cache of the run. The key is the one of design D15; the members that the run does not know
// are empty, so a run without a policy digest has its own file.
func (e *evaluator) openCache() {
	inc := e.in.Incremental
	if inc == nil {
		return
	}

	key := impact.CacheKey{
		ContractVersion: e.contract.Version, CapabilityProfile: e.profile.ID, PolicyDigest: e.in.PolicyDigest,
		AuthorityDigest: e.in.AuthorityDigest, AnchorSHA: inc.AnchorRevision, EvaluatorVersions: map[string]string{},
	}

	if e.in.Facts != nil {
		for name, version := range e.in.Facts.EvaluatorVersions() {
			key.EvaluatorVersions[name] = version
		}
	}

	key.NormalizationVersion = key.EvaluatorVersions[impact.EvaluatorNormalization]

	e.cache = impact.OpenCache(inc.Dir, &key)

	if detail := e.cache.Detail(); detail != "" {
		e.note(diag.ASM010, diag.Detail(detail))
	}
}

// noteItems gives the cache the digests of every current item, once subjects exist.
func (e *evaluator) noteItems(versions []*Assessment) {
	if e.cache == nil {
		return
	}

	var entries []impact.Entry

	for _, a := range versions {
		entries = append(entries, e.subjects[a.Key()].entries()...)
	}

	e.cache.NoteItems(entries)
}

// finishCache saves the cache and reports it. A cache that cannot be written is a note: the evaluation is complete.
func (e *evaluator) finishCache(out *Evaluation) {
	if e.cache == nil {
		return
	}

	stats := e.cache.Stats()
	out.Incremental = &IncrementalReport{
		Key: e.cache.KeyDigest(), Status: e.cache.Status(), Hits: stats.Hits, Misses: stats.Misses,
		ChangedItems: stats.ChangedItems, Fallback: e.cache.Detail(),
	}

	if err := e.cache.Save(); err != nil {
		e.note(diag.ASM010, diag.Detail(fmt.Sprintf("the cache was not written: %v", err)))
	}
}

// digester gives the digester that seals items: the one of the cache, which remembers digests, in an incremental
// evaluation, else nil (Contract.Digest).
func (e *evaluator) digester() impact.Digester {
	if e.cache == nil {
		return nil
	}

	return e.cache.Digester(e.contract)
}

// graphDigest gives the digest of the dependency graph, once: it is complete before the first comparison.
func (e *evaluator) graphDigest() string {
	if e.graphSum == "" {
		e.graphSum = e.graph.Digest()
	}

	return e.graphSum
}

// fingerprint covers every input of an itemComparison that the cache key does not: the entries of both sides, the
// dispositions, the limits, the graph, and whether the scope was widened (an unresolved selector), which the stored
// comparison reports even when the widened scope resolves to the same items.
func (e *evaluator) fingerprint(doc *Assessment, subj *subject, baseline, current []impact.Entry, dispositions []impact.Disposition) string {
	limits := e.in.Limits.WithDefaults()

	return impact.NewHasher().Add(doc.Key(), e.graphDigest(), strconv.FormatBool(subj.widened)).
		AddInts(limits.MaxDepth, limits.MaxVisited, limits.MaxSeconds).
		AddEntries(baseline).AddEntries(current).AddDispositions(dispositions).Sum()
}

// compareWithCache is compareItems' comparison with the cache: a stored result with an equal fingerprint is reused,
// else the comparison is evaluated and stored. A traversal that a limit stopped is never stored: its result depends on
// the time that the limit measured. The stored result does not depend on the judging of the caller, so the comparison
// of a revoked or expired approval shares it.
func (e *evaluator) compareWithCache(doc *Assessment, subj *subject, pick *choice) itemComparison {
	baseline := entriesOf(pick.baseline(), subj.excluded)
	current := subj.entries()
	dispositions := dispositionsOf(doc, pick)

	if e.cache == nil {
		return e.evaluateComparison(doc, subj, baseline, current, dispositions)
	}

	sum := e.fingerprint(doc, subj, baseline, current, dispositions)

	if raw, ok := e.cache.Lookup(doc.Key(), sum); ok {
		var stored itemComparison
		if json.Unmarshal(raw, &stored) == nil {
			return stored
		}

		e.cache.Reject(doc.Key())
	}

	out := e.evaluateComparison(doc, subj, baseline, current, dispositions)

	if out.Analysis.Incomplete == nil {
		if raw, err := json.Marshal(out); err == nil {
			e.cache.Put(doc.Key(), sum, raw)
		}
	}

	return out
}

// countOutcomes sets the counters of the evaluation that the analysis object reports: the approvals that an
// invalidator made review-required, and the versions whose analysis is incomplete.
func countOutcomes(out *Evaluation) {
	invalidators := []string{
		DetailContractChanged, DetailPolicyChanged, DetailAuthorityChanged, DetailNormalizationChanged, DetailEvaluatorChanged,
	}

	for i := range out.Assessments {
		cur := &out.Assessments[i].Currency

		if slices.ContainsFunc(cur.Reasons, func(r string) bool { return slices.Contains(invalidators, r) }) {
			out.Invalidations++
		}

		if !cur.Analysis.Complete {
			out.IncompleteAnalyses++
		}
	}
}

// subjectDigest is SubjectDigest with the digest remembered in an incremental evaluation: the digest of the items that
// an approval lists depends on the items only.
func (e *evaluator) subjectDigest(items []Item) string {
	if e.cache == nil {
		return SubjectDigest(items)
	}

	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b Item) int {
		return cmp.Or(canonjson.CompareUTF16(string(a.Kind), string(b.Kind)), canonjson.CompareUTF16(a.Identity, b.Identity))
	})

	key := impact.NewHasher().Add("subject").AddInts(len(sorted))
	for _, it := range sorted {
		key.Add(string(it.Kind), it.Identity, it.Digest)
	}

	return e.cache.Remember(key.Sum(), func() string { return SubjectDigest(sorted) })
}
