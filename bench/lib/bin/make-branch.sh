#!/bin/bash
# make-branch.sh <branch> <mutation>...: create <branch> from main with the mutations committed in order.
# Records "<branch> <commit>" for the branch and for main in $CONCORDIA_BENCH_STATE/branches.tsv,
# so the branches_unchanged check can prove that the agent left them as built.
set -euo pipefail
branch="${1:?usage: make-branch.sh <branch> <mutation>...}"
shift
app="${CONCORDIA_BENCH_APP:-/workspace/repo}"
state="${CONCORDIA_BENCH_STATE:-/opt/concordia-bench}"
git -C "$app" checkout -q -b "$branch" main
"$(dirname "$0")/apply-mutation.sh" "$@"
git -C "$app" checkout -q main
record="$state/branches.tsv"
touch "$record"
grep -v -e "^main " -e "^$branch " "$record" > "$record.tmp" || true
{
  cat "$record.tmp"
  printf 'main %s\n' "$(git -C "$app" rev-parse main)"
  printf '%s %s\n' "$branch" "$(git -C "$app" rev-parse "$branch")"
} > "$record"
rm -f "$record.tmp"
