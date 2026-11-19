#!/bin/bash
# Oracle: apply the reference workspace of the governance scenario.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws governance
