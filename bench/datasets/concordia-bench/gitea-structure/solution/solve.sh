#!/bin/bash
# Oracle: apply the reference structure workspace over the adopted repository.
unset CI
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws structure
