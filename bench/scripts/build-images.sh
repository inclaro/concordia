#!/bin/bash
# build-images.sh [--repo <id>]: build the base, repository, adopted and stage images.
#
# A stage image (concordia-bench-<id>-<scenario>:latest) holds the oracle state of a
# scenario that later tasks start from: bench/tasks.json names it as their `stage`.
set -euo pipefail
root="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
bench="$root/bench"
only=""
if [ "${1:-}" = "--repo" ]; then only="${2:?--repo needs an id}"; fi

for tool in "$root/.bin/go/bin/go" "$root/.bin/codebase-memory-mcp"; do
  [ -x "$tool" ] || { echo "build-images: missing $tool; run mise run tools:install" >&2; exit 1; }
done

# A private context directory: concurrent builds (one per repository) must not share it.
mkdir -p "$bench/.build"
ctx="$(mktemp -d "$bench/.build/base.XXXXXX")"
trap 'rm -rf "$ctx"' EXIT
mkdir -p "$ctx/lib"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$root/.bin/go/bin/go" build -trimpath \
  -ldflags "-X main.version=bench-$(git rev-parse --short HEAD)" -o "$ctx/concordia" ./cmd/concordia)
cp "$root/.bin/codebase-memory-mcp" "$ctx/"
cp -R "$root/docs" "$ctx/docs"
cp -R "$bench/lib/concordia_bench" "$bench/lib/bin" "$ctx/lib/"
find "$ctx/lib" -name __pycache__ -type d -prune -exec rm -rf {} +
cp "$bench/environment/Dockerfile.base" "$ctx/Dockerfile"
docker build -t concordia-bench-base:latest "$ctx"

# build_stage <id> <scenario>: build concordia-bench-<id>-<scenario>:latest from that scenario's oracle
# workspace, after the stage image its own task starts from (dependency order, each built once).
declare -A built=()
build_stage() {
  local id="$1" scenario="$2" from
  [ -n "${built[$id/$scenario]:-}" ] && return 0
  from="$(jq -r --arg r "$id" --arg s "$scenario" '.[] | select(.repo == $r and .scenario == $s) | .stage' "$bench/tasks.json")"
  case "$from" in "" | adopted) ;; *) build_stage "$id" "$from" ;; esac
  docker build -f "$bench/environment/Dockerfile.stage" -t "concordia-bench-$id-$scenario:latest" \
    --build-arg "FROM_IMAGE=concordia-bench-$id${from:+-$from}:latest" --build-arg "SCENARIO=$scenario" \
    "$bench/datasets/concordia-bench/$id-$scenario/solution/workspace"
  built[$id/$scenario]=1
}

jq -r '.[] | [.id, .url, .sha, .tag] | @tsv' "$bench/repos.json" |
while IFS=$'\t' read -r id url sha tag; do
  if [ -n "$only" ] && [ "$only" != "$id" ]; then continue; fi
  docker build -f "$bench/environment/Dockerfile.repo" -t "concordia-bench-$id:latest" \
    --build-arg "REPO_ID=$id" --build-arg "REPO_URL=$url" \
    --build-arg "REPO_SHA=$sha" --build-arg "REPO_TAG=$tag" "$bench"
  docker build -f "$bench/environment/Dockerfile.adopted" -t "concordia-bench-$id-adopted:latest" \
    --build-arg "REPO_ID=$id" "$bench/datasets/concordia-bench/$id-adopt/solution/workspace"
  # Every stage that a task of this repository starts from (tasks.json `stage` other than ""/adopted).
  for scenario in $(jq -r --arg r "$id" '[.[] | select(.repo == $r) | .stage | select(. != "" and . != "adopted")] | unique | .[]' "$bench/tasks.json"); do
    build_stage "$id" "$scenario"
  done
done
