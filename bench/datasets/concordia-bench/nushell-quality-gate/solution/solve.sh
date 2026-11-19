#!/bin/bash
# Oracle: check main and every branch with Concordia and write the review.
unset CI
set -euo pipefail
cd /workspace/repo

check_branch() { # <branch> <outdir>
  mkdir -p "$2"
  git checkout -q "$1"
  concordia index > "$2/index.log" 2>&1 || true
  rm -f build/concordia/results.json
  set +e
  concordia check --no-color > "$2/check.log" 2>&1
  echo $? > "$2/exit"
  set -e
  if [ -f build/concordia/results.json ]; then cp build/concordia/results.json "$2/results.json"; fi
}

out=/tmp/gate
check_branch main "$out/main"
branches=$(git for-each-ref --format='%(refname:short)' refs/heads/pr/)
for b in $branches; do check_branch "$b" "$out/$b"; done
git checkout -q main

# findings that are active on main
jq -r '[.findings[] | select(.disposition == "active") | .fingerprint]' "$out/main/results.json" > "$out/main-active.json"

review='{"branches": {}}'
for b in $branches; do
  d="$out/$b"
  code=$(cat "$d/exit")
  verdict=pass; [ "$code" -ne 0 ] && verdict=fail
  if [ -f "$d/results.json" ]; then
    violations=$(jq --slurpfile base "$out/main-active.json" \
      '[.findings[] | select(.disposition == "active" and ((.severity == "info" and .result == "unknown") | not) and ((.fingerprint as $f | $base[0] | index($f)) | not)) | {ruleId, path: (.location.path // "")}] | unique' "$d/results.json")
    errors='[]'
  else
    violations='[]'
    errors=$(grep -oE 'CO-[A-Z]+-[0-9]+' "$d/check.log" | sort -u | jq -R . | jq -s .)
  fi
  review=$(jq --arg b "$b" --arg v "$verdict" --argjson viol "$violations" --argjson err "$errors" \
    '.branches[$b] = {verdict: $v, violations: $viol, errors: $err}' <<< "$review")
done
echo "$review" > review.json
