#!/bin/bash
# apply-workspace.sh <src-dir> <scenario>: apply a scenario's reference workspace over the
# adopted /workspace/repo and commit it. When the new rules find pre-existing violations,
# record them in the baseline and commit that too.
set -euo pipefail
unset CI
src="$(cd "${1:?usage: apply-workspace.sh <src-dir> <scenario>}" && pwd)"
scenario="${2:?usage: apply-workspace.sh <src-dir> <scenario>}"
app="${CONCORDIA_BENCH_APP:-/workspace/repo}"
cd "$app"
derived="$("$(dirname "$0")/copy-workspace.sh" "$src" "$app")"
concordia lock update
concordia model validate
concordia index
concordia bind validate
# Upstream .gitignore files may match workspace files: force-add every file of the workspace
# and every file derived from the pinned checkout.
git add -A
(cd "$src" && find . -path ./.bench -prune -o -type f -printf '%P\0') | xargs -0 -r git add -f --
[ -z "$derived" ] || printf '%s\n' "$derived" | xargs -d '\n' git add -f --
git add -f .concordia.lock
git commit -q -m "chore: $scenario workspace"
concordia index
# Exit 1 means active mandatory findings that predate the scenario: baseline them.
status=0
concordia check --no-color >/dev/null || status=$?
if [ "$status" -eq 1 ]; then
  concordia baseline create --force
  git add -f exceptions/concordia.baseline.json
  git commit -q -m "chore: baseline existing findings of $scenario"
  concordia index
  # Some findings cannot be baselined (expired waivers, mandatory unknowns): fail the stage here.
  concordia check --no-color >/dev/null
elif [ "$status" -ne 0 ]; then
  echo "apply-workspace: concordia check exited $status" >&2
  exit "$status"
fi
