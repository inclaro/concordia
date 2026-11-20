#!/bin/bash
# Oracle: gate each branch with `concordia check` against main and write review.json (not committed).
set -uo pipefail
unset CI
cd /workspace/repo
branches=(pr/restore-activator-secret-access pr/scan-symlinked-resources pr/reconciler-uses-api-types)
work="$(mktemp -d)"

# gate <ref> <key>: index and check <ref>; keep the exit code, the console output and results.json (or {}).
gate() {
  git checkout -q "$1"
  concordia index >/dev/null 2>&1
  rm -f build/concordia/results.json
  local rc=0
  concordia check --no-color >"$work/$2.console" 2>&1 || rc=$?
  echo "$rc" >"$work/$2.exit"
  if [ -s build/concordia/results.json ]; then cp build/concordia/results.json "$work/$2.json"; else echo '{}' >"$work/$2.json"; fi
}

# review <key>: the review entry of a branch. Violations are the active findings whose
# fingerprint is not an active finding on main, minus informational unknown findings; errors are the CO- codes of a refused run.
review() {
  local verdict=pass errors='[]'
  [ "$(cat "$work/$1.exit")" -ne 0 ] && verdict=fail
  if [ "$(jq 'length' "$work/$1.json")" -eq 0 ]; then
    errors="$(grep -o 'CO-[A-Z]*-[0-9]*' "$work/$1.console" | sort -u | jq -R . | jq -s .)"
  fi
  jq --slurpfile main "$work/main.json" --arg verdict "$verdict" --argjson errors "$errors" '
    ($main[0].findings // [] | map(select(.disposition == "active") | .fingerprint)) as $known
    | {verdict: $verdict,
       violations: ([(.findings // [])[]
                     | select(.disposition == "active")
                     | select((.severity == "info" and .result == "unknown") | not)
                     | select(.fingerprint as $f | ($known | index($f)) == null)
                     | {ruleId, path: (.location.path // "")}] | unique),
       errors: $errors}' "$work/$1.json"
}

gate main main
for b in "${branches[@]}"; do gate "$b" "${b//\//_}"; done
git checkout -q main
for b in "${branches[@]}"; do
  review "${b//\//_}" | jq --arg b "$b" '{($b): .}'
done | jq -s '{branches: add}' > review.json
