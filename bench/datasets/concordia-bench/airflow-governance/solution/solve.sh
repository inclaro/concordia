#!/bin/bash
unset CI
# Oracle: apply the governance workspace over the adopted repository.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws governance
