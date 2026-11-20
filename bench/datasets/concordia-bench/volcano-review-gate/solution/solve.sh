#!/bin/bash
# Oracle: check main and every branch with Concordia and write review.json from the results.
unset CI
set -uo pipefail
cd /workspace/repo
results=build/concordia/results.json
mkdir -p /tmp/review
branches=(pr/track-admission-canary pr/stop-tracking-admission-init-job pr/add-security-reviewer pr/detect-cgroup-driver-in-admission)
check() { # <ref> <name>: check the ref, keep its exit code, results and console output
  git checkout -q "$1"
  rm -f "$results"
  concordia index > /dev/null 2>&1
  concordia check --no-color > "/tmp/review/$2.out" 2>&1
  echo $? > "/tmp/review/$2.exit"
  if [ -f "$results" ]; then cp "$results" "/tmp/review/$2.json"; else echo '{"findings": []}' > "/tmp/review/$2.json"; fi
}
check main main
echo '{"branches": {}}' > /tmp/review/review.json
for b in "${branches[@]}"; do
  n="${b//\//_}"
  check "$b" "$n"
  jq --arg b "$b" --argjson code "$(cat "/tmp/review/$n.exit")" \
     --slurpfile main /tmp/review/main.json --slurpfile cur "/tmp/review/$n.json" \
     --argjson errors "$(grep -o 'CO-[A-Z]*-[0-9]*' "/tmp/review/$n.out" | sort -u | jq -R . | jq -s .)" '
    ($main[0].findings | map(select(.disposition == "active") | .fingerprint)) as $known
    | .branches[$b] = {
        verdict: (if $code != 0 then "fail" else "pass" end),
        violations: ([$cur[0].findings[] | select(.disposition == "active" and (.severity == "info" and .result == "unknown" | not) and ((.fingerprint) as $f | $known | index($f) | not))
                      | {ruleId, path: (.location.path // "")}] | unique),
        errors: (if ($cur[0].findings | length) == 0 and $code != 0 then $errors else [] end)}' \
     /tmp/review/review.json > /tmp/review/next.json && mv /tmp/review/next.json /tmp/review/review.json
done
git checkout -q main
cp /tmp/review/review.json review.json
