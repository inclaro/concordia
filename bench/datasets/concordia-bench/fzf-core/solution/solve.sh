#!/bin/bash
# Oracle: apply the core rule pack, record pre-existing findings in the baseline.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws core
