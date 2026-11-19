#!/bin/bash
unset CI
# Oracle: adopt Concordia from the reference workspace.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/adopt-workspace.sh /tmp/ws
