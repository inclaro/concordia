package assessment

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// reviewPredicateKey marks a policy file that holds review rules.
const reviewPredicateKey = "assessmentCurrency"

// AuthorityDigest gives the digest of an authority configuration: the sha256 of the canonical JSON of the
// authorities, sorted by id. An approval records it (`producedUnder.authorityDigest`), so a change of an authority
// makes the approvals that were produced under the old one `review-required`. The pipeline and
// `assessment approve` MUST both use it, with the authorities that the trust anchor gives (the working tree's
// only when no anchor is configured).
func AuthorityDigest(authorities []workspace.Authority) string {
	list := slices.Clone(authorities)
	if list == nil {
		list = []workspace.Authority{}
	}

	slices.SortFunc(list, func(x, y workspace.Authority) int { return canonjson.CompareUTF16(x.ID, y.ID) })

	data, err := canonjson.Marshal(list)
	if err != nil {
		return ""
	}

	return digest.OfBytes(data)
}

// PolicyDigest gives the digest of the policy that selects the review rules: the policy files that hold an
// `assessmentCurrency` predicate (the review-currency pack and any file of the workspace that defines review
// rules) with the rule overrides of the workspace for each, sorted by path. The digest of no file is the digest
// of an empty list, never the empty string. An approval records it (`producedUnder.policyDigest`). The pipeline and
// `assessment approve` MUST both use it.
func PolicyDigest(files []policy.PolicyFile) string {
	type entry struct {
		Overrides any    `json:"overrides"`
		Path      string `json:"path"`
		Digest    string `json:"digest"`
	}

	entries := []entry{}

	for i := range files {
		if !strings.Contains(files[i].Text, reviewPredicateKey) {
			continue
		}

		overrides := any(files[i].Overrides)
		if len(files[i].Overrides) == 0 {
			overrides = map[string]any{}
		}

		entries = append(entries, entry{Path: files[i].Path, Digest: digest.OfBytes([]byte(files[i].Text)), Overrides: overrides})
	}

	slices.SortFunc(entries, func(x, y entry) int { return canonjson.CompareUTF16(x.Path, y.Path) })

	data, err := canonjson.Marshal(entries)
	if err != nil {
		return ""
	}

	return digest.OfBytes(data)
}
