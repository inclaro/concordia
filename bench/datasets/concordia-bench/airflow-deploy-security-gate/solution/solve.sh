#!/bin/bash
# Oracle: check main and each branch with Concordia and write the review.
unset CI
set -uo pipefail
cd /workspace/repo
branches=(pr/disable-scheduler-disruption-budget pr/sdk-reuses-own-definitions pr/sdk-uses-core-models)
out=/tmp/review
mkdir -p "$out"
results=build/concordia/results.json

check() { # <ref> <name>: index and check; keep results.json or the error codes
  git checkout -q "$1"
  rm -f "$results"
  concordia index > /dev/null 2>&1
  concordia check --no-color > "$out/$2.console" 2>&1
  echo $? > "$out/$2.exit"
  if [ -f "$results" ]; then cp "$results" "$out/$2.json"; else echo '{"findings": []}' > "$out/$2.json"; fi
}

check main main
echo '{"branches": {}}' > review.json
for b in "${branches[@]}"; do
  n="${b//\//_}"
  check "$b" "$n"
  code="$(cat "$out/$n.exit")"
  if [ -f "$results" ]; then errors='[]'; else errors="$(grep -oE 'CO-[A-Z]+-[0-9]+' "$out/$n.console" | sort -u | jq -R . | jq -s .)"; fi
  jq --arg b "$b" --argjson code "$code" --argjson errors "$errors" --slurpfile main "$out/main.json" --slurpfile br "$out/$n.json" '
    ([$main[0].findings[] | select(.disposition == "active") | .fingerprint]) as $known
    | .branches[$b] = {
        verdict: (if $code == 0 then "pass" else "fail" end),
        violations: ([$br[0].findings[] | select(.disposition == "active" and (.severity == "info" and .result == "unknown" | not) and ((.fingerprint as $f | $known | index($f)) | not))
                     | {ruleId, path: (.location.path // "")}] | unique),
        errors: $errors}' review.json > review.tmp && mv review.tmp review.json
done
git checkout -q main
