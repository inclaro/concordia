#!/bin/bash
# Oracle: add the structure policy pack from the reference workspace.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws structure
