#!/bin/bash
# Oracle: gate every pr/ branch against main with Concordia and write review.json.
unset CI
set -euo pipefail
cd /workspace/repo
results=build/concordia/results.json
work="$(mktemp -d)"

# gate <ref> <slug> <check args...>: index the ref, run the check, keep its exit code, results and console.
gate() {
  local ref="$1" slug="$2"
  shift 2
  git checkout -q "$ref"
  rm -f "$results"
  concordia index
  local status=0
  concordia check "$@" --no-color > "$work/$slug.console" 2>&1 || status=$?
  echo "$status" > "$work/$slug.status"
  if [ -f "$results" ]; then cp "$results" "$work/$slug.json"; fi
}

gate main main
main_active="$(jq '[.findings[] | select(.disposition == "active") | .fingerprint]' "$work/main.json")"

review='{"branches": {}}'
while read -r branch; do
  slug="${branch//\//_}"
  gate "$branch" "$slug" --changed main
  status="$(cat "$work/$slug.status")"
  if [ -f "$work/$slug.json" ]; then
    entry="$(jq --argjson main "$main_active" --argjson status "$status" \
      '{verdict: (if $status == 0 then "pass" else "fail" end),
        violations: ([.findings[] | select(.disposition == "active" and ((.fingerprint | IN($main[])) | not)
                      and (.severity == "info" and .result == "unknown" | not))
                      | {ruleId, path: (.location.path // "")}] | unique),
        errors: []}' "$work/$slug.json")"
  else
    entry="$(grep -oE 'CO-[A-Z]+-[0-9]+' "$work/$slug.console" | sort -u | jq -R . | jq -s --argjson status "$status" \
      '{verdict: (if $status == 0 then "pass" else "fail" end), violations: [], errors: .}')"
  fi
  review="$(jq --arg b "$branch" --argjson e "$entry" '.branches[$b] = $e' <<< "$review")"
done < <(git for-each-ref --format='%(refname:short)' refs/heads/pr/)

echo "$review" | jq . > review.json
git checkout -q main
