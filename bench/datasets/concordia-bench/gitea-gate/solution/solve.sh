#!/bin/bash
# Oracle: gate the pull request, write the review, waive only the migration finding.
unset CI
set -euo pipefail
cd /workspace/repo
git checkout -q feature/bench-change
concordia index
concordia check --changed main --no-color || true
results=build/concordia/results.json
jq '{verdict: (if .summary.exitCode == 1 then "fail" else "pass" end),
     violations: ([.findings[] | select(.disposition == "active" and .severity == "error") | {ruleId, path: .location.path}] | unique)}' \
  "$results" > review.json
jq -r '[.findings[] | select(.disposition == "active" and .ruleId == "GITEA-MIG-001") | .fingerprint] | unique | .[]' "$results" |
while read -r fp; do
  concordia waiver add --finding "$fp" --owner team-gitea --reason "Migration rewrite is tracked and approved by the architecture board" \
    --issue GITEA-BENCH-1 --expires 2099-12-31
done
git add exceptions/waivers.yaml
git commit -q -m "chore: waive the accepted migration finding"
concordia index
concordia check --changed main --no-color || true
