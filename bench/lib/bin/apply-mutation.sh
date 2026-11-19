#!/bin/bash
# apply-mutation.sh <name>...: commit verifier-owned mutations on the current branch of /workspace/repo.
set -euo pipefail
[ $# -gt 0 ] || { echo "usage: apply-mutation.sh <name>..." >&2; exit 2; }
home="${CONCORDIA_BENCH_HOME:-/opt/concordia-bench}"
PYTHONPATH="$home/python" exec /opt/venv/bin/python -m concordia_bench.mutate "$@"
