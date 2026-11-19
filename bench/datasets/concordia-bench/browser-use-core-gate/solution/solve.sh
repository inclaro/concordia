#!/bin/bash
# Oracle: gate `main` and every branch with Concordia, then write review.json from the results.
unset CI
set -uo pipefail
cd /workspace/repo
results=build/concordia/results.json
mkdir -p /tmp/gate

# gate <ref> <name>: run the gate on <ref>; keep its results.json (or {}) and its console output.
gate() {
  git checkout -q "$1"
  rm -f "$results"
  { concordia index && concordia check --no-color; } >"/tmp/gate/$2.console" 2>&1
  echo $? >"/tmp/gate/$2.exit"
  if [ -s "$results" ]; then cp "$results" "/tmp/gate/$2.json"; else echo '{}' >"/tmp/gate/$2.json"; fi
}

gate main main
echo '{"branches": {}}' >review.json
for branch in $(git for-each-ref --format='%(refname:short)' refs/heads/pr/); do
  name="${branch//\//_}"
  gate "$branch" "$name"
  jq --arg b "$branch" --slurpfile base /tmp/gate/main.json --slurpfile now "/tmp/gate/$name.json" \
     --argjson exit "$(cat "/tmp/gate/$name.exit")" \
     --argjson errors "$(grep -oE 'CO-[A-Z]+-[0-9]+' "/tmp/gate/$name.console" | sort -u | jq -R . | jq -s .)" '
    ([$base[0].findings[]? | select(.disposition == "active") | .fingerprint]) as $known
    | .branches[$b] = {
        verdict: (if $exit == 0 then "pass" else "fail" end),
        violations: ([$now[0].findings[]? | select(.disposition == "active" and (.severity == "info" and .result == "unknown" | not) and (.fingerprint as $f | $known | index($f) | not))
                      | {ruleId, path: (.location.path // "")}] | unique),
        errors: (if ($now[0] | length) == 0 then $errors else [] end)}' review.json >review.json.new
  mv review.json.new review.json
done
git checkout -q main
