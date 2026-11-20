#!/bin/bash
unset CI
# Oracle: apply the deploy-security workspace over the previous stage.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws deploy-security
