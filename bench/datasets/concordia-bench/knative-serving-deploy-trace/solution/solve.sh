#!/bin/bash
# Oracle: add the deployment trace from the reference workspace.
set -euo pipefail
unset CI
/opt/concordia-bench/bin/apply-workspace.sh /solution/workspace deploy-trace
