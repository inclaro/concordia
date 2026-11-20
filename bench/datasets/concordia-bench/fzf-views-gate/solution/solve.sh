#!/bin/bash
# Oracle: gate every branch against main with Concordia and write the review.
set -euo pipefail
unset CI
cd /workspace/repo
branches="pr/draw-protector-using-utilities pr/renderer-uses-matcher pr/tweak-generated-mermaid pr/tweak-generated-json pr/tui-measures-text"
mkdir -p /tmp/review
results=build/concordia/results.json

# gate <ref> <name>: check the ref and keep its exit code, results and console output.
gate() {
  git checkout -q "$1"
  rm -f "$results"
  concordia index > /dev/null
  code=0
  concordia check --no-color > "/tmp/review/$2.out" 2>&1 || code=$?
  echo "$code" > "/tmp/review/$2.exit"
  if [ -f "$results" ]; then cp "$results" "/tmp/review/$2.json"; else echo '{"findings": []}' > "/tmp/review/$2.json"; fi
}

gate main main
jq -r '[.findings[] | select(.disposition == "active") | .fingerprint] | unique' /tmp/review/main.json > /tmp/review/main-active.json
echo '{"branches": {}}' > review.json
for b in $branches; do
  n="${b//\//_}"
  gate "$b" "$n"
  code="$(cat "/tmp/review/$n.exit")"
  # Errors: the codes Concordia reports when it refuses to check (no results.json).
  errors='[]'
  if [ ! -f "$results" ]; then errors="$(grep -oE 'CO-[A-Z]+-[0-9]+' "/tmp/review/$n.out" | sort -u | jq -R . | jq -s .)"; fi
  jq --arg b "$b" --argjson code "$code" --argjson errors "$errors" --slurpfile base /tmp/review/main-active.json --slurpfile cur "/tmp/review/$n.json" \
    '.branches[$b] = {
       verdict: (if $code == 0 then "pass" else "fail" end),
       violations: ([$cur[0].findings[] | select(.disposition == "active" and (.severity == "info" and .result == "unknown" | not) and ((.fingerprint) as $f | ($base[0] | index($f)) | not))
                     | {ruleId, path: (.location.path // "")}] | unique),
       errors: $errors}' review.json > review.json.tmp
  mv review.json.tmp review.json
done
git checkout -q main
