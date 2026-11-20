#!/bin/bash
# Oracle: gate every pr/* branch against main with Concordia and write review.json from its output.
set -uo pipefail
unset CI
cd /workspace/repo
out=/tmp/gate
mkdir -p "$out"

# check_ref <ref> <name>: check the ref out, index, check; keep the exit code, console and results.json.
check_ref() {
  git checkout -q "$1"
  rm -f build/concordia/results.json
  concordia index > /dev/null 2>&1
  concordia check --no-color > "$out/$2.console" 2>&1
  echo $? > "$out/$2.exit"
  if [ -f build/concordia/results.json ]; then cp build/concordia/results.json "$out/$2.json"; else rm -f "$out/$2.json"; fi
}

check_ref main main
echo '{"branches": {}}' > review.json
for branch in $(git for-each-ref --format='%(refname:short)' 'refs/heads/pr/*'); do
  name="${branch//\//_}"
  check_ref "$branch" "$name"
  code="$(cat "$out/$name.exit")"
  verdict=pass
  [ "$code" -ne 0 ] && verdict=fail
  if [ -f "$out/$name.json" ]; then
    violations="$(jq -c --slurpfile main "$out/main.json" '
      ($main[0].findings | map(select(.disposition == "active") | .fingerprint)) as $known
      | [.findings[] | select(.disposition == "active" and ((.severity == "info" and .result == "unknown") | not) and ((.fingerprint as $f | $known | index($f)) == null))
         | {ruleId, path: (.location.path // "")}] | unique' "$out/$name.json")"
    errors='[]'
  else
    violations='[]'
    errors="$(grep -oE 'CO-[A-Z]+-[0-9]+' "$out/$name.console" | sort -u | jq -R . | jq -sc .)"
  fi
  jq --arg b "$branch" --arg v "$verdict" --argjson viol "$violations" --argjson err "$errors" \
    '.branches[$b] = {verdict: $v, violations: $viol, errors: $err}' review.json > review.json.tmp
  mv review.json.tmp review.json
done
git checkout -q main
