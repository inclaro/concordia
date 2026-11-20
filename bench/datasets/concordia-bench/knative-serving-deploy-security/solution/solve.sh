#!/bin/bash
# Oracle: add the deployment security pack from the reference workspace.
set -euo pipefail
unset CI
/opt/concordia-bench/bin/apply-workspace.sh /solution/workspace deploy-security
