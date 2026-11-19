#!/bin/bash
# copy-workspace.sh <src-dir> [<app>]: copy a reference workspace (without .bench/) over the app
# (default /workspace/repo), then rebuild its derived files from the pinned checkout
# (<src-dir>/.bench/derive.json, see concordia_bench/derive.py). Prints the derived paths, one per
# line: they are not in <src-dir>, and an upstream .gitignore may match them, so `git add -f` them.
set -euo pipefail
src="$(cd "${1:?usage: copy-workspace.sh <src-dir> [<app>]}" && pwd)"
app="$(cd "${2:-${CONCORDIA_BENCH_APP:-/workspace/repo}}" && pwd)"
home="${CONCORDIA_BENCH_HOME:-/opt/concordia-bench}"
find "$src" -mindepth 1 -maxdepth 1 ! -name .bench -exec cp -R {} "$app/" \;
if [ -f "$src/.bench/derive.json" ]; then
  PYTHONPATH="$home/python" /opt/venv/bin/python -m concordia_bench.derive apply "$src/.bench" "$app"
fi
