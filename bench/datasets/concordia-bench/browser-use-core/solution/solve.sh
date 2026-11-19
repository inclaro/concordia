#!/bin/bash
unset CI
# Oracle: apply the core reference workspace.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws core
