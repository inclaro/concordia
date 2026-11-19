#!/bin/bash
# Oracle: add quality scenarios, required areas and check results.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws quality
