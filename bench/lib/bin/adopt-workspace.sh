#!/bin/bash
# adopt-workspace.sh <src-dir>: adopt Concordia on /workspace/repo from a reference workspace.
set -euo pipefail
src="$(cd "${1:?usage: adopt-workspace.sh <src-dir>}" && pwd)"
app="${CONCORDIA_BENCH_APP:-/workspace/repo}"
cd "$app"
calm="$(cd "$src" && ls architecture/*.calm.json)"
mkdir -p architecture
cp "$src/$calm" "$app/$calm"
concordia init --calm "$calm" --preset custom --ci github
derived="$("$(dirname "$0")/copy-workspace.sh" "$src" "$app")"
rm -f policies/default.policy.yaml .github/workflows/concordia.yml
rmdir --ignore-fail-on-non-empty .github/workflows .github 2>/dev/null || true
concordia lock update
concordia model validate
concordia index
concordia bind validate
# Upstream .gitignore files may match workspace files (browser-use ignores *.json).
git add -A
git add -f -- concordia.yaml .concordia.lock architecture bindings policies exceptions
[ -z "$derived" ] || printf '%s\n' "$derived" | xargs -d '\n' git add -f --
git commit -q -m "chore: adopt Concordia"
concordia index
# Exit 1 (active findings) is expected before the baseline; anything else is a failure.
concordia check --no-color || [ $? -eq 1 ]
concordia baseline create
git add -f exceptions/concordia.baseline.json
git commit -q -m "chore: baseline existing architecture findings"
concordia index
concordia check --no-color
# A clean exit is only meaningful when every rule was judged against bound code.
jq -e '.summary.rules | .notApplicable == 0 and .unknown == 0 and .error == 0 and (.pass + .violation) > 0' \
  build/concordia/results.json >/dev/null || { echo "adopt-workspace: rules not evaluated" >&2; exit 1; }
