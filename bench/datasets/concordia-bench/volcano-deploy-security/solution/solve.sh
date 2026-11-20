#!/bin/bash
# Oracle: add the security and supply-chain obligations and a controlled copy of the dev manifests.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws deploy-security
